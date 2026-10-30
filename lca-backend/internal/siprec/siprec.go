// Package siprec reads and writes the SIP Recording (SIPREC, RFC 7866) parts
// of a recording session: the multipart INVITE body, its SDP offer with one
// labelled audio stream per call participant, and the rs-metadata XML that
// says which participant sends which stream.
package siprec

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
)

const (
	ContentTypeSDP      = "application/sdp"
	ContentTypeMetadata = "application/rs-metadata+xml"

	// Static RTP payload types for G.711 (RFC 3551)
	PayloadPCMU = 0
	PayloadPCMA = 8
	ClockRate   = 8000
)

// Codec names as Deepgram expects them in its encoding parameter
var codecByPayload = map[int]string{PayloadPCMU: "mulaw", PayloadPCMA: "alaw"}

// MediaStream is one audio m-line of an SDP.
type MediaStream struct {
	Label    string // a=label, links the stream to the metadata
	Port     int
	Payloads []int // offered payload types, in preference order
}

// SDP holds the parts of a session description the recorder needs.
type SDP struct {
	ConnectionIP string
	Streams      []MediaStream
}

// ParseSDP reads the connection address and audio m-lines.
func ParseSDP(body []byte) (SDP, error) {
	var sdp SDP
	var cur *MediaStream
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "c=IN IP4 "), strings.HasPrefix(line, "c=IN IP6 "):
			ip := strings.Fields(line[len("c=IN IP4 "):])
			if len(ip) > 0 && (cur == nil || sdp.ConnectionIP == "") {
				sdp.ConnectionIP = strings.Split(ip[0], "/")[0]
			}
		case strings.HasPrefix(line, "m="):
			f := strings.Fields(line[2:])
			if len(f) < 4 || f[0] != "audio" {
				cur = nil
				continue
			}
			port, err := strconv.Atoi(f[1])
			if err != nil {
				return sdp, fmt.Errorf("bad m-line port %q", f[1])
			}
			ms := MediaStream{Port: port}
			for _, p := range f[3:] {
				if pt, err := strconv.Atoi(p); err == nil {
					ms.Payloads = append(ms.Payloads, pt)
				}
			}
			sdp.Streams = append(sdp.Streams, ms)
			cur = &sdp.Streams[len(sdp.Streams)-1]
		case strings.HasPrefix(line, "a=label:") && cur != nil:
			cur.Label = strings.TrimPrefix(line, "a=label:")
		}
	}
	if len(sdp.Streams) == 0 {
		return sdp, errors.New("SDP has no audio streams")
	}
	if sdp.ConnectionIP == "" {
		return sdp, errors.New("SDP has no connection address")
	}
	return sdp, nil
}

// ChooseCodec picks the first offered G.711 payload type.
func ChooseCodec(ms MediaStream) (payload int, encoding string, ok bool) {
	for _, pt := range ms.Payloads {
		if enc, ok := codecByPayload[pt]; ok {
			return pt, enc, true
		}
	}
	return 0, "", false
}

// AnswerStream is one accepted stream in the recorder's SDP answer.
type AnswerStream struct {
	Label   string
	Port    int
	Payload int
}

// BuildAnswer writes the recorder's SDP answer: every stream receive-only.
func BuildAnswer(ip string, streams []AnswerStream) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "v=0\r\no=lca-srs 1 1 IN IP4 %s\r\ns=SIPREC\r\nc=IN IP4 %s\r\nt=0 0\r\n", ip, ip)
	for _, s := range streams {
		fmt.Fprintf(&b, "m=audio %d RTP/AVP %d\r\n", s.Port, s.Payload)
		fmt.Fprintf(&b, "a=rtpmap:%d %s/%d\r\n", s.Payload, rtpName(s.Payload), ClockRate)
		fmt.Fprintf(&b, "a=label:%s\r\na=recvonly\r\n", s.Label)
	}
	return []byte(b.String())
}

func rtpName(pt int) string {
	if pt == PayloadPCMA {
		return "PCMA"
	}
	return "PCMU"
}

// BuildOffer writes an SRC's SDP offer: one send-only G.711 stream per label.
func BuildOffer(ip string, streams []AnswerStream) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "v=0\r\no=lca-src 1 1 IN IP4 %s\r\ns=SIPREC\r\nc=IN IP4 %s\r\nt=0 0\r\n", ip, ip)
	for _, s := range streams {
		fmt.Fprintf(&b, "m=audio %d RTP/AVP %d\r\n", s.Port, s.Payload)
		fmt.Fprintf(&b, "a=rtpmap:%d %s/%d\r\n", s.Payload, rtpName(s.Payload), ClockRate)
		fmt.Fprintf(&b, "a=label:%s\r\na=sendonly\r\n", s.Label)
	}
	return []byte(b.String())
}

// ── rs-metadata (RFC 7865) ──────────────────────────────────

type Metadata struct {
	XMLName xml.Name `xml:"recording"`
	Parties []struct {
		ID     string `xml:"participant_id,attr"`
		NameID struct {
			AOR  string `xml:"aor,attr"`
			Name string `xml:"name"`
		} `xml:"nameID"`
	} `xml:"participant"`
	Streams []struct {
		ID    string `xml:"stream_id,attr"`
		Label string `xml:"label"`
	} `xml:"stream"`
	Assocs []struct {
		ParticipantID string   `xml:"participant_id,attr"`
		Send          []string `xml:"send"`
	} `xml:"participantstreamassoc"`
}

// Participant is a call party and the stream label carrying their voice.
type Participant struct {
	AOR   string // e.g. sip:BobS@pbx.example or tel:+17035550100
	Name  string
	Label string // "" if the metadata doesn't link them to a stream
}

// Participants resolves each participant's sent stream label.
func (m Metadata) Participants() []Participant {
	labelByStream := map[string]string{}
	for _, s := range m.Streams {
		labelByStream[s.ID] = strings.TrimSpace(s.Label)
	}
	labelByParticipant := map[string]string{}
	for _, a := range m.Assocs {
		for _, sid := range a.Send {
			if l, ok := labelByStream[strings.TrimSpace(sid)]; ok {
				labelByParticipant[a.ParticipantID] = l
				break
			}
		}
	}
	out := make([]Participant, 0, len(m.Parties))
	for _, p := range m.Parties {
		out = append(out, Participant{AOR: p.NameID.AOR, Name: strings.TrimSpace(p.NameID.Name), Label: labelByParticipant[p.ID]})
	}
	return out
}

// User returns the user part of an AOR: "sip:BobS@pbx" → "BobS", "tel:+1703…" → "+1703…".
func User(aor string) string {
	u := aor
	if i := strings.IndexByte(u, ':'); i >= 0 {
		u = u[i+1:]
	}
	if i := strings.IndexByte(u, '@'); i >= 0 {
		u = u[:i]
	}
	if i := strings.IndexByte(u, ';'); i >= 0 {
		u = u[:i]
	}
	return u
}

// ── Multipart body ──────────────────────────────────────────

// SplitBody returns the SDP and rs-metadata parts of a SIPREC INVITE body.
// A plain SDP body (no metadata) is accepted too.
func SplitBody(contentType string, body []byte) (sdp []byte, meta *Metadata, err error) {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, nil, fmt.Errorf("bad Content-Type: %w", err)
	}
	if mt == ContentTypeSDP {
		return body, nil, nil
	}
	if !strings.HasPrefix(mt, "multipart/") {
		return nil, nil, fmt.Errorf("unsupported Content-Type %q", mt)
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("bad multipart body: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(part, 1<<20))
		if err != nil {
			return nil, nil, err
		}
		pt, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		switch pt {
		case ContentTypeSDP:
			sdp = data
		case ContentTypeMetadata:
			var m Metadata
			if err := xml.Unmarshal(data, &m); err != nil {
				return nil, nil, fmt.Errorf("bad rs-metadata: %w", err)
			}
			meta = &m
		}
	}
	if sdp == nil {
		return nil, nil, errors.New("multipart body has no SDP")
	}
	return sdp, meta, nil
}

// BuildBody writes a SIPREC multipart body and returns it with its Content-Type.
func BuildBody(sdp, metadata []byte) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range []struct {
		ct   string
		data []byte
	}{{ContentTypeSDP, sdp}, {ContentTypeMetadata, metadata}} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", p.ct)
		if p.ct == ContentTypeMetadata {
			h.Set("Content-Disposition", "recording-session")
		}
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(p.data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "multipart/mixed;boundary=" + w.Boundary(), nil
}

// BuildMetadata writes rs-metadata linking each participant to their stream label.
func BuildMetadata(sessionID string, parts []Participant) []byte {
	var b strings.Builder
	esc := func(s string) string {
		var out bytes.Buffer
		_ = xml.EscapeText(&out, []byte(s))
		return out.String()
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<recording xmlns="urn:ietf:params:xml:ns:recording:1">` + "\n")
	b.WriteString("<datamode>complete</datamode>\n")
	fmt.Fprintf(&b, `<session session_id="%s"></session>`+"\n", esc(sessionID))
	for i, p := range parts {
		fmt.Fprintf(&b, `<participant participant_id="P%d"><nameID aor="%s"><name>%s</name></nameID></participant>`+"\n", i+1, esc(p.AOR), esc(p.Name))
	}
	for i, p := range parts {
		fmt.Fprintf(&b, `<stream stream_id="S%d" session_id="%s"><label>%s</label></stream>`+"\n", i+1, esc(sessionID), esc(p.Label))
	}
	for i := range parts {
		fmt.Fprintf(&b, `<participantstreamassoc participant_id="P%d"><send>S%d</send></participantstreamassoc>`+"\n", i+1, i+1)
	}
	b.WriteString("</recording>\n")
	return []byte(b.String())
}
