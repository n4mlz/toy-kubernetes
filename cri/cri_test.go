package cri

import (
	"context"
	"testing"

	"toy-kubernetes/api"
)

func TestFakeCRIRunsListsAndStopsAContainer(t *testing.T) {
	runtime := NewFakeCRI()
	pod := api.Pod{ObjectMeta: api.ObjectMeta{Name: "nginx"}}

	container, err := runtime.Run(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if container.PodName != "nginx" || container.State != Running {
		t.Fatalf("Run should create a running container: %#v", container)
	}

	containers, err := runtime.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].PodName != "nginx" {
		t.Fatalf("List should expose the running container: %#v", containers)
	}

	if err := runtime.Stop(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	containers, _ = runtime.List(context.Background())
	if containers[0].State != Stopped {
		t.Fatalf("Stop should leave the container stopped: %#v", containers[0])
	}
}
