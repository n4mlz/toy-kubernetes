package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"toy-kubernetes/cni"
	"toy-kubernetes/config"
	containerRuntime "toy-kubernetes/cri/runtime"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("[runtime] ")
	child := flag.Bool("container-child", false, "run as a container process")
	sandboxChild := flag.Bool("sandbox-child", false, "run as a Pod sandbox process")
	rootfs := flag.String("rootfs", "", "container rootfs")
	readyFD := flag.Int("network-ready-fd", -1, "file descriptor released after CNI setup")
	networkFD := flag.Int("network-fd", -1, "Pod sandbox network namespace file descriptor")
	socket := flag.String("socket", "", "CRI Unix socket")
	bundleDir := flag.String("bundle-dir", config.BundleDir, "bundle directory")
	bridge := flag.String("bridge", "", "Pod network bridge")
	podCIDR := flag.String("pod-cidr", "", "Pod network CIDR")
	gateway := flag.String("gateway", "", "Pod network gateway")
	flag.Parse()

	if *child {
		if *readyFD >= 0 {
			if err := containerRuntime.WaitForNetwork(*readyFD); err != nil {
				log.Fatal(err)
			}
		}
		if *networkFD >= 0 {
			if err := containerRuntime.JoinNetworkNamespaceFD(*networkFD); err != nil {
				log.Fatal(err)
			}
		}
		if err := containerRuntime.RunContainerChild(*rootfs, flag.Args()); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *sandboxChild {
		if *readyFD >= 0 {
			if err := containerRuntime.WaitForNetwork(*readyFD); err != nil {
				log.Fatal(err)
			}
		}
		termination := make(chan os.Signal, 1)
		signal.Notify(termination, os.Interrupt, syscall.SIGTERM)
		<-termination
		return
	}
	if *socket == "" {
		log.Fatal("socket is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtime := containerRuntime.NewRuntime(*socket, *bundleDir)
	if *bridge != "" || *podCIDR != "" || *gateway != "" {
		if *bridge == "" || *podCIDR == "" || *gateway == "" {
			log.Fatal("bridge, pod-cidr and gateway must be provided together")
		}
		network, err := cni.NewLinuxCNI(*bridge, *podCIDR, *gateway)
		if err != nil {
			log.Fatal(err)
		}
		runtime = containerRuntime.NewRuntimeWithNetwork(*socket, *bundleDir, network)
	}
	if err := runtime.Serve(ctx); err != nil {
		log.Fatal(err)
	}
	runtime.StopAll()
}
