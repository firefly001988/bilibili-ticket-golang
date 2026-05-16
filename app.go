package main

import (
	"bilibili-ticket-golang/biliutils"
	"context"
	"os"
)

// App struct
type App struct {
	ctx  context.Context
	bili *biliutils.BiliClient
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

func (a *App) GetBiliClient() *biliutils.BiliClient {
	return a.bili
}
