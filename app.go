package main

import (
	"bilibili-ticket-golang/biliutils"
	"context"
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

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) GetBiliClient() *biliutils.BiliClient {
	return a.bili
}
