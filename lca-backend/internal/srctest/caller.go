package srctest

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"lca-backend/internal/siprec"
)

// PhoneCall is an ordinary SIP call, like a softphone dialling a number.
type PhoneCall struct {
	Server   string // SIP server host:port, e.g. FreeSWITCH 192.168.24.136:5060
	LocalIP  string
	User     string // SIP account, e.g. 1001
	Password string
	To       string  // number to dial, e.g. 5000
	Audio    []int16 // 8 kHz mono PCM to speak after answer
	// How long to wait for an answer
	RingTimeout time.Duration
	Log         func(format string, args ...any)
}

func (c PhoneCall) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

// Dial places the call, speaks Audio once answered, then hangs up.
func Dial(ctx context.Context, c PhoneCall) error {
	host, portStr, err := net.SplitHostPort(c.Server)
	if err != nil {
		return err
	}
	port, _ := strconv.Atoi(portStr)

	ua, err := sipgo.NewUA(sipgo.WithUserAgent("lca-test-caller"))
	if err != nil {
		return err
	}
	defer ua.Close()
	cli, err := sipgo.NewClient(ua, sipgo.WithClientHostname(c.LocalIP))
	if err != nil {
		return err
	}
	// Answer FreeSWITCH's in-dialog requests (BYE if the agent hangs up first)
	srv, err := sipgo.NewServer(ua)
	if err != nil {
		return err
	}
	sipConn, err := net.ListenPacket("udp", net.JoinHostPort(c.LocalIP, "0"))
	if err != nil {
		return err
	}
	sipPort := sipConn.LocalAddr().(*net.UDPAddr).Port
	dialogs := sipgo.NewDialogClientCache(cli, sip.ContactHeader{Address: sip.Uri{User: c.User, Host: c.LocalIP, Port: sipPort}})
	hungUp := make(chan struct{})
	srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		_ = dialogs.ReadBye(req, tx)
		select {
		case <-hungUp:
		default:
			close(hungUp)
		}
	})
	go func() { _ = srv.ServeUDP(sipConn) }()
	defer sipConn.Close()

	rtpConn, err := net.ListenPacket("udp", net.JoinHostPort(c.LocalIP, "0"))
	if err != nil {
		return err
	}
	defer rtpConn.Close()
	// Drain what the agent says; this caller doesn't play it
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := rtpConn.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	offer := []byte(fmt.Sprintf("v=0\r\no=caller 1 1 IN IP4 %[1]s\r\ns=call\r\nc=IN IP4 %[1]s\r\nt=0 0\r\n"+
		"m=audio %[2]d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\na=sendrecv\r\n", c.LocalIP, rtpConn.LocalAddr().(*net.UDPAddr).Port))

	from := sip.NewHeader("From", fmt.Sprintf("<sip:%s@%s>;tag=%s", c.User, host, randomID()))
	c.logf("calling %s as %s", c.To, c.User)
	sess, err := dialogs.Invite(ctx, sip.Uri{User: c.To, Host: host, Port: port}, offer,
		sip.NewHeader("Content-Type", siprec.ContentTypeSDP), from)
	if err != nil {
		return fmt.Errorf("INVITE: %w", err)
	}
	defer sess.Close()
	ringCtx, cancel := context.WithTimeout(ctx, c.RingTimeout)
	defer cancel()
	if err := sess.WaitAnswer(ringCtx, sipgo.AnswerOptions{Username: c.User, Password: c.Password}); err != nil {
		return fmt.Errorf("not answered: %w", err)
	}
	if err := sess.Ack(ctx); err != nil {
		return fmt.Errorf("ACK: %w", err)
	}
	answer, err := siprec.ParseSDP(sess.InviteResponse.Body())
	if err != nil {
		return fmt.Errorf("bad SDP answer: %w", err)
	}
	dest := &net.UDPAddr{IP: net.ParseIP(answer.ConnectionIP), Port: answer.Streams[0].Port}
	c.logf("answered; talking to %s", dest)

	sender := newSender(rtpConn, dest)
	tick := time.NewTicker(PacketDuration)
	defer tick.Stop()
	packets := (len(c.Audio) + samplesPerPkt - 1) / samplesPerPkt
	for n := 0; n < packets; n++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-hungUp:
			c.logf("the agent hung up")
			return nil
		case <-tick.C:
		}
		sender.send(frame(c.Audio, n))
	}
	c.logf("hanging up")
	byeCtx, cancelBye := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelBye()
	return sess.Bye(byeCtx)
}
