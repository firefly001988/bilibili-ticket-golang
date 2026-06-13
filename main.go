package main

import (
	"bilibili-ticket-golang/biliutils"
	"bilibili-ticket-golang/biliutils/notify"
	"bilibili-ticket-golang/biliutils/scheduler"
	"bilibili-ticket-golang/models/bili/api"
	gcaptcha "bilibili-ticket-golang/models/bili/captcha"
	"bilibili-ticket-golang/plugins"
	"bilibili-ticket-golang/plugins/captcha"
	"bilibili-ticket-golang/store/configuration"
	"bilibili-ticket-golang/store/cookiejar"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func testCaptchaPlugin(solverFunc func(gt string, challenge string) (string, error), pm *plugins.PluginManager, pluginName string) {
	go func() {
		client := resty.New()
		res, err := client.R().
			SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36").
			Get("https://passport.bilibili.com/x/passport-login/captcha?source=main_web")
		if err != nil {
			pm.SetTestResult(pluginName, fmt.Sprintf("获取验证码失败: %v", err))
			return
		}

		var r api.MainApiDataRoot[gcaptcha.RegisterVoucherResponse]
		err = json.Unmarshal(res.Body(), &r)
		if err != nil {
			pm.SetTestResult(pluginName, fmt.Sprintf("解析验证码响应失败: %v\nResp: %s", err, string(res.Body())))
			return
		}

		if r.Code != 0 {
			pm.SetTestResult(pluginName, fmt.Sprintf("获取验证码返回错误: %s", string(res.Body())))
			return
		}

		gt := r.Data.Geetest.Gt
		challenge := r.Data.Geetest.Challenge

		start := time.Now()
		_, err = solverFunc(gt, challenge)
		elapsed := time.Since(start)

		if err != nil {
			pm.SetTestResult(pluginName, fmt.Sprintf("测试失败 (耗时 %v):\n%v", elapsed, err))
		} else {
			pm.SetTestResult(pluginName, fmt.Sprintf("测试成功 (耗时 %v)", elapsed))
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

	// Auto-restart persisted tasks on launch
	schedSvc.ReloadTickets()
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
	written := len(p)
	for _, b := range p {
		if b == '\n' {
			line := append([]byte(time.Now().Format(timeLayout)), t.buf.Bytes()...)
			line = append(line, '\n')
			if _, err := t.w.Write(line); err != nil {
				return 0, err
			}
			t.buf.Reset()
			if f, ok := t.w.(*os.File); ok {
				f.Sync()
			}
		} else {
			t.buf.WriteByte(b)
		}
	}
	return written, nil
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
