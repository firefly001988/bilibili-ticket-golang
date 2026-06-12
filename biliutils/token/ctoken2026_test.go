package token_test

import (
	"bilibili-ticket-golang/biliutils/token"
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
