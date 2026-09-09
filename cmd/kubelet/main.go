package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"toy-kubernetes/apiserver"
	"toy-kubernetes/cri"
	"toy-kubernetes/kubelet"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("kubelet", flag.ContinueOnError)
	nodeName := flags.String("node", "", "node name")
	manifestDir := flags.String("manifests", "", "static Pod manifest directory")
	socket := flags.String("socket", "", "CRI socket path")
	apiServer := flags.String("api-server", "", "API server URL")
	interval := flags.Duration("interval", time.Second, "reconcile interval")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *nodeName == "" || *manifestDir == "" || *socket == "" {
		return errors.New("node, manifests and socket are required")
	}

	var apiClient *apiserver.Client
	if *apiServer != "" {
		apiClient = apiserver.NewClient(*apiServer)
	}

	worker := kubelet.NewWithManifestDir(apiClient, cri.NewClient(*socket), *nodeName, *manifestDir)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		if err := worker.Reconcile(ctx); err != nil {
			log.Printf("reconcile: %v", err)
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
