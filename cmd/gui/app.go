package main

import (
	"bilibili-ticket-golang/cmd/gui/i18n"
	"bilibili-ticket-golang/cmd/gui/store/configuration"
	"bilibili-ticket-golang/lib/biliutils"
	"context"
	"os"
)

// App struct
type App struct {
	ctx   context.Context
	bili  *biliutils.BiliClient
	store *configuration.DataStorage
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

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
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
