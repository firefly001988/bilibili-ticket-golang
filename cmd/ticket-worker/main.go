package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
	"bilibili-ticket-golang/cluster/worker"
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
	server, err := worker.NewServer(config, nil)
	if err != nil {
		fatal("initialize worker: %v", err)
	}
	if err := server.ListenAndServe(); err != nil {
		fatal("serve worker: %v", err)
	}
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	path := fs.String("task", "", "execution task JSON")
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
	backend, err := executor.NewBilibiliBackend(spec.Credentials)
	if err != nil {
		fatal("initialize Bilibili client: %v", err)
	}
	result := (executor.Engine{Backend: backend}).Run(context.Background(), spec)
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
