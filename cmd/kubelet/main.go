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
	log.SetFlags(0)
	log.SetPrefix("[kubelet] ")
	flags := flag.NewFlagSet("kubelet", flag.ContinueOnError)
	nodeName := flags.String("node", "", "node name")
	manifestDir := flags.String("manifests", "", "static Pod manifest directory")
	podCIDR := flags.String("pod-cidr", "", "Pod CIDR assigned to this Node")
	socket := flags.String("socket", "", "CRI socket path")
	apiServer := flags.String("api-server", "", "API server URL")
	registerNode := flags.Bool("register-node", true, "register this node in the API server")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *nodeName == "" || *socket == "" {
		return errors.New("node and socket are required")
	}

	var apiClient *apiserver.Client
	if *apiServer != "" {
		apiClient = apiserver.NewClient(*apiServer)
	}

	worker := kubelet.NewWithManifestDirAndPodCIDR(apiClient, cri.NewClient(*socket), *nodeName, *manifestDir, *podCIDR)
	worker.SetRegisterNode(*registerNode)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
		log.Printf("run: %v", err)
	}
	return nil
}
