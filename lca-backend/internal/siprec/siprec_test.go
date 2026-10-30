package siprec

import (
	"slices"
	"testing"
)

func TestBodyRoundTrip(t *testing.T) {
	offer := BuildOffer("10.0.0.5", []AnswerStream{{Label: "1", Port: 4000, Payload: PayloadPCMU}, {Label: "2", Port: 4002, Payload: PayloadPCMA}})
	meta := BuildMetadata("sess-1", []Participant{
		{AOR: "tel:+17035550100", Name: "Caller", Label: "1"},
		{AOR: "sip:BobS@pbx.example", Name: "Bob & co", Label: "2"},
	})
	body, ct, err := BuildBody(offer, meta)
	if err != nil {
		t.Fatal(err)
	}
	sdpBytes, m, err := SplitBody(ct, body)
	if err != nil || m == nil {
		t.Fatalf("split: %v %v", m, err)
	}
	sdp, err := ParseSDP(sdpBytes)
	if err != nil {
		t.Fatal(err)
	}
	if sdp.ConnectionIP != "10.0.0.5" || len(sdp.Streams) != 2 || sdp.Streams[1].Port != 4002 || sdp.Streams[1].Label != "2" {
		t.Errorf("sdp = %+v", sdp)
	}
	if pt, enc, ok := ChooseCodec(sdp.Streams[1]); !ok || pt != PayloadPCMA || enc != "alaw" {
		t.Errorf("codec = %d %s %v", pt, enc, ok)
	}
	parts := m.Participants()
	if len(parts) != 2 || parts[1].AOR != "sip:BobS@pbx.example" || parts[1].Name != "Bob & co" || parts[1].Label != "2" {
		t.Errorf("participants = %+v", parts)
	}
}

// A body from a real-world style SRC: participant/stream IDs that aren't P1/S1
func TestMetadataFromOtherSRC(t *testing.T) {
	xmlDoc := `<?xml version="1.0" encoding="UTF-8"?>
<recording xmlns='urn:ietf:params:xml:ns:recording:1'>
  <datamode>complete</datamode>
  <session session_id="hVpd7YQgRW2nD22h7q60JQ=="><sipSessionID>ab30317f1a784dc48ff824d0d3715d86</sipSessionID></session>
  <participant participant_id="srfBElmCRp2QB23b7Mpk0w=="><nameID aor="sip:+15551234@carrier.example"/></participant>
  <participant participant_id="zSfPoSvdSDCmU3A3TRDxAw=="><nameID aor="sip:agent7@cc.example"><name>Agent Seven</name></nameID></participant>
  <stream stream_id="UAAMm5GRQKSCMVvLyl4rFw==" session_id="hVpd7YQgRW2nD22h7q60JQ=="><label>96</label></stream>
  <stream stream_id="i1Pz3to5hGk8fuXl+PbwCw==" session_id="hVpd7YQgRW2nD22h7q60JQ=="><label>97</label></stream>
  <participantstreamassoc participant_id="srfBElmCRp2QB23b7Mpk0w=="><send>UAAMm5GRQKSCMVvLyl4rFw==</send><recv>i1Pz3to5hGk8fuXl+PbwCw==</recv></participantstreamassoc>
  <participantstreamassoc participant_id="zSfPoSvdSDCmU3A3TRDxAw=="><send>i1Pz3to5hGk8fuXl+PbwCw==</send><recv>UAAMm5GRQKSCMVvLyl4rFw==</recv></participantstreamassoc>
</recording>`
	body := "--b\r\nContent-Type: application/sdp\r\n\r\nv=0\r\nc=IN IP4 192.0.2.1\r\nm=audio 10000 RTP/AVP 0 101\r\na=label:96\r\nm=audio 10002 RTP/AVP 0 101\r\na=label:97\r\n\r\n" +
		"--b\r\nContent-Type: application/rs-metadata+xml\r\nContent-Disposition: recording-session\r\n\r\n" + xmlDoc + "\r\n--b--\r\n"
	_, m, err := SplitBody(`multipart/mixed;boundary="b"`, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	got := m.Participants()
	if len(got) != 2 || got[0].Label != "96" || got[1].Label != "97" || User(got[1].AOR) != "agent7" || got[1].Name != "Agent Seven" {
		t.Errorf("participants = %+v", got)
	}
}

func TestParseSDPErrors(t *testing.T) {
	if _, err := ParseSDP([]byte("v=0\r\nc=IN IP4 1.2.3.4\r\nm=video 5000 RTP/AVP 96\r\n")); err == nil {
		t.Error("video-only SDP accepted")
	}
	if _, err := ParseSDP([]byte("v=0\r\nm=audio 5000 RTP/AVP 0\r\n")); err == nil {
		t.Error("SDP without address accepted")
	}
	if _, _, ok := ChooseCodec(MediaStream{Payloads: []int{9, 111}}); ok {
		t.Error("non-G.711 codec accepted")
	}
}

func TestUser(t *testing.T) {
	for in, want := range map[string]string{
		"sip:BobS@pbx.example": "BobS", "tel:+17035550100": "+17035550100",
		"sips:1001@x;transport=tls": "1001", "sip:alice;tag=1": "alice",
	} {
		if got := User(in); got != want {
			t.Errorf("User(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMulawRoundTrip(t *testing.T) {
	if MulawEncode(0) != MulawSilence {
		t.Errorf("silence = %#x", MulawEncode(0))
	}
	for _, s := range []int16{1, 100, -100, 1000, -1000, 12000, -32768, 32767} {
		back := MulawDecode(MulawEncode(s))
		diff := int(back) - int(s)
		// μ-law keeps about 3% precision
		if diff < 0 {
			diff = -diff
		}
		if limit := max(8, abs(int(s))/16); diff > limit {
			t.Errorf("%d → %d (off by %d)", s, back, diff)
		}
	}
	if !slices.Equal([]byte{MulawEncode(32767), MulawEncode(-32768)}, []byte{0x80, 0x00}) {
		t.Errorf("full scale = %#x %#x", MulawEncode(32767), MulawEncode(-32768))
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
