package main

import (
	"bilibili-ticket-golang/biliutils"
	"bilibili-ticket-golang/biliutils/notify"
	"bilibili-ticket-golang/biliutils/scheduler"
	"bilibili-ticket-golang/plugins"
	"bilibili-ticket-golang/plugins/captcha"
	"bilibili-ticket-golang/store/configuration"
	"bilibili-ticket-golang/store/cookiejar"
	"bytes"
	"context"
	"embed"
	"fmt"
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
			log.Printf("[main] Failed to persist cookies: %v", saveErr)
		}
	})

	// Restore refresh token from previous session
	c.SetRefreshToken(store.RefreshToken)

	// Log broker for real-time task log streaming to the frontend
	logStorage := scheduler.NewLogStorage()
	if err := logStorage.Load(); err != nil {
		log.Printf("[main] Failed to load persisted logs: %v", err)
	}

	logBroker := scheduler.NewLogBroker(logStorage)

	// Build MultiNotifier from persisted notification channels
	notifier := notify.NewMultiNotifier()
	for _, ch := range store.NotifyChData.GetAll() {
		n, err := ch.ToNotifier()
		if err == nil {
			notifier.Add(n)
		}
	}

	// Scheduler service — orchestrates tasks, BiliClient, LogBroker and ticket storage
	schedSvc := scheduler.NewSchedulerService(c, logBroker, store.TicketData, store.BWSData, notifier, store.NotifyChData, store)

	// App instance for frontend verification & misc utilities
	app := NewAppWithClient(c)

	defer func() {
		schedSvc.StopClockCalibration()
		c.PersistCookies()
		logBroker.FlushLogs()
	}()

	// Auto-restart persisted tasks on launch (using stored retry interval)
	schedSvc.ReloadTickets(store.RetryIntervalMs)
	schedSvc.ReloadBWSTasks()

	// Start periodic clock calibration against Bilibili server (every 10s)
	schedSvc.StartClockCalibration()

	// Keep tickets persisted on change
	store.TicketData.SetChangeCallback(func(_ *configuration.TicketData, _ configuration.TicketEntry) {
		if saveErr := store.Save(); saveErr != nil {
			log.Printf("[main] Failed to persist tickets: %v", saveErr)
		}
	})

	var solverFunc = func(gt string, challenge string) (string, error) {
		// Placeholder solver that always fails. Will be replaced if captcha plugin loads successfully.
		return "", fmt.Errorf("captcha solver not available")
	}

	pluginManager := plugins.NewPluginManager("plugins")

	defer pluginManager.UnloadAll()

	err = pluginManager.LoadPlugin("captcha-plugin")
	if err != nil {
		log.Printf("[main] Failed to load captcha plugin: %v", err)
	} else {
		//Captcha Solver Plugin
		solver, err := captcha.Dispense(pluginManager.GetClient("captcha-plugin"))
		if err != nil {
			log.Printf("[main] Failed to dispense captcha plugin: %v", err)
		} else {
			solverFunc = func(gt string, challenge string) (string, error) {
				id, _ := solver.Create(gt, challenge)
				_, err = solver.GetCS(id, "")
				if err != nil {
					return "", fmt.Errorf("GetCS error: %w", err)
				}
				_, err := solver.GetType(id, "")
				if err != nil {
					return "", fmt.Errorf("GetType error: %w", err)
				}
				args, err := solver.GetNewCSArgs(id)
				if err != nil {
					return "", fmt.Errorf("GetNewCSArgs error: %w", err)
				}
				key, err := solver.CalculateKey(id, args)
				if err != nil {
					return "", fmt.Errorf("CalculateKey error: %w", err)
				}
				w, err := solver.GenerateW(id, key, args)
				if err != nil {
					return "", fmt.Errorf("GenerateW error: %w", err)
				}
				return solver.Verify(id, w)
			}
			// Wire the solver into BiliClient so voucher errors are auto-resolved.
			c.SetCaptchaSolver(solverFunc)
			log.Printf("[main] Captcha solver installed — vouchers will be auto-resolved")
		}
	}

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
			app,
			c,
			logBroker,
			schedSvc,
			pluginManager,
		},
	})

	if err != nil {
		log.Printf("[main] Error: %v", err)
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
