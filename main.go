package main

import (
	"bilibili-ticket-golang/biliutils"
	notify2 "bilibili-ticket-golang/biliutils/notify"
	"bilibili-ticket-golang/biliutils/scheduler"
	"bilibili-ticket-golang/global"
	"bilibili-ticket-golang/store/configuration"
	"bilibili-ticket-golang/store/cookiejar"
	"bytes"
	"context"
	"embed"
	"io"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Pipe stdout + stderr to main.log for post-mortem debugging.
	logFile, err := os.OpenFile("logs/main.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		defer logFile.Close()
		tw := &timestampWriter{w: logFile}
		log.SetOutput(tw)
		log.SetFlags(0) // timestampWriter handles the timestamp prefix

		// Redirect os.Stdout / os.Stderr through pipes so that println and
		// third-party libraries writing to stdout/stderr are captured.
		// (In Wails desktop builds stdout/stderr are discarded by default.)
		rOut, wOut, _ := os.Pipe()
		rErr, wErr, _ := os.Pipe()
		os.Stdout = wOut
		os.Stderr = wErr
		go io.Copy(tw, rOut)
		go io.Copy(tw, rErr)
	} else {
		log.SetOutput(os.Stderr)
	}

	// Create an instance of the app structure
	//app := NewApp()
	store := configuration.NewDataStorage()
	err = store.Load()
	if err != nil {
		panic("Failed to load data:" + err.Error())
	}
	jar := cookiejar.New(&cookiejar.Options{
		DefaultCookies: store.Cookies,
	})
	c, err := biliutils.NewBiliClientWithCookiejar(jar)
	if err != nil {
		panic(err)
	}

	// Wire up cookie persistence: called from frontend after login & on exit
	c.SetCookieSaveCallback(func() {
		store.Cookies = jar.AllPersistentEntries()
		store.RefreshToken = c.GetRefreshToken()
		if saveErr := store.Save(); saveErr != nil {
			println("Failed to persist cookies:", saveErr.Error())
		}
	})

	// Restore refresh token from previous session
	c.SetRefreshToken(store.RefreshToken)

	// Log broker for real-time task log streaming to the frontend
	logStorage := scheduler.NewLogStorage()
	if err := logStorage.Load(); err != nil {
		println("Failed to load persisted logs:", err.Error())
	}
	logBroker := scheduler.NewLogBroker(logStorage)

	// Build MultiNotifier from persisted notification channels
	notifier := notify2.NewMultiNotifier()
	for _, ch := range store.NotifyChData.GetAll() {
		n, err := ch.ToNotifier()
		if err == nil {
			notifier.Add(n)
		}
	}

	// Scheduler service — orchestrates tasks, BiliClient, LogBroker and ticket storage
	schedSvc := scheduler.NewSchedulerService(c, logBroker, store.TicketData, notifier, store.NotifyChData, store)

	defer func() {
		c.PersistCookies()
		logBroker.FlushLogs()
	}()

	// Auto-restart persisted tasks on launch (DefaultIntervalMs)
	schedSvc.ReloadTickets(global.DefaultIntervalMs)

	// Keep tickets persisted on change
	store.TicketData.SetChangeCallback(func(_ *configuration.TicketData, _ configuration.TicketEntry) {
		if saveErr := store.Save(); saveErr != nil {
			println("Failed to persist tickets:", saveErr.Error())
		}
	})

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "bilibili-ticket-golang",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup: func(ctx context.Context) {
			logBroker.SetContext(ctx)
		},
		Bind: []interface{}{
			c,
			logBroker,
			schedSvc,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// timestampWriter writes output to an io.Writer, prepending a timestamp
// to each line and syncing (if the underlying writer is an *os.File) after
// every write so that crash logs are never lost.
type timestampWriter struct {
	w   io.Writer
	buf bytes.Buffer
}

const timeLayout = "2006-01-02 15:04:05 "

func (t *timestampWriter) Write(p []byte) (int, error) {
	written := len(p)
	for _, b := range p {
		if b == '\n' {
			line := append([]byte(time.Now().Format(timeLayout)), t.buf.Bytes()...)
			line = append(line, '\n')
			if _, err := t.w.Write(line); err != nil {
				return 0, err
			}
			t.buf.Reset()
		} else {
			t.buf.WriteByte(b)
		}
	}
	// Sync if possible
	if f, ok := t.w.(*os.File); ok {
		f.Sync()
	}
	return written, nil
}
