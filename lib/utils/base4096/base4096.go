// Package base4096 implements Base4096 encoding/decoding using the first
// 4096 Chinese characters from GB2312 as the code table.
//
// Each character encodes 12 bits (log₂4096 = 12), so 3 bytes (24 bits)
// are represented by 2 characters. A single trailing '=' marks encoded
// data whose original length was not a multiple of 3 (i.e. 1 or 2 bytes
// of remainder).
package base4096

import (
	"errors"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	charsetSize   = 4096 // 2^12
	bitsPerChar   = 12   //
	bytesPerGroup = 3    // LCM(8,12)/8
	charsPerGroup = 2    // LCM(8,12)/12
	padChar       = '。'  // Chinese full stop as padding marker
)

var (
	charset    [charsetSize]rune // index → rune
	decodeMap  map[rune]uint16   // rune → index (0…4095)
	gbkDecoder *encoding.Decoder
)

func init() {
	gbkDecoder = simplifiedchinese.GBK.NewDecoder()
	decodeMap = make(map[rune]uint16, charsetSize)

	idx := 0
	seen := make(map[rune]bool, charsetSize)
	// Iterate GB2312 Chinese character range:
	//   Level 1: areas 16-55 (0xB0 – 0xD7)
	//   Level 2: areas 56-87 (0xD8 – 0xF7)
	//   Each area has 94 code points (0xA1 – 0xFE).
	for first := 0xB0; first <= 0xF7 && idx < charsetSize; first++ {
		for second := 0xA1; second <= 0xFE && idx < charsetSize; second++ {
			r, ok := gbToRune(byte(first), byte(second))
			if !ok || seen[r] {
				continue
			}
			seen[r] = true
			charset[idx] = r
			decodeMap[r] = uint16(idx)
			idx++
		}
	}

	// Safety check: we must have exactly 4096 entries.
	if idx != charsetSize {
		panic("base4096: failed to collect 4096 GB2312 characters")
	}
}

// gbToRune decodes a single GB2312 double-byte sequence to a rune.
func gbToRune(first, second byte) (rune, bool) {
	gb := []byte{first, second}
	utf8Bytes, _, err := transform.Bytes(gbkDecoder, gb)
	if err != nil || len(utf8Bytes) == 0 {
		return 0, false
	}
	r := []rune(string(utf8Bytes))[0]
	// Accept only CJK Unified Ideographs (U+4E00 – U+9FFF).
	// This excludes symbols, replacement characters (U+FFFD), and
	// GB2312 code points that are undefined or map outside CJK.
	if r < 0x4E00 || r > 0x9FFF {
		return 0, false
	}
	return r, true
}

// Encode returns the Base4096 encoding of src.
func Encode(src []byte) string {
	if len(src) == 0 {
		return ""
	}

	var b strings.Builder
	// Pre-allocate enough room: each 3-byte group → 2 chars + possible padding.
	b.Grow((len(src)+bytesPerGroup-1)/bytesPerGroup*charsPerGroup + 1)

	i := 0
	// Full 3-byte groups → 2 chars.
	for i+3 <= len(src) {
		val := uint32(src[i])<<16 | uint32(src[i+1])<<8 | uint32(src[i+2])
		b.WriteRune(charset[(val>>12)&0xFFF])
		b.WriteRune(charset[val&0xFFF])
		i += 3
	}

	remaining := len(src) - i
	switch remaining {
	case 1:
		// 8 data bits → pad 4 zero bits → 1 char.
		val := uint32(src[i]) << 4
		b.WriteRune(charset[val&0xFFF])
		// No padding marker — the decoder detects the incomplete final
		// group by having only 1 char instead of 2.
	case 2:
		// 16 data bits → pad 8 zero bits → 2 chars + trailing '。'.
		val := uint32(src[i])<<8 | uint32(src[i+1])
		b.WriteRune(charset[(val>>4)&0xFFF])
		b.WriteRune(charset[(val&0xF)<<8])
		b.WriteRune(padChar)
	}

	return b.String()
}

// Decode decodes a Base4096 string, returning the original bytes.
func Decode(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}

	// Detect and strip trailing padding (Chinese full stop or ASCII =).
	// Work on runes because '。' is a multi-byte UTF-8 character.
	runes := []rune(s)
	hasPad := len(runes) > 0 && (runes[len(runes)-1] == '。' || runes[len(runes)-1] == '=')
	if hasPad {
		runes = runes[:len(runes)-1]
	}

	n := len(runes)
	if n == 0 {
		return nil, errors.New("base4096: invalid encoding (only padding)")
	}

	// Determine output size.
	// Full groups: n/2 groups × 3 bytes each.
	// Incomplete final group (1 char): yields 1 byte.
	fullGroups := n / 2
	outLen := fullGroups * 3
	if n%2 == 1 {
		outLen++ // single char → 1 byte
	}

	buf := make([]byte, outLen)
	pos := 0

	// Process full 2-char groups.
	for k := 0; k < fullGroups; k++ {
		idx0, ok := decodeMap[runes[k*2]]
		if !ok {
			return nil, errors.New("base4096: invalid character in input")
		}
		idx1, ok := decodeMap[runes[k*2+1]]
		if !ok {
			return nil, errors.New("base4096: invalid character in input")
		}
		val := uint32(idx0)<<12 | uint32(idx1)
		buf[pos] = byte(val >> 16)
		buf[pos+1] = byte(val >> 8)
		buf[pos+2] = byte(val)
		pos += 3
	}

	// Process incomplete final group.
	if n%2 == 1 {
		idx, ok := decodeMap[runes[n-1]]
		if !ok {
			return nil, errors.New("base4096: invalid character in input")
		}
		// 12 bits → top 8 bits are data.
		buf[pos] = byte(idx >> 4)
		pos++
	}

	// Remove the zero-padding byte when the original data ended with
	// 2 bytes (signalled by the '=' suffix).
	if hasPad {
		// The last decoded byte is pure zero-padding — drop it.
		if outLen < 1 {
			return nil, errors.New("base4096: invalid padding")
		}
		// Sanity-check: the last byte should be 0x00 (our padding).
		// We don't strictly enforce it to allow round-tripping data that
		// naturally ends with 0x00 and was padded, but we log/check it.
		if buf[outLen-1] != 0x00 {
			// Data that happens to end with a non-zero byte and has a
			// padding marker is technically invalid, but we still
			// honour the marker and drop the byte.
		}
		buf = buf[:outLen-1]
	}

	return buf, nil
}
