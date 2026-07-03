package main

import (
	"bilibili-ticket-golang/cmd/gui/i18n"
	"bilibili-ticket-golang/cmd/gui/payqr"
	"bilibili-ticket-golang/cmd/gui/store/configuration"
	"bilibili-ticket-golang/lib/biliutils"
	"bilibili-ticket-golang/lib/global"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	gc "bilibili-ticket-golang/captcha-solver"
	api "bilibili-ticket-golang/lib/models/bili/api"
	gcaptcha "bilibili-ticket-golang/lib/models/bili/captcha"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// App is exposed as a Wails v3 service.
type App struct {
	bili          *biliutils.BiliClient
	store         *configuration.DataStorage
	wailsApp      *application.App
	captchaSolver biliutils.CaptchaSolverFn
}

// NewApp creates a new App application struct
func NewApp() *App {
	c, err := biliutils.NewBiliClient()
	if err != nil {
		panic(err)
	}
	return &App{
		bili: c,
	}
}

// NewAppWithClient creates an App with an existing BiliClient.
func NewAppWithClient(c *biliutils.BiliClient) *App {
	return &App{bili: c}
}

// NewAppWithClientAndStore creates an App with BiliClient and DataStorage for locale persistence.
func NewAppWithClientAndStore(c *biliutils.BiliClient, store *configuration.DataStorage) *App {
	return &App{bili: c, store: store}
}

// IsVerified checks whether the anti-scalper declaration has been accepted.
func (a *App) IsVerified() bool {
	_, err := os.Stat("data/.verified")
	return err == nil
}

// Verify accepts the anti-scalper declaration. Returns true if the input
// matches the required phrase and persistence succeeds.
func (a *App) Verify(input string) bool {
	if input != "黄牛死全家" {
		return false
	}
	os.MkdirAll("data", 0755)
	return os.WriteFile("data/.verified", []byte("1"), 0644) == nil
}

// TestFaultError triggers a test Fault error for verifying the frontend error
// display.  It returns a Fault with file:line, operation name, a simulated
// underlying error, and a human-readable hint.
func (a *App) TestFaultError() error {
	return global.NewFault("测试错误报告功能",
		fmt.Errorf("这是一个模拟的底层错误: 数据库连接超时"),
		"这只是一个测试错误，用于验证 cause 字段是否正确显示文件:行号信息",
	)
}

// SetLocale sets the application locale and persists it.
func (a *App) SetLocale(locale string) {
	i18n.SetLocale(locale)
	if a.store != nil {
		a.store.Locale = locale
		_ = a.store.Save()
	}
}

// GetLocale returns the current application locale.
// Returns empty string if no locale has been set (first startup).
func (a *App) GetLocale() string {
	return i18n.GetLocale()
}

// SetApp stores the Wails v3 application reference for window management.
func (a *App) SetApp(app *application.App) {
	a.wailsApp = app
}

// OpenQRTestWindow creates a new window with a test QR code.
func (a *App) OpenQRTestWindow(text string) {
	if a.wailsApp == nil {
		return
	}
	if text == "" {
		text = "https://space.bilibili.com"
	}
	a.OpenPayQRWindow(PayQRWindowOptions{
		Link:  text,
		Title: "测试二维码",
	})
}

type PayQRWindowOptions struct {
	Link      string `json:"link"`
	Title     string `json:"title,omitempty"`
	Project   string `json:"project,omitempty"`
	Screen    string `json:"screen,omitempty"`
	SKU       string `json:"sku,omitempty"`
	Buyer     string `json:"buyer,omitempty"`
	Expire    int64  `json:"expire,omitempty"`
	OrderTime int64  `json:"orderTime,omitempty"`
}

// OpenPayQRWindow opens a compact QR-code payment window.
func (a *App) OpenPayQRWindow(options PayQRWindowOptions) {
	if a.wailsApp == nil || options.Link == "" {
		return
	}
	title := options.Title
	if title == "" {
		title = "支付二维码"
	}
	values := url.Values{}
	values.Set("link", options.Link)
	values.Set("title", title)
	if options.Project != "" {
		values.Set("project", options.Project)
	}
	if options.Screen != "" {
		values.Set("screen", options.Screen)
	}
	if options.SKU != "" {
		values.Set("sku", options.SKU)
	}
	if options.Buyer != "" {
		values.Set("buyer", options.Buyer)
	}
	if options.Expire > 0 {
		values.Set("expire", fmt.Sprint(options.Expire))
	}
	if options.OrderTime > 0 {
		values.Set("orderTime", fmt.Sprint(options.OrderTime))
	}

	payqr.OpenWindow(a.wailsApp, title, values)
}

// =============================================================================
// Captcha solver —— 供前端检测和测试
// =============================================================================

// SetCaptchaSolver stores the captcha solving function.
func (a *App) SetCaptchaSolver(solver biliutils.CaptchaSolverFn) {
	a.captchaSolver = solver
}

// HasCaptchaSolver returns whether a captcha solver is installed and available.
func (a *App) HasCaptchaSolver() bool {
	return a.bili.HasCaptchaSolver()
}

// CaptchaTestResult is returned by TestCaptchaSolver.
type CaptchaTestResult struct {
	Success  bool   `json:"success"`
	Elapsed  string `json:"elapsed"`
	Validate string `json:"validate,omitempty"`
	Error    string `json:"error,omitempty"`
	Type     string `json:"type,omitempty"`
}

// TestCaptchaSolver 从 Bilibili 获取真实验证码并测试求解器。
// 返回 CaptchaTestResult 供前端展示。
func (a *App) TestCaptchaSolver() CaptchaTestResult {
	if a.captchaSolver == nil && !a.bili.HasCaptchaSolver() {
		return CaptchaTestResult{Success: false, Error: "验证码求解器未安装"}
	}

	req, _ := http.NewRequest("GET",
		"https://passport.bilibili.com/x/passport-login/captcha?source=main_web", nil)
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return CaptchaTestResult{Success: false, Error: fmt.Sprintf("请求验证码失败: %v", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CaptchaTestResult{Success: false, Error: fmt.Sprintf("读取响应失败: %v", err)}
	}

	var r api.MainApiDataRoot[gcaptcha.RegisterVoucherResponse]
	if err := json.Unmarshal(body, &r); err != nil {
		return CaptchaTestResult{Success: false, Error: fmt.Sprintf("解析失败: %v (raw: %.200s)", err, string(body))}
	}
	if r.Code != 0 {
		return CaptchaTestResult{Success: false, Error: fmt.Sprintf("API 错误 code=%d: %s", r.Code, r.Message)}
	}

	gt := r.Data.Geetest.Gt
	challenge := r.Data.Geetest.Challenge
	captTypeStr := r.Data.Type

	solver := a.captchaSolver
	if solver == nil {
		return CaptchaTestResult{Success: false, Error: "验证码求解函数未设置"}
	}

	start := time.Now()
	validate, err := solver(gt, challenge)
	elapsed := time.Since(start)

	if err != nil {
		return CaptchaTestResult{
			Success: false,
			Elapsed: elapsed.String(),
			Error:   err.Error(),
			Type:    captTypeStr,
		}
	}
	return CaptchaTestResult{
		Success:  true,
		Elapsed:  elapsed.String(),
		Validate: validate,
		Type:     captTypeStr,
	}
}

// HasCaptchaDLL returns whether the native captcha DLL was loaded.
func (a *App) HasCaptchaDLL() bool {
	// 尝试多个可能的 libs 目录
	for _, dir := range []string{"./libs", "../libs", "../../libs"} {
		if gc.IsAvailable(dir) {
			return true
		}
	}
	// 运行时从可执行文件同目录查找
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Join(filepath.Dir(exe), "libs")
		if gc.IsAvailable(exeDir) {
			return true
		}
	}
	return false
}
