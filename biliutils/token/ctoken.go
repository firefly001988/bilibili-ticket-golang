package token

import (
	"encoding/base64"
	"math/rand/v2"
	"time"
)

// CTokenGenerator generates tokens for hot projects (热门项目).
// It simulates browser window statistics encoded in binary format.
type CTokenGenerator struct {
	begin          time.Time
	generateCounts int
}

// NewCTokenGenerator creates a new CTokenGenerator.
func NewCTokenGenerator() *CTokenGenerator {
	return &CTokenGenerator{
		begin: time.Now().Add(-2 * time.Second),
	}
}

type windowStats struct {
	TouchCount   uint16
	VisibleCount uint16
	UnloadCount  uint16
	StaySeconds  uint16
	SinceInitSec uint16
	ScrollX      uint16
	ScrollY      uint16
	InnerWidth   uint16
	InnerHeight  uint16
	OuterWidth   uint16
	OuterHeight  uint16
	ScreenX      uint16
	ScreenY      uint16
	ScreenWidth  uint16
	ScreenHeight uint16
	AvailWidth   uint16
}

// makeToken encodes window statistics into a base64 token string.
// It mimics JavaScript's DataView + Uint16Array pattern:
//   - 16 bytes are first filled according to the field map
//   - Then each byte is expanded to 2 bytes (little-endian Uint16)
//   - The resulting 32-byte array is base64-encoded
func makeToken(stats *windowStats) string {
	buf := make([]byte, 16)

	// Field positions in the 16-byte buffer, with their data source and byte length
	fieldMap := map[int]struct {
		data   uint16
		length int
	}{
		0:  {stats.TouchCount, 1},
		1:  {stats.ScrollX, 1},
		2:  {stats.VisibleCount, 1},
		3:  {stats.ScrollY, 1},
		4:  {stats.InnerWidth, 1},
		5:  {stats.UnloadCount, 1},
		6:  {stats.InnerHeight, 1},
		7:  {stats.OuterWidth, 1},
		8:  {stats.StaySeconds, 2},
		10: {stats.SinceInitSec, 2},
		12: {stats.OuterHeight, 1},
		13: {stats.ScreenX, 1},
		14: {stats.ScreenY, 1},
		15: {stats.ScreenWidth, 1},
	}

	for offset := 0; offset < 16; offset++ {
		if field, ok := fieldMap[offset]; ok {
			if field.length == 1 {
				// Single byte field: clamp to 255
				if field.data > 255 {
					buf[offset] = 255
				} else {
					buf[offset] = byte(field.data)
				}
			} else {
				// Two-byte field (Uint16 little-endian): clamp to 65535
				val := field.data
				if val > 65535 {
					val = 65535
				}
				buf[offset] = byte(val & 0xFF)
				buf[offset+1] = byte((val >> 8) & 0xFF)
				offset++ // skip the next byte since it's been filled
			}
		} else {
			// Unmapped positions: use ScreenHeight-dependent logic (simulates JS behavior)
			if stats.ScreenHeight&4 != 0 {
				buf[offset] = byte(stats.ScrollY)
			} else {
				buf[offset] = byte(stats.AvailWidth)
			}
		}
	}

	// Expand 16 bytes → 32 bytes (simulating JS Uint16Array little-endian layout)
	result := make([]byte, 32)
	for i := 0; i < 16; i++ {
		result[i*2] = buf[i] // low byte
		result[i*2+1] = 0x00 // high byte (zero)
	}

	return base64.StdEncoding.EncodeToString(result)
}

// GenerateTokenPrepareStage generates the CToken for the order prepare stage.
// It simulates a fresh page view with minimal interactions.
func (gen *CTokenGenerator) GenerateTokenPrepareStage() string {
	token := makeToken(&windowStats{
		TouchCount:   uint16(rand.IntN(7) + 3),
		VisibleCount: uint16(rand.IntN(2) + 3),
		UnloadCount:  uint16(gen.generateCounts),
		StaySeconds:  uint16(time.Since(gen.begin).Seconds()),
		SinceInitSec: 0,
		ScrollX:      0,
		ScrollY:      0,
		InnerWidth:   1578,
		InnerHeight:  690,
		OuterWidth:   1578,
		OuterHeight:  690,
		ScreenX:      1699,
		ScreenY:      834,
		ScreenWidth:  1699,
		ScreenHeight: 834,
		AvailWidth:   1578,
	})
	gen.generateCounts++
	return token
}

// GenerateTokenCreateStage generates the CToken for the order create stage.
// It simulates a page that has been open for a while with more interactions.
func (gen *CTokenGenerator) GenerateTokenCreateStage(whenGenPToken time.Time) string {
	token := makeToken(&windowStats{
		TouchCount:   uint16(rand.IntN(7) + 3),
		VisibleCount: uint16(rand.IntN(13) + 3),
		UnloadCount:  uint16(gen.generateCounts),
		StaySeconds:  uint16(time.Since(gen.begin).Seconds()),
		SinceInitSec: uint16(time.Since(whenGenPToken).Seconds()),
		ScrollX:      0,
		ScrollY:      0,
		InnerWidth:   1578,
		InnerHeight:  690,
		OuterWidth:   1578,
		OuterHeight:  690,
		ScreenX:      1699,
		ScreenY:      834,
		ScreenWidth:  1699,
		ScreenHeight: 834,
		AvailWidth:   1578,
	})
	gen.generateCounts++
	return token
}

// IsHotProject returns true for CTokenGenerator (always a hot project).
func (gen *CTokenGenerator) IsHotProject() bool {
	return true
}
