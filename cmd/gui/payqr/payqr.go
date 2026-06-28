package payqr

import (
	"net/url"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// OpenWindow creates a payment QR window with the given title and query
// parameters. The frontend JS will auto-resize the window to fit its content
// after the page renders.
func OpenWindow(wailsApp *application.App, title string, params url.Values) {
	if wailsApp == nil {
		return
	}
	window := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            title,
		BackgroundColour: application.RGBA{Red: 27, Green: 38, Blue: 54, Alpha: 255},
		URL:              "/#/pay-qr?" + params.Encode(),
		MinWidth:         600,
		MinHeight:        850,
	})
	window.Show()
	window.Center()
	window.Focus()
}
