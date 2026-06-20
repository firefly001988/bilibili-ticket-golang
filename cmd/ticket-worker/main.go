package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	biliclock "bilibili-ticket-golang/biliutils/clock"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
	"bilibili-ticket-golang/cluster/worker"
	"bilibili-ticket-golang/plugins"
	"bilibili-ticket-golang/plugins/captcha"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: ticket-worker <run|serve>")
	}
	switch os.Args[1] {
	case "run":
		run(os.Args[2:])
	case "serve":
		serve(os.Args[2:])
	default:
		fatal("unknown command %q", os.Args[1])
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := fs.String("config", "", "worker config JSON")
	_ = fs.Parse(args)
	if *path == "" {
		fatal("--config is required")
	}
	b, err := os.ReadFile(*path)
	if err != nil {
		fatal("read config: %v", err)
	}
	var config worker.Config
	if err := json.Unmarshal(b, &config); err != nil {
		fatal("decode config: %v", err)
	}
	config.CalibrateClock = true
	factory, cleanup, err := workerFactory(&config)
	if err != nil {
		fatal("initialize captcha plugin: %v", err)
	}
	defer cleanup()
	server, err := worker.NewServer(config, factory)
	if err != nil {
		fatal("initialize worker: %v", err)
	}
	if err := server.ListenAndServe(); err != nil {
		fatal("serve worker: %v", err)
	}
}

func workerFactory(config *worker.Config) (worker.BackendFactory, func(), error) {
	if config.CaptchaPlugin == "" {
		return nil, func() {}, nil
	}
	dir := config.PluginDir
	if dir == "" {
		dir = "plugins"
	}
	manager := plugins.NewPluginManager(dir)
	if err := manager.LoadPlugin(config.CaptchaPlugin); err != nil {
		return nil, func() {}, err
	}
	solver, err := captcha.Dispense(manager.GetClient(config.CaptchaPlugin))
	if err != nil {
		manager.UnloadAll()
		return nil, func() {}, err
	}
	solverFunc := func(gt, challenge string) (string, error) {
		id, err := solver.Create(gt, challenge)
		if err != nil {
			return "", err
		}
		defer solver.Destroy(id)
		if _, err = solver.GetCS(id, ""); err != nil {
			return "", err
		}
		captchaType, err := solver.GetType(id, "")
		if err != nil {
			return "", err
		}
		args, err := solver.GetNewCSArgs(id)
		if err != nil {
			return "", err
		}
		started := time.Now()
		key, err := solver.CalculateKey(id, args)
		if err != nil {
			return "", err
		}
		w, err := solver.GenerateW(id, key, args)
		if err != nil {
			return "", err
		}
		if captchaType == captcha.CaptchaType_CLICK {
			if elapsed := time.Since(started); elapsed < 2*time.Second {
				time.Sleep(2*time.Second - elapsed)
			}
		}
		return solver.Verify(id, w)
	}
	if info, versionErr := manager.GetVersion(config.CaptchaPlugin); versionErr == nil {
		config.PluginVersion = info.Version + "+" + info.GitCommit
	}
	return func(spec domain.ExecutionSpec) (executor.Backend, error) {
		return executor.NewBilibiliBackendWithSolver(spec.Credentials, solverFunc)
	}, manager.UnloadAll, nil
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	path := fs.String("task", "", "execution task JSON")
	pluginDir := fs.String("plugin-dir", "plugins", "captcha plugin directory")
	captchaPlugin := fs.String("captcha-plugin", "", "captcha plugin executable name")
	_ = fs.Parse(args)
	if *path == "" {
		fatal("--task is required")
	}
	b, err := os.ReadFile(*path)
	if err != nil {
		fatal("read task: %v", err)
	}
	var spec domain.ExecutionSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fatal("decode task: %v", err)
	}
	pluginConfig := worker.Config{PluginDir: *pluginDir, CaptchaPlugin: *captchaPlugin}
	factory, cleanup, err := workerFactory(&pluginConfig)
	if err != nil {
		fatal("initialize captcha plugin: %v", err)
	}
	defer cleanup()
	var backend executor.Backend
	if factory == nil {
		backend, err = executor.NewBilibiliBackend(spec.Credentials)
	} else {
		backend, err = factory(spec)
	}
	if err != nil {
		fatal("initialize Bilibili client: %v", err)
	}
	var executionClock executor.Clock
	if offset, clockErr := biliclock.GetBilibiliClockOffset(); clockErr == nil {
		executionClock = executor.OffsetClock{Offset: offset}
	}
	result := (executor.Engine{Backend: backend, Clock: executionClock}).Run(context.Background(), spec)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fatal("encode result: %v", err)
	}
	if !result.Success {
		os.Exit(1)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
