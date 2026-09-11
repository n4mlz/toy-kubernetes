package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"toy-kubernetes/apiserver"
	"toy-kubernetes/scheduler"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	log.SetFlags(0)
	log.SetPrefix("[kube-scheduler] ")
	flags := flag.NewFlagSet("kube-scheduler", flag.ContinueOnError)
	apiServer := flags.String("api-server", "", "API server URL")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *apiServer == "" {
		return fmt.Errorf("api-server is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return scheduler.NewScheduler(apiserver.NewClient(*apiServer)).Run(ctx)
}
