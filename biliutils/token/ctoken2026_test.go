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

// TestCToken2026ReverseParser 逆解析 CToken 2026，将 base64 token 解码为可读的字段值。
// 用法：将抓包拿到的 ctoken 字符串填入 tokenStr，然后运行测试。
func TestCToken2026ReverseParser(t *testing.T) {
	// ========== 在这里填入要逆解析的 ctoken ==========
	tokenStr := "4wAGAAgAAgA0ACwAAADWAAAADwAAAAgAeAD6AMwAwwA="
	// ===============================================

	if tokenStr == "" {
		t.Skip("未提供 ctoken 字符串，跳过逆解析")
	}

	// Step 1: Base64 解码 → 32 字节
	raw32, err := base64.StdEncoding.DecodeString(tokenStr)
	if err != nil {
		t.Fatalf("Base64 解码失败: %v", err)
	}
	if len(raw32) != 32 {
		t.Fatalf("期望 32 字节，实际 %d 字节", len(raw32))
	}

	// Step 2: toBinary 逆操作 — 提取偶数位字节 (low byte)，得到 16 字节原始 buffer
	buf16 := make([]byte, 16)
	for i := 0; i < 16; i++ {
		buf16[i] = raw32[i*2]
	}

	t.Logf("Raw 16-byte buffer: %x", buf16)

	// Step 3: 字段映射表（与 Encode 中的 fieldMap 一致）
	// offset → {字段名, 字节长度}
	type fieldMeta struct {
		Name   string
		Length int // 1 或 2
	}
	fieldMap := map[int]fieldMeta{
		0:  {"Param1", 1},
		1:  {"Param2（TouchCount / 点击次数）", 1},
		2:  {"Param7", 1},
		3:  {"Param3（VisibilityChangeCount / 页面切换次数）", 1},
		4:  {"Param8", 1},
		5:  {"Param9", 1},
		6:  {"Param4（UnloadCount / openWindow 次数）", 1},
		7:  {"Param10", 1},
		8:  {"Param5（Time1 / 页面停留时间 秒）", 2},
		10: {"Param6（Time2 / 距上次提交间隔 秒）", 2},
		12: {"Param11", 1},
		13: {"Param12", 1},
		14: {"Param13", 1},
		15: {"Param14", 1},
	}

	// 未映射位置说明
	fallbackPositions := map[int]string{
		9:  "fallback（Param15&4!=0 ? Param6 : Param16）",
		11: "fallback（Param15&4!=0 ? Param6 : Param16）",
	}

	fmt.Println("\n========== CToken 2026 逆解析结果 ==========")
	fmt.Printf("输入 token: %s\n", tokenStr)
	fmt.Printf("16 字节 hex: %x\n\n", buf16)

	// Step 4: 按偏移量解析
	i := 0
	for i < 16 {
		if meta, ok := fieldMap[i]; ok {
			var val int
			switch meta.Length {
			case 1:
				val = int(buf16[i])
				fmt.Printf("  offset %2d  %-40s = %d (0x%02x)\n", i, meta.Name, val, val)
				i++
			case 2:
				if i+1 < 16 {
					// Big-endian: high byte at i, low byte at i+1
					val = int(buf16[i])<<8 | int(buf16[i+1])
					fmt.Printf("  offset %2d  %-40s = %d (0x%04x)\n", i, meta.Name, val, val)
				}
				i += 2
			}
		} else {
			// 未映射位置（fallback）
			desc, ok := fallbackPositions[i]
			if !ok {
				desc = "未知"
			}
			fmt.Printf("  offset %2d  %-40s = %d (0x%02x) [未映射/fallback]\n", i, desc, buf16[i], buf16[i])
			i++
		}
	}

	fmt.Println("\n========== 字段含义参考 ==========")
	fmt.Println("  Param1, Param7-Param14 : encode(index) 派生值，基于窗口统计信息")
	fmt.Println("  Param2 (TouchCount)    : 模拟的页面点击次数")
	fmt.Println("  Param3 (VisibilityChangeCount) : 页面可见性切换次数")
	fmt.Println("  Param4 (UnloadCount)   : openWindow / 页面卸载次数")
	fmt.Println("  Param5 (Time1, 2 bytes): 自页面加载起的停留时间（秒）")
	fmt.Println("  Param6 (Time2, 2 bytes): 距上次请求的间隔（秒）")
	fmt.Println("  fallback 位            : Param15 & 4 ? Param6 : Param16")
	fmt.Println("==============================================")
}

// TestCToken2026Roundtrip 往返验证：生成 → 逆解析 → 对比，确保编解码一致。
func TestCToken2026Roundtrip(t *testing.T) {
	ecdata := token.NewEncodeData(
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"https://show.bilibili.com/platform/detail.html?id=12345",
	)
	gen := token.NewCToken2026Generator(ecdata)
	tokenStr := gen.GenerateTokenPrepareStage()
	t.Logf("生成的 token: %s", tokenStr)

	// ===== 逆解析 =====
	raw32, err := base64.StdEncoding.DecodeString(tokenStr)
	if err != nil {
		t.Fatalf("Base64 解码失败: %v", err)
	}
	if len(raw32) != 32 {
		t.Fatalf("期望 32 字节，实际 %d 字节", len(raw32))
	}

	buf16 := make([]byte, 16)
	for i := 0; i < 16; i++ {
		buf16[i] = raw32[i*2]
	}
	t.Logf("16 字节 hex: %x", buf16)

	// ===== 重新编码验证一致性 =====
	// 构造相同的 16 字节 → 32 字节 → base64
	result := make([]byte, 32)
	for i := 0; i < 16; i++ {
		result[i*2] = buf16[i]
		result[i*2+1] = 0x00
	}
	reEncoded := base64.StdEncoding.EncodeToString(result)

	if reEncoded != tokenStr {
		t.Errorf("往返编码不一致!\n  原始: %s\n  重编: %s", tokenStr, reEncoded)
	} else {
		t.Log("✓ 往返编码一致")
	}

	// ===== 逐字段解析并输出 =====
	type fieldMeta struct {
		Name   string
		Length int
	}
	fieldMap := map[int]fieldMeta{
		0:  {"Param1", 1},
		1:  {"Param2(TouchCount)", 1},
		2:  {"Param7", 1},
		3:  {"Param3(VisibilityChange)", 1},
		4:  {"Param8", 1},
		5:  {"Param9", 1},
		6:  {"Param4(UnloadCount)", 1},
		7:  {"Param10", 1},
		8:  {"Param5(Time1)", 2},
		10: {"Param6(Time2)", 2},
		12: {"Param11", 1},
		13: {"Param12", 1},
		14: {"Param13", 1},
		15: {"Param14", 1},
	}

	fmt.Println("\n===== 往返验证：逐字段解析 =====")
	i := 0
	for i < 16 {
		if meta, ok := fieldMap[i]; ok {
			var val int
			switch meta.Length {
			case 1:
				val = int(buf16[i])
				fmt.Printf("  [%02d] %-25s = %3d (0x%02x)\n", i, meta.Name, val, val)
				i++
			case 2:
				val = int(buf16[i])<<8 | int(buf16[i+1])
				fmt.Printf("  [%02d] %-25s = %3d (0x%04x)\n", i, meta.Name, val, val)
				i += 2
			}
		} else {
			fmt.Printf("  [%02d] <fallback>             = %3d (0x%02x)\n", i, buf16[i], buf16[i])
			i++
		}
	}
	fmt.Println("==================================")
}
