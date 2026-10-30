// Package srs is a SIPREC Session Recording Server. A recording client (an
// SBC or PBX, the "SRC") sends it a copy of each call: one RTP audio stream
// per participant plus metadata saying who is who. Each stream is transcribed
// by Deepgram separately, so every line is labelled with the right speaker,
// and lines are posted to the dashboard backend as they are spoken.
package srs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/pion/rtp"

	"lca-backend/internal/analysis"
	"lca-backend/internal/asr"
	"lca-backend/internal/siprec"
)

const (
	// Audio is batched into frames of about this length before going to Deepgram
	sendEveryBytes = siprec.ClockRate / 10 // 100 ms of 8 kHz G.711
	readTimeout    = time.Second
	// How long to wait for Deepgram's last words after the call ends
	finishTimeout = 6 * time.Second
	// Longest call recorded; matches the backend's limit
	maxCallDuration = 4 * time.Hour
	maxRTPPacket    = 1500
)

type Config struct {
	MediaIP    string // address put in SDP answers, reachable by the SRC
	RTPPortMin int
	RTPPortMax int
	Language   string // Deepgram language, e.g. en-US
	// End a call when no audio arrives for this long (SRC died without BYE)
	RTPTimeout time.Duration
}

type Server struct {
	cfg     Config
	asr     *asr.Client
	backend *Backend
	ports   *portPool

	ua      *sipgo.UserAgent
	sip     *sipgo.Server
	dialogs *sipgo.DialogServerCache
	wg      sync.WaitGroup // running calls
}

func New(cfg Config, speech *asr.Client, backend *Backend) (*Server, error) {
	if cfg.RTPPortMin <= 0 || cfg.RTPPortMax < cfg.RTPPortMin {
		return nil, errors.New("invalid RTP port range")
	}
	return &Server{
		cfg: cfg, asr: speech, backend: backend,
		ports: newPortPool(cfg.MediaIP, cfg.RTPPortMin, cfg.RTPPortMax),
	}, nil
}

// Serve answers SIP on udp and tcp (same port; either may be nil) until ctx
// ends, then waits for running calls. SIPREC INVITEs carry SDP plus metadata
// and usually exceed the UDP size limit, so SRCs normally use TCP.
func (s *Server) Serve(ctx context.Context, udp net.PacketConn, tcp net.Listener) error {
	if udp == nil && tcp == nil {
		return errors.New("no SIP listener")
	}
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("lca-srs"))
	if err != nil {
		return err
	}
	defer ua.Close()
	srv, err := sipgo.NewServer(ua)
	if err != nil {
		return err
	}
	cli, err := sipgo.NewClient(ua, sipgo.WithClientHostname(s.cfg.MediaIP))
	if err != nil {
		return err
	}
	var port int
	if udp != nil {
		port = udp.LocalAddr().(*net.UDPAddr).Port
	} else {
		port = tcp.Addr().(*net.TCPAddr).Port
	}
	s.ua, s.sip = ua, srv
	s.dialogs = sipgo.NewDialogServerCache(cli, sip.ContactHeader{
		Address: sip.Uri{User: "srs", Host: s.cfg.MediaIP, Port: port},
	})

	srv.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) { s.handleInvite(ctx, req, tx) })
	srv.OnAck(func(req *sip.Request, tx sip.ServerTransaction) { _ = s.dialogs.ReadAck(req, tx) })
	srv.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		if err := s.dialogs.ReadBye(req, tx); err != nil {
			_ = tx.Respond(sip.NewResponseFromRequest(req, 481, "Call/Transaction Does Not Exist", nil))
		}
	})
	srv.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	})

	errs := make(chan error, 2)
	listeners := 0
	if udp != nil {
		listeners++
		go func() { errs <- srv.ServeUDP(udp) }()
	}
	if tcp != nil {
		listeners++
		go func() { errs <- srv.ServeTCP(tcp) }()
	}
	slog.Info("SIPREC recorder listening", "sipPort", port, "udp", udp != nil, "tcp", tcp != nil,
		"media", s.cfg.MediaIP, "rtpPorts", fmt.Sprintf("%d-%d", s.cfg.RTPPortMin, s.cfg.RTPPortMax))

	select {
	case <-ctx.Done():
		err = nil
	case err = <-errs:
		listeners--
	}
	if udp != nil {
		udp.Close()
	}
	if tcp != nil {
		tcp.Close()
	}
	for ; listeners > 0; listeners-- {
		<-errs
	}
	s.wg.Wait()
	return err
}

func reject(dlg *sipgo.DialogServerSession, code int, reason string) {
	slog.Warn("rejecting recording session", "code", code, "reason", reason)
	_ = dlg.Respond(code, reason, nil)
}

func (s *Server) handleInvite(ctx context.Context, req *sip.Request, tx sip.ServerTransaction) {
	dlg, err := s.dialogs.ReadInvite(req, tx)
	if err != nil {
		_ = tx.Respond(sip.NewResponseFromRequest(req, 400, "Bad Request", nil))
		return
	}
	_ = dlg.Respond(100, "Trying", nil)

	contentType := ""
	if h := req.GetHeader("Content-Type"); h != nil {
		contentType = h.Value()
	}
	sdpBody, meta, err := siprec.SplitBody(contentType, req.Body())
	if err != nil {
		reject(dlg, 400, "Bad SIPREC body: "+err.Error())
		return
	}
	offer, err := siprec.ParseSDP(sdpBody)
	if err != nil {
		reject(dlg, 488, "Not Acceptable Here: "+err.Error())
		return
	}

	agents, err := s.backend.Agents(ctx)
	if err != nil {
		reject(dlg, 503, "Service Unavailable: backend")
		slog.Error("list agents", "err", err)
		return
	}
	roles := assignSpeakers(offer, meta, agents)

	// One receive socket per audio stream
	var streams []*mediaStream
	var answer []siprec.AnswerStream
	release := func() {
		for _, m := range streams {
			m.conn.Close()
		}
	}
	for i, ms := range offer.Streams {
		pt, enc, ok := siprec.ChooseCodec(ms)
		if !ok {
			release()
			reject(dlg, 488, "Not Acceptable Here: only G.711 (PCMU/PCMA) is supported")
			return
		}
		conn, port, err := s.ports.listen()
		if err != nil {
			release()
			reject(dlg, 503, "Service Unavailable: no free RTP ports")
			return
		}
		label := streamLabel(ms, i)
		streams = append(streams, &mediaStream{label: label, speaker: roles.speakers[label], payload: uint8(pt), encoding: enc, conn: conn, ports: s.ports, port: port})
		answer = append(answer, siprec.AnswerStream{Label: label, Port: port, Payload: pt})
	}

	callID, err := s.backend.CreateCall(ctx, roles.agent, roles.caller)
	if err != nil {
		release()
		reject(dlg, 503, "Service Unavailable: backend")
		slog.Error("create call", "err", err)
		return
	}
	if err := dlg.Respond(200, "OK", siprec.BuildAnswer(s.cfg.MediaIP, answer), sip.NewHeader("Content-Type", siprec.ContentTypeSDP)); err != nil {
		release()
		_ = s.backend.EndCall(context.WithoutCancel(ctx), callID, 0)
		slog.Error("answer recording session", "err", err)
		return
	}

	slog.Info("recording started", "call", callID, "agent", roles.agent, "caller", roles.caller, "streams", describe(streams))
	sess := &session{srv: s, callID: callID, dialog: dlg, streams: streams, started: time.Now()}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		sess.run(ctx)
	}()
}

func streamLabel(ms siprec.MediaStream, i int) string {
	if ms.Label != "" {
		return ms.Label
	}
	return strconv.Itoa(i + 1)
}

func describe(streams []*mediaStream) string {
	parts := make([]string, len(streams))
	for i, m := range streams {
		parts[i] = fmt.Sprintf("%s=%s(%s,:%d)", m.label, m.speaker, m.encoding, m.port)
	}
	return strings.Join(parts, " ")
}

// ── Who is the agent? ───────────────────────────────────────

type roles struct {
	speakers map[string]string // stream label → AGENT or CUSTOMER
	agent    string            // agent ID for the call
	caller   string            // customer's number or SIP user
}

// assignSpeakers maps each stream to AGENT or CUSTOMER. A participant whose
// SIP user matches a known agent ID is the agent; everyone else is a
// customer. Without a match, the usual SRC convention applies: the first
// participant (the caller) is the customer and the second is the agent.
func assignSpeakers(offer siprec.SDP, meta *siprec.Metadata, agents []string) roles {
	r := roles{speakers: map[string]string{}}
	known := map[string]string{}
	for _, a := range agents {
		known[strings.ToLower(a)] = a
	}

	var parts []siprec.Participant
	if meta != nil {
		for _, p := range meta.Participants() {
			if p.Label != "" {
				parts = append(parts, p)
			}
		}
	}
	agentIdx := -1
	for i, p := range parts {
		if a, ok := known[strings.ToLower(siprec.User(p.AOR))]; ok {
			agentIdx, r.agent = i, a
			break
		}
	}
	if agentIdx < 0 && len(parts) >= 2 {
		agentIdx, r.agent = 1, siprec.User(parts[1].AOR)
		slog.Warn("no participant matches an agent ID; treating the second participant as the agent", "aor", parts[1].AOR)
	}
	for i, p := range parts {
		if i == agentIdx {
			r.speakers[p.Label] = analysis.SpeakerAgent
			continue
		}
		r.speakers[p.Label] = analysis.SpeakerCustomer
		if r.caller == "" {
			r.caller = siprec.User(p.AOR)
		}
	}

	// Streams the metadata didn't describe: caller first, agent second
	for i, ms := range offer.Streams {
		label := streamLabel(ms, i)
		if _, ok := r.speakers[label]; ok {
			continue
		}
		if i == 1 && agentIdx < 0 {
			r.speakers[label] = analysis.SpeakerAgent
		} else {
			r.speakers[label] = analysis.SpeakerCustomer
		}
	}
	return r
}

// ── A recorded call ─────────────────────────────────────────

type mediaStream struct {
	label, speaker, encoding string
	payload                  uint8
	conn                     net.PacketConn
	ports                    *portPool
	port                     int
	lastPacket               atomic.Int64 // unix nanoseconds
}

type session struct {
	srv     *Server
	callID  string
	dialog  *sipgo.DialogServerSession
	streams []*mediaStream
	started time.Time
	lineMu  sync.Mutex // lines are posted one at a time so analysis stays consistent
}

func (c *session) elapsed() float64 { return time.Since(c.started).Seconds() }

// run transcribes every stream until the SRC hangs up, audio stops, or the
// server shuts down, then ends the call in the backend.
func (c *session) run(ctx context.Context) {
	// Deepgram connections outlive the dialog so the last words still arrive
	speechCtx, cancelSpeech := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelSpeech()

	now := time.Now().UnixNano()
	var wg sync.WaitGroup
	stops := make([]func(), 0, len(c.streams))
	for _, m := range c.streams {
		m.lastPacket.Store(now)
		dg, err := c.srv.asr.Open(speechCtx, asr.StreamOptions{Language: c.srv.cfg.Language, Encoding: m.encoding, SampleRate: siprec.ClockRate})
		if err != nil {
			slog.Error("deepgram unavailable; stream not transcribed", "call", c.callID, "label", m.label, "err", err)
			m.conn.Close()
			continue
		}
		wg.Add(2)
		go func() { defer wg.Done(); c.receive(speechCtx, m, dg) }()
		go func() { defer wg.Done(); c.transcribe(speechCtx, m, dg) }()
		stops = append(stops, func() {
			m.conn.Close() // ends receive, which finishes the Deepgram stream
		})
	}

	reason := c.waitForEnd(ctx)
	slog.Info("recording stopping", "call", c.callID, "reason", reason)
	for _, stop := range stops {
		stop()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(finishTimeout):
		slog.Warn("deepgram did not finish in time", "call", c.callID)
		cancelSpeech()
		<-done
	}
	for _, m := range c.streams {
		m.ports.release(m.port)
	}
	if reason != "bye" {
		byeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = c.dialog.Bye(byeCtx)
		cancel()
	}
	endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := c.srv.backend.EndCall(endCtx, c.callID, c.elapsed()); err != nil {
		slog.Error("end call", "call", c.callID, "err", err)
	}
	c.dialog.Close()
	slog.Info("recording ended", "call", c.callID, "seconds", int(c.elapsed()))
}

func (c *session) waitForEnd(ctx context.Context) string {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	limit := time.NewTimer(maxCallDuration)
	defer limit.Stop()
	for {
		select {
		case <-c.dialog.Context().Done():
			return "bye"
		case <-ctx.Done():
			return "shutdown"
		case <-limit.C:
			return "max duration"
		case <-tick.C:
			if c.srv.cfg.RTPTimeout > 0 && c.silentFor() > c.srv.cfg.RTPTimeout {
				return "no audio"
			}
		}
	}
}

// silentFor is how long since any stream last received audio.
func (c *session) silentFor() time.Duration {
	var latest int64
	for _, m := range c.streams {
		latest = max(latest, m.lastPacket.Load())
	}
	return time.Since(time.Unix(0, latest))
}

// receive reads RTP from the stream's socket and forwards the G.711 payload
// to Deepgram. Closing the socket ends it and finishes the Deepgram stream.
func (c *session) receive(ctx context.Context, m *mediaStream, dg *asr.Stream) {
	defer func() {
		finishCtx, cancel := context.WithTimeout(ctx, finishTimeout)
		_ = dg.Finish(finishCtx)
		cancel()
	}()
	buf := make([]byte, maxRTPPacket)
	batch := make([]byte, 0, sendEveryBytes*2)
	var pkt rtp.Packet
	for {
		_ = m.conn.SetReadDeadline(time.Now().Add(readTimeout))
		n, _, err := m.conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			if len(batch) > 0 {
				_ = dg.SendAudio(ctx, batch)
			}
			return
		}
		if pkt.Unmarshal(buf[:n]) != nil || pkt.PayloadType != m.payload {
			continue // not RTP, or comfort noise / DTMF events
		}
		m.lastPacket.Store(time.Now().UnixNano())
		batch = append(batch, pkt.Payload...)
		if len(batch) >= sendEveryBytes {
			if err := dg.SendAudio(ctx, batch); err != nil {
				slog.Warn("send audio to deepgram", "call", c.callID, "label", m.label, "err", err)
				return
			}
			batch = batch[:0]
		}
	}
}

// transcribe turns Deepgram results into lines for this stream's speaker.
func (c *session) transcribe(ctx context.Context, m *mediaStream, dg *asr.Stream) {
	defer dg.Close()
	var asm asr.Assembler
	save := func(u *asr.Utterance) {
		if u == nil {
			return
		}
		c.lineMu.Lock()
		defer c.lineMu.Unlock()
		if err := c.srv.backend.AddLine(ctx, c.callID, m.speaker, u.Text, u.Start, u.End); err != nil {
			slog.Error("save line", "call", c.callID, "speaker", m.speaker, "err", err)
		}
	}
	for {
		ev, err := dg.Next(ctx)
		if err != nil {
			save(asm.Flush())
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				slog.Warn("deepgram stream failed", "call", c.callID, "label", m.label, "err", err)
			}
			return
		}
		switch {
		case ev.UtteranceEnd:
			save(asm.Flush())
		case ev.Transcript != nil:
			_, done := asm.Add(*ev.Transcript)
			save(done)
		}
	}
}

// ── RTP ports ───────────────────────────────────────────────

// portPool hands out even-numbered UDP ports (RTP convention) in a range.
type portPool struct {
	host     string
	min, max int
	mu       sync.Mutex
	inUse    map[int]bool
	next     int
}

func newPortPool(host string, min, max int) *portPool {
	if min%2 == 1 {
		min++
	}
	return &portPool{host: host, min: min, max: max, inUse: map[int]bool{}, next: min}
}

func (p *portPool) listen() (net.PacketConn, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for tries := 0; tries <= (p.max-p.min)/2; tries++ {
		port := p.next
		p.next += 2
		if p.next > p.max {
			p.next = p.min
		}
		if p.inUse[port] {
			continue
		}
		conn, err := net.ListenPacket("udp", net.JoinHostPort(p.host, strconv.Itoa(port)))
		if err != nil {
			continue // taken by another program
		}
		p.inUse[port] = true
		return conn, port, nil
	}
	return nil, 0, errors.New("no free RTP ports")
}

func (p *portPool) release(port int) {
	p.mu.Lock()
	delete(p.inUse, port)
	p.mu.Unlock()
}
