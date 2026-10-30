package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"lca-backend/internal/analysis"
	"lca-backend/internal/asr"
)

const (
	// Browser audio chunks are a few KB; this leaves plenty of headroom
	maxAudioChunkBytes = 1 << 20
	keepAliveEvery     = 5 * time.Second
	// How long to wait for Deepgram's last results after the user stops
	finishTimeout = 5 * time.Second
	speakerAuto   = "AUTO"
)

var languagePattern = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z0-9]{2,8})?$`)

// Raw PCM sample rates the browser may send
var allowedSampleRates = map[int]bool{8000: true, 16000: true, 24000: true, 44100: true, 48000: true}

// handleStreamConfig tells the page which speech-to-text engine to use.
func (s *Server) handleStreamConfig(w http.ResponseWriter, r *http.Request) {
	engine := "browser"
	if s.asr != nil {
		engine = "deepgram"
	}
	writeJSON(w, http.StatusOK, map[string]string{"engine": engine})
}

// handleStreamTest serves POST /api/stream/test: transcribes a short mic test
// recording with Deepgram's file API, so the page can compare it with live
// streaming. Nothing is stored.
func (s *Server) handleStreamTest(w http.ResponseWriter, r *http.Request) {
	if s.asr == nil {
		writeError(w, http.StatusServiceUnavailable, "Deepgram isn't configured.")
		return
	}
	var req struct {
		Audio    []byte `json:"audio"` // base64 in JSON
		MimeType string `json:"mimeType"`
		Language string `json:"language"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Audio) == 0 || !strings.HasPrefix(req.MimeType, "audio/") || !languagePattern.MatchString(req.Language) {
		writeError(w, http.StatusBadRequest, "audio, an audio/* mimeType and a language are required.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), finishTimeout*2)
	defer cancel()
	res, err := s.asr.TranscribeFile(ctx, req.Audio, req.MimeType, req.Language)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// The test clip is the user's own speech; redact like everything else
	res.Transcript, _ = analysis.Redact(res.Transcript)
	slog.Info("mic test transcribed", "bytes", len(req.Audio), "mime", req.MimeType,
		"decodedSec", res.Duration, "channels", res.Channels, "chars", len(res.Transcript))
	writeJSON(w, http.StatusOK, res)
}

// wsMessage is sent to the browser: interim text, saved lines, or an error.
type wsMessage struct {
	Type     string                 `json:"type"` // interim | segments | error | closed
	Text     string                 `json:"text,omitempty"`
	Speaker  string                 `json:"speaker,omitempty"`
	Message  string                 `json:"message,omitempty"`
	Segments *streamSegmentResponse `json:"result,omitempty"`
}

// handleStreamAudio serves GET /ws/stream/{id}?lang=&speaker=&offset=&encoding=&sampleRate=
//
// The browser sends binary audio chunks (raw 16-bit PCM when
// encoding=linear16, else a container such as WebM/Opus) and
// text control messages: {"type":"speaker","value":"AGENT|CUSTOMER|AUTO"} or
// {"type":"stop"}. Audio is relayed to Deepgram and never stored. offset is
// the call time in seconds when this connection started, so timestamps stay
// right if the browser reconnects.
func (s *Server) handleStreamAudio(w http.ResponseWriter, r *http.Request) {
	if s.asr == nil {
		writeError(w, http.StatusServiceUnavailable, "Deepgram isn't configured. Set DEEPGRAM_API_KEY in .env and restart the backend.")
		return
	}
	q := r.URL.Query()
	lang := q.Get("lang")
	if !languagePattern.MatchString(lang) {
		writeError(w, http.StatusBadRequest, "lang must be a language code such as en-US.")
		return
	}
	speaker := strings.ToUpper(q.Get("speaker"))
	if speaker == "" {
		speaker = speakerAuto
	}
	if speaker != speakerAuto && speaker != analysis.SpeakerAgent && speaker != analysis.SpeakerCustomer {
		writeError(w, http.StatusBadRequest, "speaker must be AUTO, AGENT or CUSTOMER.")
		return
	}
	offset, err := strconv.ParseFloat(q.Get("offset"), 64)
	if q.Get("offset") == "" {
		offset, err = 0, nil
	}
	if err != nil || offset < 0 || offset > maxStreamSeconds {
		writeError(w, http.StatusBadRequest, "offset is out of range.")
		return
	}
	audio := asr.StreamOptions{Language: lang, Encoding: q.Get("encoding")}
	if audio.Encoding != "" {
		audio.SampleRate, err = strconv.Atoi(q.Get("sampleRate"))
		if audio.Encoding != "linear16" || err != nil || !allowedSampleRates[audio.SampleRate] {
			writeError(w, http.StatusBadRequest, "encoding must be linear16 with a sampleRate of 8000, 16000, 24000, 44100 or 48000.")
			return
		}
	}
	call, ok := s.ownCall(w, r)
	if !ok {
		return
	}
	if call.Status != statusInProgress {
		writeError(w, http.StatusConflict, "This call has ended.")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the error response
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxAudioChunkBytes)

	ctx, cancel := context.WithDeadline(r.Context(), time.UnixMilli(current(r).sess.ExpiresAt))
	defer cancel()
	send := func(m wsMessage) {
		data, _ := json.Marshal(m)
		_ = conn.Write(ctx, websocket.MessageText, data)
	}

	dg, err := s.asr.Open(ctx, audio)
	if err != nil {
		slog.Warn("deepgram open failed", "call", call.ID, "err", err)
		send(wsMessage{Type: "error", Message: err.Error()})
		conn.Close(websocket.StatusInternalError, "speech-to-text unavailable")
		return
	}
	defer dg.Close()

	// Counts only, never content: enough to tell "no audio" from "no speech heard"
	var stats streamStats
	defer func() {
		rmsDB, peakDB := stats.loudness()
		slog.Info("stream ended", "call", call.ID, "lang", lang, "encoding", audio.Encoding, "sampleRate", audio.SampleRate,
			"rmsDBFS", rmsDB, "peakDBFS", peakDB,
			"audioChunks", stats.chunks.Load(), "audioBytes", stats.bytes.Load(),
			"results", stats.results, "withText", stats.withText, "linesSaved", stats.saved)
	}()

	speakerCh := make(chan string, 1)
	speakerCh <- speaker
	stopped := make(chan struct{})
	go s.relayAudio(ctx, conn, dg, speakerCh, stopped, &stats, audio.Encoding == "linear16")

	mode := speaker
	var asm asr.Assembler
	save := func(u *asr.Utterance) {
		if u == nil {
			return
		}
		who := resolveSpeaker(mode, u.Speaker)
		text := u.Text
		if len(text) > maxSegmentChars {
			text = text[:maxSegmentChars]
		}
		res, err := s.saveLine(ctx, call.ID, who, text, offset+u.Start, offset+u.End)
		if err != nil {
			if !errors.Is(err, errCallEnded) {
				slog.Error("save streamed line", "call", call.ID, "err", err)
			}
			send(wsMessage{Type: "error", Message: "A line couldn't be saved: " + err.Error()})
			return
		}
		stats.saved++
		send(wsMessage{Type: "segments", Segments: &res})
	}

	for {
		ev, err := dg.Next(ctx)
		select {
		case mode = <-speakerCh:
		default:
		}
		if err != nil {
			save(asm.Flush())
			if errors.Is(err, io.EOF) {
				send(wsMessage{Type: "closed"})
				conn.Close(websocket.StatusNormalClosure, "")
			} else if ctx.Err() == nil {
				select {
				case <-stopped: // browser went away; nothing to tell it
				default:
					slog.Warn("deepgram stream failed", "call", call.ID, "err", err)
					send(wsMessage{Type: "error", Message: "The speech-to-text connection dropped."})
				}
			}
			return
		}
		switch {
		case ev.UtteranceEnd:
			save(asm.Flush())
		case ev.Transcript != nil:
			stats.results++
			if ev.Transcript.Text != "" {
				stats.withText++
			}
			preview, done := asm.Add(*ev.Transcript)
			save(done)
			if preview != "" {
				send(wsMessage{Type: "interim", Text: preview, Speaker: resolveSpeaker(mode, ev.Transcript.Speaker)})
			}
		}
	}
}

// relayAudio forwards browser audio to Deepgram and handles control
// messages. It closes stopped when the browser disconnects.
type streamStats struct {
	chunks, bytes            atomic.Int64 // written by the relay goroutine
	results, withText, saved int

	// Loudness of raw PCM audio, to tell "too quiet" from "not speech"
	mu         sync.Mutex
	sumSquares float64
	samples    int64
	peak       int
}

// addPCM folds 16-bit little-endian samples into the loudness totals.
func (st *streamStats) addPCM(data []byte) {
	var sum float64
	peak := 0
	for i := 0; i+1 < len(data); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(data[i:])))
		sum += float64(v * v)
		if v < 0 {
			v = -v
		}
		peak = max(peak, v)
	}
	st.mu.Lock()
	st.sumSquares += sum
	st.samples += int64(len(data) / 2)
	st.peak = max(st.peak, peak)
	st.mu.Unlock()
}

// loudness reports RMS and peak in dBFS (0 is full scale; speech is
// typically -30 to -15, below -50 is near silence).
func (st *streamStats) loudness() (rmsDB, peakDB float64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.samples == 0 {
		return math.Inf(-1), math.Inf(-1)
	}
	const fullScale = 32768.0
	rms := math.Sqrt(st.sumSquares/float64(st.samples)) / fullScale
	return math.Round(20*math.Log10(rms)*10) / 10, math.Round(20*math.Log10(float64(st.peak)/fullScale)*10) / 10
}

func (s *Server) relayAudio(ctx context.Context, conn *websocket.Conn, dg *asr.Stream, speakerCh chan string, stopped chan struct{}, stats *streamStats, pcm bool) {
	defer close(stopped)
	keepAlive := time.NewTicker(keepAliveEvery)
	defer keepAlive.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopped:
				return
			case <-keepAlive.C:
				_ = dg.KeepAlive(ctx)
			}
		}
	}()

	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			// Browser left without "stop": finish so the last words are kept
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
			_ = dg.Finish(finishCtx)
			cancel()
			return
		}
		if kind == websocket.MessageBinary {
			stats.chunks.Add(1)
			stats.bytes.Add(int64(len(data)))
			if pcm {
				stats.addPCM(data)
			}
			if err := dg.SendAudio(ctx, data); err != nil {
				return
			}
			continue
		}
		var msg struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "speaker":
			v := strings.ToUpper(msg.Value)
			if v == speakerAuto || v == analysis.SpeakerAgent || v == analysis.SpeakerCustomer {
				select {
				case <-speakerCh: // replace any unread value
				default:
				}
				speakerCh <- v
			}
		case "stop":
			_ = dg.Finish(ctx)
		}
	}
}

// resolveSpeaker picks AGENT or CUSTOMER. In AUTO mode Deepgram's first
// detected voice (speaker 0) is the agent, who usually speaks first.
func resolveSpeaker(mode string, diarized int) string {
	if mode != speakerAuto {
		return mode
	}
	if diarized > 0 {
		return analysis.SpeakerCustomer
	}
	return analysis.SpeakerAgent
}
