package token

import (
	"encoding/base64"
	"math/rand/v2"
	"time"
)

// CToken2026Generator is a new token generator for hot projects, based on the latest reverse engineering of the token generation algorithm.
// It simulates browser window statistics encoded in binary format, with a different set of fields and encoding logic compared to CToken2025Generator.
type CToken2026Generator struct {
	field      ctokenField
	whenGen    time.Time
	lastSubmit time.Time
}

type ctokenField struct {
	H      int
	F      int // 点击次数
	Y      int
	B      int // 页面切换次数
	z      int
	Q      int
	V      int // openWindow次数
	K      int
	G      int // 页面停留时间，单位秒
	U      int // 请求时间间隔
	W      int
	J      int
	X      int
	Dollar int
	Z      int
	ee     int
}

func NewCToken2026Generator(ecdata *EncodeData) *CToken2026Generator {
	return &CToken2026Generator{
		field: ctokenField{
			H:      ecdata.encode(1),
			F:      0,
			Y:      ecdata.encode(2),
			B:      0,
			z:      ecdata.encode(3),
			Q:      ecdata.encode(4),
			V:      0,
			K:      ecdata.encode(5),
			G:      0,
			U:      0,
			W:      ecdata.encode(6),
			J:      ecdata.encode(7),
			X:      ecdata.encode(8),
			Dollar: ecdata.encode(9),
			Z:      ecdata.encode(10),
			ee:     ecdata.encode(11),
		},
		whenGen:    time.Now(),
		lastSubmit: time.Time{},
	}
}

type EncodeData struct {
	Ua               string
	Herf             string
	DevicePixelRatio float64
	ScrollX          int
	ScrollY          int
	InnerWidth       int
	InnerHeight      int
	OuterWidth       int
	OuterHeight      int
	ScreenX          int
	ScreenY          int
	ScreenWidth      int
	ScreenHeight     int
	AvailWidth       int
	AvailHeight      int
	HistoryLength    int
}

func NewEncodeData(ua string, href string) *EncodeData {
	return &EncodeData{
		Ua:               ua,
		Herf:             href,
		DevicePixelRatio: 1.5,
		ScrollX:          0,
		ScrollY:          0,
		InnerWidth:       1707,
		InnerHeight:      900,
		OuterWidth:       1707,
		OuterHeight:      912,
		ScreenX:          0,
		ScreenY:          0,
		ScreenWidth:      1707,
		ScreenHeight:     960,
		AvailWidth:       1707,
		AvailHeight:      900,
		HistoryLength:    2,
	}
}

func (data *EncodeData) encode(index int) int {
	arr := [16]int{
		data.ScrollX,
		data.ScrollY,
		data.InnerWidth,
		data.InnerHeight,
		data.OuterWidth,
		data.OuterHeight,
		data.ScreenX,
		data.ScreenY,
		data.ScreenWidth,
		data.ScreenHeight,
		data.AvailWidth,
		data.HistoryLength,
		len(data.Ua),
		len(data.Herf),
		func() int {
			v := data.DevicePixelRatio * 10
			if v == 0 {
				return 10
			}
			return int(v)
		}(),
		int(time.Now().UnixMilli() % 256),
	}

	return arr[index%len(arr)] + arr[3*index%len(arr)] + 17*index&255
}

// Encode encodes the ctokenField into a base64 token string.
// It mimics the JavaScript encode() function:
//   - 16 bytes are filled according to the field map (offset → {data, length})
//   - 1-byte fields: clamped to 255, written as uint8
//   - 2-byte fields: clamped to 65535, written as uint16 big-endian (matching JS DataView.setUint16 default)
//   - Unmapped positions (9, 11): use fallback (Z & 4 ? Y : ee)
//   - Then toBinary: each byte → uint16 LE (high byte = 0), 16 bytes → 32 bytes → base64
func (f *ctokenField) Encode() string {
	buf := make([]byte, 16)

	type fieldEntry struct {
		data   int
		length int
	}
	fieldMap := map[int]fieldEntry{
		0:  {f.H, 1},
		1:  {f.F, 1},
		2:  {f.Y, 1},
		3:  {f.B, 1},
		4:  {f.z, 1},
		5:  {f.Q, 1},
		6:  {f.V, 1},
		7:  {f.K, 1},
		8:  {f.G, 2},
		10: {f.U, 2},
		12: {f.W, 1},
		13: {f.J, 1},
		14: {f.X, 1},
		15: {f.Dollar, 1},
	}

	for i := 0; i < 16; i++ {
		if entry, ok := fieldMap[i]; ok {
			switch entry.length {
			case 1:
				val := entry.data
				if val > 255 {
					val = 255
				}
				buf[i] = byte(val)
			case 2:
				val := entry.data
				if val > 65535 {
					val = 65535
				}
				// Big-endian: high byte first, matching JS DataView.setUint16 default
				buf[i] = byte((val >> 8) & 0xFF)
				buf[i+1] = byte(val & 0xFF)
				i++ // skip the next byte consumed by uint16
			}
		} else {
			// Fallback for unmapped indices (9, 11): 4 & Z ? Y : ee
			var fallback int
			if f.Z&4 != 0 {
				fallback = f.Y
			} else {
				fallback = f.ee
			}
			if fallback > 255 {
				fallback = 255
			}
			buf[i] = byte(fallback)
		}
	}

	// toBinary: expand 16 bytes → 32 bytes (uint16 little-endian, high byte zero)
	result := make([]byte, 32)
	for i := 0; i < 16; i++ {
		result[i*2] = buf[i]
		result[i*2+1] = 0x00
	}

	return base64.StdEncoding.EncodeToString(result)
}

// GenerateTokenPrepareStage generates the CToken for the order prepare stage.
// It simulates a fresh page view with minimal interactions.
func (gen *CToken2026Generator) GenerateTokenPrepareStage() string {
	gen.field.F = rand.IntN(7) + 3                       // 模拟点击次数
	gen.field.B = rand.IntN(2)                           // 模拟页面切换
	gen.field.V = rand.IntN(3)                           // 模拟 openWindow 次数
	gen.field.G = int(time.Since(gen.whenGen).Seconds()) // 页面停留时间
	gen.field.U = 0                                      // 首次请求，无间隔

	gen.lastSubmit = time.Now()
	return gen.field.Encode()
}

// GenerateTokenCreateStage generates the CToken for the order create stage.
// It simulates a page that has been open for a while with more interactions.
func (gen *CToken2026Generator) GenerateTokenCreateStage(whenGenPToken time.Time) string {
	gen.field.F += rand.IntN(3) + 1                         // 点击继续增加
	gen.field.B += rand.IntN(2) + 1                         // 页面切换继续增加
	gen.field.V += rand.IntN(2)                             // openWindow 继续增加
	gen.field.G = int(time.Since(gen.whenGen).Seconds())    // 页面停留时间
	gen.field.U = int(time.Since(gen.lastSubmit).Seconds()) // 距上次提交的间隔

	gen.lastSubmit = time.Now()
	return gen.field.Encode()
}

// IsHotProject returns true for CToken2026Generator (always a hot project).
func (gen *CToken2026Generator) IsHotProject() bool {
	return true
}
