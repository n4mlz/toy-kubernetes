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

	"toy-kubernetes/bootstrap"
	"toy-kubernetes/config"
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
	runtimePath := flags.String("runtime", config.RuntimeBinary, "container runtime path")
	kubeletPath := flags.String("kubelet", config.KubeletBinary, "kubelet path")
	kubeProxyPath := flags.String("kube-proxy", config.KubeProxyBinary, "kube-proxy path")
	socketPath := flags.String("socket", "", "runtime socket path")
	bundleDir := flags.String("bundle-dir", config.BundleDir, "runtime bundle directory")
	nodeIndex := flags.Int("node-index", -1, "node network index")
	nodeCount := flags.Int("node-count", 0, "number of nodes in the underlay")
	manifestDir := flags.String("manifests", "", "static Pod manifest directory")
	apiServer := flags.String("api-server", "", "API server URL")
	logDir := flags.String("log-dir", "", "log directory")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *nodeName == "" || *nodeIndex < 0 || *nodeCount < 1 || *socketPath == "" || *manifestDir == "" || *logDir == "" || *kubeProxyPath == "" {
		return errors.New("node, node-index, node-count, socket, manifests, log-dir and kube-proxy are required")
	}
	podCIDR, gateway, err := bootstrap.NodeNetwork(*nodeIndex)
	if err != nil {
		return fmt.Errorf("calculate node network: %w", err)
	}

	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		return fmt.Errorf("create node log directory: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := node.ConfigureNetwork(ctx, *nodeIndex, *nodeCount); err != nil {
		return err
	}

	units := []node.Unit{
		{
			Name: "runtime",
			Path: *runtimePath,
			Args: []string{
				"-socket", *socketPath,
				"-bundle-dir", filepath.Clean(*bundleDir),
				"-bridge", config.BridgeName,
				"-pod-cidr", podCIDR.String(),
				"-gateway", gateway.String(),
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
	if *apiServer != "" && *nodeIndex > 0 {
		// 本家の kube-proxy は kube-system の DaemonSet から各 Node に配置される。
		// この toy 実装では DaemonSet がまだないため、node supervisor が代わりに起動する。
		units = append(units, node.Unit{
			Name:    "kube-proxy",
			Path:    *kubeProxyPath,
			Args:    []string{"-api-server", *apiServer, "-table", config.KubeProxyTable},
			LogPath: filepath.Join(*logDir, "kube-proxy.log"),
		})
	}
	supervisor := node.NewSupervisor(units)
	return supervisor.Run(ctx)
}
