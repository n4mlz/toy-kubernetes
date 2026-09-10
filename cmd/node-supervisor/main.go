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
	kubeletPath := flags.String("kubelet", "/tmp/kubelet", "kubelet path")
	socketPath := flags.String("socket", "", "runtime socket path")
	bundleDir := flags.String("bundle-dir", "bundles", "runtime bundle directory")
	bridge := flags.String("bridge", "", "Pod network bridge")
	podCIDR := flags.String("pod-cidr", "", "Pod network CIDR")
	gateway := flags.String("gateway", "", "Pod network gateway")
	manifestDir := flags.String("manifests", "", "static Pod manifest directory")
	apiServer := flags.String("api-server", "", "API server URL")
	logDir := flags.String("log-dir", "", "log directory")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *nodeName == "" || *socketPath == "" || *manifestDir == "" || *logDir == "" {
		return errors.New("node, socket, manifests and log-dir are required")
	}

	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		return fmt.Errorf("create node log directory: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	units := []node.Unit{
		{
			Name: "runtime",
			Path: *runtimePath,
			Args: []string{
				"-socket", *socketPath,
				"-bundle-dir", filepath.Clean(*bundleDir),
				"-bridge", *bridge,
				"-pod-cidr", *podCIDR,
				"-gateway", *gateway,
			},
			LogPath: filepath.Join(*logDir, "runtime.log"),
		},
		{
			Name:    "kubelet",
			Path:    *kubeletPath,
			Args:    []string{"-node", *nodeName, "-manifests", *manifestDir, "-socket", *socketPath, "-api-server", *apiServer},
			LogPath: filepath.Join(*logDir, "kubelet.log"),
		},
	}
	supervisor := node.NewSupervisor(units)
	return supervisor.Run(ctx)
}
