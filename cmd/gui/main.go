package main

import (
	clusterstorage "bilibili-ticket-golang/cluster/storage"
	"bilibili-ticket-golang/cmd/gui/cluster_service"
	"bilibili-ticket-golang/cmd/gui/i18n"
	"bilibili-ticket-golang/cmd/gui/store/configuration"
	"bilibili-ticket-golang/cmd/gui/store/cookiejar"
	"bilibili-ticket-golang/lib/biliutils"
	"bilibili-ticket-golang/lib/biliutils/notify"
	"bilibili-ticket-golang/lib/biliutils/scheduler"
	"bilibili-ticket-golang/lib/models/bili/api"
	gcaptcha "bilibili-ticket-golang/lib/models/bili/captcha"
	"bilibili-ticket-golang/lib/plugins"
	"bilibili-ticket-golang/lib/plugins/captcha"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[scheduler.LogEntry]("ticket:log")
}

func testCaptchaPlugin(solverFunc func(gt string, challenge string) (string, error), pm *plugins.PluginManager, pluginName string) {
	go func() {
		req, reqErr := http.NewRequest("GET", "https://passport.bilibili.com/x/passport-login/captcha?source=main_web", nil)
		if reqErr != nil {
			pm.SetTestResult(pluginName, i18n.T("plugin.test.captcha_failed", map[string]interface{}{"Error": reqErr}))
			return
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36")

		httpClient := &http.Client{}
		resp, err := httpClient.Do(req)
		if err != nil {
			pm.SetTestResult(pluginName, i18n.T("plugin.test.captcha_failed", map[string]interface{}{"Error": err}))
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			pm.SetTestResult(pluginName, i18n.T("plugin.test.captcha_failed", map[string]interface{}{"Error": err}))
			return
		}

		var r api.MainApiDataRoot[gcaptcha.RegisterVoucherResponse]
		err = json.Unmarshal(body, &r)
		if err != nil {
			pm.SetTestResult(pluginName, i18n.T("plugin.test.parse_failed", map[string]interface{}{"Error": err, "Resp": string(body)}))
			return
		}

		if r.Code != 0 {
			pm.SetTestResult(pluginName, i18n.T("plugin.test.api_error", map[string]interface{}{"Resp": string(body)}))
			return
		}

		gt := r.Data.Geetest.Gt
		challenge := r.Data.Geetest.Challenge

		start := time.Now()
		_, err = solverFunc(gt, challenge)
		elapsed := time.Since(start)

		if err != nil {
			pm.SetTestResult(pluginName, i18n.T("plugin.test.failed", map[string]interface{}{"Elapsed": elapsed, "Error": err}))
		} else {
			pm.SetTestResult(pluginName, i18n.T("plugin.test.success", map[string]interface{}{"Elapsed": elapsed}))
		}
	}()
}

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
		rOut, wOut, pipeOutErr := os.Pipe()
		rErr, wErr, pipeErrErr := os.Pipe()
		if pipeOutErr != nil || pipeErrErr != nil {
			log.Printf("[main] failed to create stdout/stderr pipes: out=%v err=%v", pipeOutErr, pipeErrErr)
		} else {
			os.Stdout = wOut
			os.Stderr = wErr
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _ = io.Copy(tw, rOut)
			}()
			go func() {
				defer wg.Done()
				_, _ = io.Copy(tw, rErr)
			}()
			// On exit, close writers (so the io.Copy goroutines can drain
			// remaining buffered data) and wait for them to finish.
			defer func() {
				_ = wOut.Close()
				_ = wErr.Close()
				wg.Wait()
			}()
		}
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
	clusterRepository, err := clusterstorage.Open("data/employer.db")
	if err != nil {
		panic("Failed to open cluster database:" + err.Error())
	}
	if err = clusterRepository.MigrateLegacy(context.Background(), store); err != nil {
		panic("Failed to migrate legacy tickets:" + err.Error())
	}
	clusterSvc := cluster_service.NewClusterService(clusterRepository)

	// Restore saved locale or leave empty for first-startup detection
	if store.Locale != "" {
		i18n.SetLocale(store.Locale)
	}
	jar := cookiejar.New(&cookiejar.Options{
		DefaultCookies: store.Cookies,
	})
	c, err := biliutils.NewBiliClientWithCookiejar(jar)
	if err != nil {
		panic(err)
	}
	clusterSvc.SetCatalogClient(c)

	// Wire up cookie persistence: called from frontend after login & on exit
	c.SetCookieSaveCallback(func() {
		store.Cookies = jar.AllPersistentEntries()
		store.RefreshToken = c.GetRefreshToken()
		if saveErr := store.Save(); saveErr != nil {
			log.Printf("[main] Failed to persist cookies: %v", saveErr)
		}
		if syncErr := clusterSvc.SyncMainAccount(); syncErr != nil {
			log.Printf("[main] Failed to sync main account into pool: %v", syncErr)
		}
	})

	// Restore refresh token from previous session
	c.SetRefreshToken(store.RefreshToken)
	if syncErr := clusterSvc.SyncMainAccount(); syncErr != nil {
		log.Printf("[main] Main account is not available for pool sync: %v", syncErr)
	}

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
	clusterSvc.SetNotifier(func(message string) { notifier.Notify(message) })
	if err := clusterSvc.Start(context.Background()); err != nil {
		log.Fatalf("[main] Failed to start cluster service: %v", err)
	}

	// Scheduler service — BWS (Bilibili World) reservations and
	// notification‑channel management.
	schedSvc := scheduler.NewSchedulerService(c, logBroker, store.BWSData, notifier, store.NotifyChData, store)

	// App instance for frontend verification & misc utilities
	app := NewAppWithClientAndStore(c, store)

	defer func() {
		clusterSvc.Close()
		c.PersistCookies()
		logBroker.FlushLogs()
	}()

	// Recover persisted BWS reservations on startup.
	schedSvc.ReloadBWSTasks()

	// Keep tickets persisted on change
	store.TicketData.SetChangeCallback(func(_ configuration.TicketEntry) {
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
		solver, dispenseErr := captcha.Dispense(pluginManager.GetClient("captcha-plugin"))
		if dispenseErr != nil {
			log.Printf("[main] Failed to dispense captcha plugin: %v", dispenseErr)
		} else {
			solverFunc = func(gt string, challenge string) (string, error) {
				id, err := solver.Create(gt, challenge)
				if err != nil {
					return "", fmt.Errorf("Create error: %w", err)
				}
				defer func() { _ = solver.Destroy(id) }()

				cs, err := solver.GetCS(id, "")
				if err != nil {
					return "", fmt.Errorf("GetCS error: %w", err)
				}
				_ = cs

				t, err := solver.GetType(id, "")
				if err != nil {
					return "", fmt.Errorf("GetType error: %w", err)
				}
				args, err := solver.GetNewCSArgs(id)
				if err != nil {
					return "", fmt.Errorf("GetNewCSArgs error: %w", err)
				}
				before := time.Now()
				key, err := solver.CalculateKey(id, args)
				if err != nil {
					return "", fmt.Errorf("CalculateKey error: %w", err)
				}
				w, err := solver.GenerateW(id, key, args)
				if err != nil {
					return "", fmt.Errorf("GenerateW error: %w", err)
				}
				if t == captcha.CaptchaType_CLICK {
					use := time.Since(before)
					if use < 2*time.Second {
						time.Sleep(2*time.Second - use)
					}
				}
				return solver.Verify(id, w)
			}
			// Wire the solver into BiliClient so voucher errors are auto-resolved.
			c.SetCaptchaSolver(solverFunc)
			log.Printf("[main] Captcha solver installed — vouchers will be auto-resolved")

			// Test the captcha plugin with a generic web captcha
			testCaptchaPlugin(solverFunc, pluginManager, "captcha-plugin")
		}
	}

	// Create Wails v3 application.
	wailsApp := application.New(application.Options{
		Name: "bilibili-ticket-golang",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})
	logBroker.SetApp(wailsApp)
	schedSvc.SetApp(wailsApp)
	app.SetApp(wailsApp)
	clusterSvc.SetApp(wailsApp)

	// Register all services exposed to the frontend as Wails v3 bindings.
	wailsApp.RegisterService(application.NewService(app))
	wailsApp.RegisterService(application.NewService(clusterSvc))
	wailsApp.RegisterService(application.NewService(c))
	wailsApp.RegisterService(application.NewService(logBroker))
	wailsApp.RegisterService(application.NewService(schedSvc))
	wailsApp.RegisterService(application.NewService(pluginManager))

	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "bilibili-ticket-golang",
		Width:            1024,
		Height:           768,
		BackgroundColour: application.RGBA{Red: 27, Green: 38, Blue: 54, Alpha: 255},
		URL:              "/",
	})

	if err = wailsApp.Run(); err != nil {
		log.Printf("[main] Error: %v", err)
	}
}

// timestampWriter writes output to an io.Writer, prepending a timestamp
// to each line and syncing (if the underlying writer is an *os.File) after
// every newline so that crash logs are never lost.
type timestampWriter struct {
	w   io.Writer
	mu  sync.Mutex
	buf bytes.Buffer
}

const timeLayout = "2006-01-02 15:04:05 "

func (t *timestampWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	consumed := 0
	for _, b := range p {
		consumed++
		if b == '\n' {
			line := append([]byte(time.Now().Format(timeLayout)), t.buf.Bytes()...)
			line = append(line, '\n')
			if _, err := t.w.Write(line); err != nil {
				return consumed, err
			}
			t.buf.Reset()
			if f, ok := t.w.(*os.File); ok {
				f.Sync()
			}
		} else {
			t.buf.WriteByte(b)
		}
	}
	return len(p), nil
}

// Flush writes any remaining buffered partial line to the underlying writer.
func (t *timestampWriter) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buf.Len() == 0 {
		return nil
	}
	line := append([]byte(time.Now().Format(timeLayout)), t.buf.Bytes()...)
	line = append(line, '\n')
	_, err := t.w.Write(line)
	t.buf.Reset()
	return err
}
