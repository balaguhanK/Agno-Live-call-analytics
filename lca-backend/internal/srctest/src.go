// Package srctest is a minimal SIPREC recording client (SRC) for testing the
// recorder without a PBX: it places a recording session and streams one
// G.711 μ-law RTP stream per participant, then hangs up.
package srctest

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/pion/rtp"

	"lca-backend/internal/siprec"
)

const (
	PacketDuration = 20 * time.Millisecond
	samplesPerPkt  = siprec.ClockRate / 50 // 20 ms at 8 kHz
)

// Party is one call participant and the audio they speak (8 kHz mono PCM).
type Party struct {
	AOR   string // e.g. sip:BobS@pbx.example, tel:+17035550100
	Name  string
	Audio []int16
}

type Call struct {
	SRS     string // recorder's SIP address, host:port
	LocalIP string // this machine's address, as seen by the recorder
	Parties []Party
	// "tcp" (default; SIPREC INVITEs are too big for UDP) or "udp"
	Transport string
	// Optional progress messages
	Log func(format string, args ...any)
}

func (c Call) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

// Run places the recording session, streams all parties' audio in real
// time, then sends BYE.
func Run(ctx context.Context, c Call) error {
	if len(c.Parties) == 0 {
		return errors.New("no parties")
	}
	host, portStr, err := net.SplitHostPort(c.SRS)
	if err != nil {
		return fmt.Errorf("SRS address: %w", err)
	}
	srsPort, _ := strconv.Atoi(portStr)

	ua, err := sipgo.NewUA(sipgo.WithUserAgent("lca-test-src"))
	if err != nil {
		return err
	}
	defer ua.Close()
	cli, err := sipgo.NewClient(ua, sipgo.WithClientHostname(c.LocalIP))
	if err != nil {
		return err
	}
	dialogs := sipgo.NewDialogClientCache(cli, sip.ContactHeader{Address: sip.Uri{User: "src", Host: c.LocalIP}})

	// A local socket per party to send RTP from
	conns := make([]net.PacketConn, len(c.Parties))
	offer := make([]siprec.AnswerStream, len(c.Parties))
	meta := make([]siprec.Participant, len(c.Parties))
	for i, p := range c.Parties {
		conn, err := net.ListenPacket("udp", net.JoinHostPort(c.LocalIP, "0"))
		if err != nil {
			return err
		}
		defer conn.Close()
		conns[i] = conn
		label := strconv.Itoa(i + 1)
		offer[i] = siprec.AnswerStream{Label: label, Port: conn.LocalAddr().(*net.UDPAddr).Port, Payload: siprec.PayloadPCMU}
		meta[i] = siprec.Participant{AOR: p.AOR, Name: p.Name, Label: label}
	}
	body, contentType, err := siprec.BuildBody(siprec.BuildOffer(c.LocalIP, offer), siprec.BuildMetadata(randomID(), meta))
	if err != nil {
		return err
	}

	transport := c.Transport
	if transport == "" {
		transport = "tcp"
	}
	recipient := sip.Uri{User: "srs", Host: host, Port: srsPort, UriParams: sip.NewParams()}
	recipient.UriParams.Add("transport", transport)
	c.logf("INVITE sip:srs@%s over %s (SIPREC, %d streams)", c.SRS, transport, len(c.Parties))
	sess, err := dialogs.Invite(ctx, recipient, body,
		sip.NewHeader("Content-Type", contentType), sip.NewHeader("Require", "siprec"))
	if err != nil {
		return fmt.Errorf("INVITE: %w", err)
	}
	defer sess.Close()
	if err := sess.WaitAnswer(ctx, sipgo.AnswerOptions{}); err != nil {
		return fmt.Errorf("recorder refused the session: %w", err)
	}
	answer, err := siprec.ParseSDP(sess.InviteResponse.Body())
	if err != nil {
		return fmt.Errorf("bad SDP answer: %w", err)
	}
	if err := sess.Ack(ctx); err != nil {
		return fmt.Errorf("ACK: %w", err)
	}
	dest := map[string]*net.UDPAddr{}
	for _, s := range answer.Streams {
		dest[s.Label] = &net.UDPAddr{IP: net.ParseIP(answer.ConnectionIP), Port: s.Port}
	}
	c.logf("200 OK; streaming RTP to %s", answer.ConnectionIP)

	// All streams share one clock so they stay in step, like a real call
	longest := 0
	for _, p := range c.Parties {
		longest = max(longest, len(p.Audio))
	}
	packets := (longest + samplesPerPkt - 1) / samplesPerPkt
	senders := make([]*rtpSender, len(c.Parties))
	for i := range c.Parties {
		addr, ok := dest[offer[i].Label]
		if !ok {
			return fmt.Errorf("recorder didn't accept stream %s", offer[i].Label)
		}
		senders[i] = newSender(conns[i], addr)
	}
	tick := time.NewTicker(PacketDuration)
	defer tick.Stop()
	for n := 0; n < packets; n++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		var wg sync.WaitGroup
		for i, p := range c.Parties {
			wg.Add(1)
			go func() {
				defer wg.Done()
				senders[i].send(frame(p.Audio, n))
			}()
		}
		wg.Wait()
	}

	c.logf("BYE after %.1f s of audio", float64(packets)*PacketDuration.Seconds())
	byeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return sess.Bye(byeCtx)
}

// frame returns the n-th 20 ms of audio as μ-law, padding with silence.
func frame(audio []int16, n int) []byte {
	out := make([]byte, samplesPerPkt)
	for i := range out {
		j := n*samplesPerPkt + i
		if j < len(audio) {
			out[i] = siprec.MulawEncode(audio[j])
		} else {
			out[i] = siprec.MulawSilence
		}
	}
	return out
}

type rtpSender struct {
	conn net.PacketConn
	dest *net.UDPAddr
	pkt  rtp.Packet
}

func newSender(conn net.PacketConn, dest *net.UDPAddr) *rtpSender {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return &rtpSender{conn: conn, dest: dest, pkt: rtp.Packet{Header: rtp.Header{
		Version:        2,
		PayloadType:    siprec.PayloadPCMU,
		SequenceNumber: binary.BigEndian.Uint16(b[0:]),
		Timestamp:      binary.BigEndian.Uint32(b[2:]),
		SSRC:           binary.BigEndian.Uint32(b[4:]),
	}}}
}

func (s *rtpSender) send(payload []byte) {
	s.pkt.Payload = payload
	if data, err := s.pkt.Marshal(); err == nil {
		_, _ = s.conn.WriteTo(data, s.dest)
	}
	s.pkt.SequenceNumber++
	s.pkt.Timestamp += samplesPerPkt
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x", b)
}
