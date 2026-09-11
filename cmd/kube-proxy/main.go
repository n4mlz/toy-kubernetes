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
	log.SetFlags(0)
	log.SetPrefix("[kube-proxy] ")
	flags := flag.NewFlagSet("kube-proxy", flag.ContinueOnError)
	apiServer := flags.String("api-server", "", "API server URL")
	table := flags.String("table", config.KubeProxyTable, "nftables table name")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *apiServer == "" {
		return errors.New("api-server is required")
	}

	proxy := kubeproxy.New(apiserver.NewClient(*apiServer), kubeproxy.NewNftablesForwarder(*table))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := proxy.Run(ctx); err != nil {
		return err
	}
	return nil
}
