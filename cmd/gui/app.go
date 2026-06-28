package main

import (
	"bilibili-ticket-golang/cmd/gui/i18n"
	"bilibili-ticket-golang/cmd/gui/payqr"
	"bilibili-ticket-golang/cmd/gui/store/configuration"
	"bilibili-ticket-golang/lib/biliutils"
	"fmt"
	"net/url"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// App is exposed as a Wails v3 service.
type App struct {
	bili     *biliutils.BiliClient
	store    *configuration.DataStorage
	wailsApp *application.App
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

// GetBiliClient returns the underlying BiliClient.
func (a *App) GetBiliClient() *biliutils.BiliClient {
	return a.bili
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
