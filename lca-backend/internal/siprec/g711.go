package siprec

// MulawEncode converts one 16-bit linear PCM sample to G.711 μ-law (ITU-T G.711).
func MulawEncode(sample int16) byte {
	const bias = 0x84
	const clip = 32635
	s := int(sample)
	sign := 0
	if s < 0 {
		s = -s
		sign = 0x80
	}
	s = min(s, clip) + bias
	exponent := 7
	for mask := 0x4000; s&mask == 0 && exponent > 0; mask >>= 1 {
		exponent--
	}
	mantissa := (s >> (exponent + 3)) & 0x0F
	return ^byte(sign | exponent<<4 | mantissa)
}

// MulawDecode converts one G.711 μ-law byte back to 16-bit linear PCM.
func MulawDecode(b byte) int16 {
	u := ^b
	sign := u & 0x80
	exponent := int(u>>4) & 0x07
	mantissa := int(u & 0x0F)
	s := ((mantissa << 3) + 0x84) << exponent
	s -= 0x84
	if sign != 0 {
		return int16(-s)
	}
	return int16(s)
}

// MulawSilence is the μ-law byte for a zero sample.
const MulawSilence byte = 0xFF
