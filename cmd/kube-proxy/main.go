package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"toy-kubernetes/apiserver"
	"toy-kubernetes/config"
	kubeproxy "toy-kubernetes/kube-proxy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("kube-proxy", flag.ContinueOnError)
	apiServer := flags.String("api-server", "", "API server URL")
	table := flags.String("table", config.KubeProxyTable, "nftables table name")
	interval := flags.Duration("interval", time.Second, "reconcile interval")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *apiServer == "" {
		return errors.New("api-server is required")
	}

	proxy := kubeproxy.New(apiserver.NewClient(*apiServer), kubeproxy.NewNftablesForwarder(*table))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		if err := proxy.Reconcile(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
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
