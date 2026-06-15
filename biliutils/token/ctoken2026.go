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
	Param1                int
	TouchCount            int
	VisibilityChangeCount int
	UnloadCount           int
	Time1                 int
	Time2                 int
	Param7                int
	Param8                int
	Param9                int
	Param10               int
	Param11               int
	Param12               int
	Param13               int
	Param14               int
	Param15               int
	Param16               int
}

func NewCToken2026Generator(ecdata *EncodeData) *CToken2026Generator {
	return &CToken2026Generator{
		field: ctokenField{
			Param1:                ecdata.encode(1),
			TouchCount:            0,
			VisibilityChangeCount: 0,
			UnloadCount:           0,
			Time1:                 0,
			Time2:                 0,
			Param7:                ecdata.encode(2),
			Param8:                ecdata.encode(3),
			Param9:                ecdata.encode(4),
			Param10:               ecdata.encode(5),
			Param11:               ecdata.encode(6),
			Param12:               ecdata.encode(7),
			Param13:               ecdata.encode(8),
			Param14:               ecdata.encode(9),
			Param15:               ecdata.encode(10),
			Param16:               ecdata.encode(11),
		},
		whenGen:    time.Now(),
		lastSubmit: time.Now(),
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
		DevicePixelRatio: []float64{1.0, 1.25, 1.5, 2.0}[rand.IntN(4)],
		ScrollX:          0,
		ScrollY:          0,
		InnerWidth:       rand.IntN(200) + 1500,
		InnerHeight:      rand.IntN(200) + 700,
		OuterWidth:       rand.IntN(200) + 1500,
		OuterHeight:      rand.IntN(200) + 800,
		ScreenX:          0,
		ScreenY:          0,
		ScreenWidth:      rand.IntN(200) + 1500,
		ScreenHeight:     rand.IntN(200) + 800,
		AvailWidth:       rand.IntN(200) + 1400,
		AvailHeight:      rand.IntN(200) + 700,
		HistoryLength:    rand.IntN(3) + 1,
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

	return (arr[index%len(arr)] + arr[3*index%len(arr)] + 17*index) & 255
}

// Encode encodes the ctokenField into a base64 token string.
// It mimics the JavaScript encode() function:
//   - 16 bytes are filled according to the field map (offset → {data, length})
//   - 1-byte fields: clamped to 255, written as uint8
//   - 2-byte fields: clamped to 65535, written as uint16 big-endian (matching JS DataView.setUint16 default)
//   - Unmapped positions (9, 11): use fallback (Z & 4 ? Q : ee)
//   - Then toBinary: each byte → uint16 LE (high byte = 0), 16 bytes → 32 bytes → base64
func (f *ctokenField) Encode() string {
	buf := make([]byte, 16)

	type fieldEntry struct {
		data   int
		length int
	}
	fieldMap := map[int]fieldEntry{
		0:  {f.Param1, 1},
		1:  {f.TouchCount, 1},
		2:  {f.Time2, 1},
		3:  {f.VisibilityChangeCount, 1},
		4:  {f.Param7, 1},
		5:  {f.Param8, 1},
		6:  {f.UnloadCount, 1},
		7:  {f.Param10, 1},
		8:  {f.Time1, 2},
		10: {f.Time2, 2},
		12: {f.Param11, 1},
		13: {f.Param12, 1},
		14: {f.Param13, 1},
		15: {f.Param14, 1},
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
				buf[i] = byte(val >> 8)
				buf[i+1] = byte(val & 0xFF)
				i++ // skip the next byte consumed by uint16
			}
		} else {
			// Fallback for unmapped indices (9, 11): 4 & Z ? Q : ee
			var fallback int
			if f.Param15&4 != 0 {
				fallback = f.Time2
			} else {
				fallback = f.Param16
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
	gen.field.TouchCount = rand.IntN(7) + 3                       // 模拟点击次数
	gen.field.VisibilityChangeCount = rand.IntN(2)                // 模拟页面切换
	gen.field.UnloadCount = rand.IntN(3)                          // 模拟 openWindow 次数
	gen.field.Time1 = int(time.Since(gen.whenGen).Seconds() + 15) // 页面停留时间
	gen.field.Time2 = 0                                           // 首次请求，无间隔

	gen.lastSubmit = time.Now()
	return gen.field.Encode()
}

// GenerateTokenCreateStage generates the CToken for the order create stage.
// It simulates a page that has been open for a while with more interactions.
func (gen *CToken2026Generator) GenerateTokenCreateStage(whenGenPToken time.Time) string {
	gen.field.TouchCount += rand.IntN(3) + 1                      // 点击继续增加
	gen.field.VisibilityChangeCount += rand.IntN(2) + 1           // 页面切换继续增加
	gen.field.UnloadCount += rand.IntN(2)                         // openWindow 继续增加
	gen.field.Time1 = int(time.Since(gen.whenGen).Seconds() + 15) // 页面停留时间
	gen.field.Time2 = int(time.Since(gen.lastSubmit).Seconds())   // 距上次提交的间隔

	gen.lastSubmit = time.Now()
	return gen.field.Encode()
}

// IsHotProject returns true for CToken2026Generator (always a hot project).
func (gen *CToken2026Generator) IsHotProject() bool {
	return true
}
