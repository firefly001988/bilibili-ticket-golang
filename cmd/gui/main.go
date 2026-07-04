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
	"bilibili-ticket-golang/lib/global"
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

	gc "bilibili-ticket-golang/captcha-solver"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[scheduler.LogEntry]("ticket:log")
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
		fault := global.NewFault("加载配置文件 data/store.bin", err, "删除 data/store.bin 以重置配置，或检查文件权限")
		log.Fatalf("[main] %v", fault)
	}
	clusterRepository, err := clusterstorage.Open("data/employer.db")
	if err != nil {
		fault := global.NewFault("打开集群数据库 data/employer.db", err, "检查文件权限；若存在 data/employer.db-wal 残留文件，删除后重试")
		log.Fatalf("[main] %v", fault)
	}
	if err = clusterRepository.MigrateLegacy(context.Background(), store); err != nil {
		fault := global.NewFault("迁移旧版数据到集群数据库", err, "数据库可能已损坏，尝试删除 data/employer.db 后重新配置")
		log.Fatalf("[main] %v", fault)
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
		fault := global.NewFault("创建 Bilibili 客户端", err, "检查网络连接和 Cookie 有效性")
		log.Fatalf("[main] %v", fault)
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
		// The Start method already wraps its internal errors with Fault; if
		// the error chain already contains a Fault, it will be rendered with
		// file:line info via the custom MarshalError.
		log.Fatalf("[main] 启动集群服务失败: %v", err)
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
		return gc.Solve(gt, challenge)
	}

	// 初始化 captcha DLL
	os.MkdirAll("./libs", 0755)
	if err := gc.Init("./libs"); err != nil {
		log.Printf("[main] captcha DLL init: %v", err)
	} else {
		v, _ := gc.Version()
		log.Printf("[main] captcha DLL loaded (version=%s, commit=%s)", v.Version, v.GitCommit)

		// 预热验证码模块，加载 ONNX 模型，避免首次请求耗时过长
		if err := gc.Warmup(); err != nil {
			log.Printf("[main] captcha warmup failed (non-fatal): %v", err)
		}

		// Wire the solver into BiliClient so voucher errors are auto-resolved.
		c.SetCaptchaSolver(solverFunc)
		app.SetCaptchaSolver(solverFunc)
		clusterSvc.SetLocalWorkerSolver(solverFunc, makeCaptchaTester(solverFunc))
		log.Printf("[main] captcha solver installed — vouchers will be auto-resolved")
	}

	// Create Wails v3 application.
	wailsApp := application.New(application.Options{
		Name: "bilibili-ticket-golang",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		// Custom error marshalling: when a bound method returns an error, the
		// Wails CallError.Cause field will contain structured JSON with the
		// source file, line number, operation name, error message and a
		// human-readable hint — instead of the default opaque "0".
		MarshalError: global.MarshalError,
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

// makeCaptchaTester wraps the solver into a CaptchaTester that fetches a live
// captcha from Bilibili and tests the solver. Used by the local worker manager.
func makeCaptchaTester(solver func(gt, challenge string) (string, error)) func() (elapsed, validate, captchaType string, err error) {
	return func() (elapsed, validate, captchaType string, err error) {
		req, _ := http.NewRequest("GET",
			"https://passport.bilibili.com/x/passport-login/captcha?source=main_web", nil)
		req.Header.Set("User-Agent",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36")

		resp, httpErr := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if httpErr != nil {
			err = fmt.Errorf("HTTP: %w", httpErr)
			return
		}
		defer resp.Body.Close()

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			err = fmt.Errorf("read: %w", readErr)
			return
		}

		var r struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Type    string `json:"type"`
				Geetest struct {
					Gt        string `json:"gt"`
					Challenge string `json:"challenge"`
				} `json:"geetest"`
			} `json:"data"`
		}
		if jsonErr := json.Unmarshal(body, &r); jsonErr != nil {
			err = fmt.Errorf("parse: %w", jsonErr)
			return
		}
		if r.Code != 0 {
			err = fmt.Errorf("API code=%d: %s", r.Code, r.Message)
			return
		}

		captchaType = r.Data.Type
		start := time.Now()
		validate, err = solver(r.Data.Geetest.Gt, r.Data.Geetest.Challenge)
		elapsed = time.Since(start).String()
		return
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
