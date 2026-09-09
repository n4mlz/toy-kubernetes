package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	containerRuntime "toy-kubernetes/cri/runtime"
)

func main() {
	child := flag.Bool("container-child", false, "run as a container process")
	rootfs := flag.String("rootfs", "", "container rootfs")
	socket := flag.String("socket", containerRuntime.DefaultSocket, "CRI Unix socket")
	bundleDir := flag.String("bundle-dir", containerRuntime.DefaultBundleDir, "bundle directory")
	flag.Parse()

	if *child {
		if err := containerRuntime.RunContainerChild(*rootfs, flag.Args()); err != nil {
			log.Fatal(err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtime := containerRuntime.NewRuntime(*socket, *bundleDir)
	if err := runtime.Serve(ctx); err != nil {
		log.Fatal(err)
	}
	runtime.StopAll()
}
