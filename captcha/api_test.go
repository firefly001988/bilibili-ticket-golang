//go:build windows && amd64

package captcha

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// =============================================================================
// Bilibili captcha API 响应结构体（与 lib/models/bili/captcha/structs.go 一致）
// =============================================================================

type registerVoucherResp struct {
	Type    string `json:"type"`
	Token   string `json:"token"`
	Geetest struct {
		Gt        string `json:"gt"`
		Challenge string `json:"challenge"`
	} `json:"geetest"`
}

type biliAPIResp struct {
	Code    int                 `json:"code"`
	Message string              `json:"message"`
	Data    registerVoucherResp `json:"data"`
}

// fetchCaptcha 从 Bilibili 获取一个真实验证码的 gt 和 challenge。
func fetchCaptcha() (gt, challenge string, err error) {
	req, _ := http.NewRequest("GET",
		"https://passport.bilibili.com/x/passport-login/captcha?source=main_web", nil)
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("读取响应失败: %w", err)
	}

	var r biliAPIResp
	if err := json.Unmarshal(body, &r); err != nil {
		return "", "", fmt.Errorf("解析 JSON 失败: %w (body: %.200s)", err, string(body))
	}
	if r.Code != 0 {
		return "", "", fmt.Errorf("Bilibili API 错误 code=%d: %s", r.Code, r.Message)
	}

	return r.Data.Geetest.Gt, r.Data.Geetest.Challenge, nil
}

// =============================================================================
// 测试入口
// =============================================================================

func TestMain(m *testing.M) {
	if err := Init("./libs"); err != nil {
		panic(fmt.Sprintf("Init 失败: %v", err))
	}
	m.Run()
}

// =============================================================================
// 基础测试
// =============================================================================

func TestVersion(t *testing.T) {
	v, err := Version()
	if err != nil {
		t.Fatalf("Version() error: %v", err)
	}
	if v.Version == "" {
		t.Error("Version 字符串为空")
	}
	t.Logf("DLL 版本: %s (commit: %s)", v.Version, v.GitCommit)
}

func TestGetType(t *testing.T) {
	gt, challenge, err := fetchCaptcha()
	if err != nil {
		t.Skipf("跳过：无法获取验证码 (%v)", err)
	}
	typ, err := GetType(gt, challenge, "")
	if err != nil {
		t.Fatalf("GetType() error: %v", err)
	}
	if typ != TypeSlide && typ != TypeClick {
		t.Errorf("非预期类型: %s (%d)", typ, typ)
	}
	t.Logf("gt=%s challenge=%s → type=%s", truncateStr(gt, 20), truncateStr(challenge, 20), typ)
}

// =============================================================================
// 一键求解 Solve（与 main.go 中 testCaptchaPlugin 模式一致）
// =============================================================================

func TestSolve(t *testing.T) {
	gt, challenge, err := fetchCaptcha()
	if err != nil {
		t.Skipf("跳过：无法获取验证码 (%v)", err)
	}

	start := time.Now()
	validate, err := Solve(gt, challenge)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Solve() error (耗时 %v): %v", elapsed, err)
	}
	if validate == "" {
		t.Error("validate 为空")
	}
	t.Logf("✅ Solve 成功 (耗时 %v) validate=%s", elapsed, validate)
}

// =============================================================================
// 分步求解 —— 精确镜像 main.go 第 242-287 行逻辑
// =============================================================================

func TestSolveStepByStep(t *testing.T) {
	gt, challenge, err := fetchCaptcha()
	if err != nil {
		t.Skipf("跳过：无法获取验证码 (%v)", err)
	}
	t.Logf("gt=%s challenge=%s", gt, challenge)

	// Step 1 — GetCS
	cs, err := GetCS(gt, challenge, "")
	if err != nil {
		t.Fatalf("GetCS() error: %v", err)
	}
	if cs.S == "" {
		t.Error("cs.S 为空")
	}
	t.Logf("Step 1 GetCS: S=%s, C_len=%d", cs.S, len(cs.C))

	// Step 2 — GetType
	captType, err := GetType(gt, challenge, "")
	if err != nil {
		t.Fatalf("GetType() error: %v", err)
	}
	if captType != TypeSlide && captType != TypeClick {
		t.Errorf("非预期类型: %s (%d)", captType, captType)
	}
	t.Logf("Step 2 GetType: %s", captType)

	// Step 3 — GetNewCSArgs
	var args *NewCSArgs
	switch captType {
	case TypeClick:
		args, err = GetNewCSArgsClick(gt, challenge)
	case TypeSlide:
		args, err = GetNewCSArgsSlide(gt, challenge)
	default:
		t.Fatalf("未知验证码类型: %s", captType)
	}
	if err != nil {
		t.Fatalf("GetNewCSArgs*() error: %v", err)
	}
	t.Logf("Step 3 GetNewCSArgs: S=%s, C_len=%d, PicURL=%s, FullBg=%s",
		args.S, len(args.C), truncateStr(args.PicURL, 40), truncateStr(args.FullBgURL, 40))

	// Step 4 — CalculateKey
	before := time.Now()
	var key string
	switch captType {
	case TypeClick:
		key, err = CalculateKeyClick(args.PicURL)
	case TypeSlide:
		key, err = CalculateKeySlide(args.FullBgURL, args.MissBgURL, args.SliderURL)
	}
	if err != nil {
		t.Fatalf("CalculateKey*() error: %v", err)
	}
	if key == "" {
		t.Error("key 为空")
	}
	t.Logf("Step 4 CalculateKey: key=%s (耗时 %v)", key, time.Since(before))

	// Step 5 — GenerateW
	var w string
	switch captType {
	case TypeClick:
		w, err = GenerateWClick(key, gt, challenge, args.C, args.S)
	case TypeSlide:
		w, err = GenerateWSlide(key, gt, challenge, args.C, args.S)
	}
	if err != nil {
		t.Fatalf("GenerateW*() error: %v", err)
	}
	if w == "" {
		t.Error("w 为空")
	}
	t.Logf("Step 5 GenerateW: w=%s", truncateStr(w, 60))

	// click 类型需要至少 2 秒间隔（与 main.go 一致）
	if captType == TypeClick {
		use := time.Since(before)
		if use < 2*time.Second {
			time.Sleep(2*time.Second - use)
		}
	}

	// Step 6 — Verify
	result, err := Verify(gt, challenge, w)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if result.Validate == "" {
		t.Error("result.Validate 为空")
	}
	t.Logf("Step 6 Verify: validate=%s, message=%s", result.Validate, result.Message)
	t.Logf("✅ 分步求解成功 (总耗时 %v)", time.Since(before))
}

// =============================================================================
// 便捷函数
// =============================================================================

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
