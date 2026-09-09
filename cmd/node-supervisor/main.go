package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"toy-kubernetes/node"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("node-supervisor", flag.ContinueOnError)
	nodeName := flags.String("node", "", "node name")
	runtimePath := flags.String("runtime", "/tmp/runtime", "container runtime path")
	socketPath := flags.String("socket", "", "runtime socket path")
	bundleDir := flags.String("bundle-dir", "bundles", "runtime bundle directory")
	logDir := flags.String("log-dir", "", "log directory")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *nodeName == "" || *socketPath == "" || *logDir == "" {
		return errors.New("node, socket and log-dir are required")
	}

	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		return fmt.Errorf("create node log directory: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	supervisor := node.NewSupervisor([]node.Unit{{
		Name:    "runtime",
		Path:    *runtimePath,
		Args:    []string{"-socket", *socketPath, "-bundle-dir", filepath.Clean(*bundleDir)},
		LogPath: filepath.Join(*logDir, "runtime.log"),
	}})
	return supervisor.Run(ctx)
}
