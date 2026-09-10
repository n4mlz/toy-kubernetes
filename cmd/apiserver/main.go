package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"toy-kubernetes/apiserver"
	"toy-kubernetes/etcd"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("kube-apiserver", flag.ContinueOnError)
	listen := flags.String("listen", "0.0.0.0:8080", "HTTP listen address")
	etcdEndpoint := flags.String("etcd-endpoint", "http://127.0.0.1:2379", "etcd client endpoint")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}

	etcdClient, err := clientv3.New(clientv3.Config{Endpoints: []string{*etcdEndpoint}, DialTimeout: 5 * time.Second})
	if err != nil {
		return fmt.Errorf("connect etcd: %w", err)
	}
	defer etcdClient.Close()

	server := &http.Server{Addr: *listen, Handler: apiserver.NewServer(etcd.NewEtcdClient(etcdClient)).Handler()}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve API server: %w", err)
	}
	return nil
}
