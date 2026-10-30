package srctest

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"lca-backend/internal/siprec"
)

// ReadWAV loads a 16-bit PCM WAV file as 8 kHz mono samples. Stereo is mixed
// down; higher sample rates that are a multiple of 8 kHz are averaged down.
func ReadWAV(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	var channels, bits int
	var rate int
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		body := data[off+8 : min(len(data), off+8+size)]
		switch id {
		case "fmt ":
			if len(body) < 16 || binary.LittleEndian.Uint16(body[0:]) != 1 {
				return nil, errors.New("WAV must be uncompressed PCM")
			}
			channels = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			pcm = body
		}
		off += 8 + size + size%2
	}
	if bits != 16 || channels < 1 || pcm == nil {
		return nil, errors.New("WAV must be 16-bit PCM with a data chunk")
	}
	if rate%siprec.ClockRate != 0 {
		return nil, fmt.Errorf("sample rate %d Hz isn't a multiple of 8000 Hz", rate)
	}
	step := rate / siprec.ClockRate
	frameBytes := 2 * channels
	frames := len(pcm) / frameBytes
	out := make([]int16, 0, frames/step)
	for f := 0; f+step <= frames; f += step {
		var sum int
		for k := 0; k < step; k++ {
			for ch := 0; ch < channels; ch++ {
				sum += int(int16(binary.LittleEndian.Uint16(pcm[(f+k)*frameBytes+2*ch:])))
			}
		}
		out = append(out, int16(sum/(step*channels)))
	}
	return out, nil
}
