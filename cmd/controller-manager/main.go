package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"toy-kubernetes/apiserver"
	controllermanager "toy-kubernetes/controller-manager"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("kube-controller-manager", flag.ContinueOnError)
	apiServer := flags.String("api-server", "", "API server URL")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *apiServer == "" {
		return fmt.Errorf("api-server is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return controllermanager.Run(ctx, apiserver.NewClient(*apiServer))
}
