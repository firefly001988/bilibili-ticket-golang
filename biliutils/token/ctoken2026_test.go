package token_test

import (
	"bilibili-ticket-golang/biliutils/token"
	"encoding/base64"
	"fmt"
	"testing"
)

func TestCToken2026Generator(t *testing.T) {
	ecdata := &token.EncodeData{
		Ua:               "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36 Edg/149.0.0.0",
		Herf:             "https://mall.bilibili.com/neul-next/ticket-renovation/detail.html?id=1001653&outsideMall=no&outsideMall=no#themeType=2",
		DevicePixelRatio: 1.5,
		ScrollX:          0,
		ScrollY:          0,
		InnerWidth:       440,
		InnerHeight:      836,
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
	generator := token.NewCToken2026Generator(ecdata)
	if generator == nil {
		t.Fatal("Failed to create CToken2026Generator")
	}

	tokenStr := generator.GenerateTokenPrepareStage()
	if len(tokenStr) == 0 {
		t.Error("GenerateTokenPrepareStage returned empty token")
	}
	t.Logf("PrepareStage token: %s", tokenStr)
}

func TestCToken2026GeneratorWithRandomData(t *testing.T) {
	type fieldEntry struct {
		data   int
		length int
	}
	mapped := map[int]fieldEntry{
		0: {
			85,
			1,
		},
		1: {
			0,
			1,
		},
		2: {
			245,
			1,
		},
		3: {
			0,
			1,
		},
		4: {
			55,
			1,
		},
		5: {
			108,
			1,
		},
		6: {
			0,
			1,
		},
		7: {
			244,
			1,
		},
		8: {
			1,
			2,
		},
		10: {
			0,
			2,
		},
		12: {
			57,
			1,
		},
		13: {
			7,
			1,
		},
		14: {
			222,
			1,
		},
		15: {
			92,
			1,
		},
	}
	buf := make([]byte, 16)
	for i := 0; i < 16; i++ {
		if entry, ok := mapped[i]; ok {
			switch entry.length {
			case 1:
				val := entry.data
				buf[i] = byte(val)
			case 2:
				val := entry.data
				buf[i] = byte(val >> 8)
				buf[i+1] = byte(val & 0xFF)
				i++
			}
		}
	}
	result := make([]byte, 32)
	for i := 0; i < 16; i++ {
		result[i*2] = buf[i]
		result[i*2+1] = 0x00
	}
	fmt.Printf("Generated buffer: %x\n", buf)
	fmt.Printf("Generated result: %x\n", result)
	fmt.Printf("Generated token: %s\n", base64.StdEncoding.EncodeToString(result))
}
