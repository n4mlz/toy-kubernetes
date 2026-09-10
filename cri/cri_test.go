package cri

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"toy-kubernetes/api"
)

func TestClientUsesTheRuntimeSocketProtocol(t *testing.T) {
	listener, err := net.Listen("unix", t.TempDir()+"/runtime.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; index < 7; index++ {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			request, err := bufio.NewReader(connection).ReadString('\n')
			if err != nil {
				connection.Close()
				return
			}
			switch {
			case strings.Contains(request, `"op":"list"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true,"containers":[{"id":"pid-1","pod":"nginx","state":"Running"}]}`)
			case strings.Contains(request, `"op":"list-sandboxes"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true,"sandboxes":[{"id":"sandbox-1","pod":"nginx","source":"workload","state":"Running"}]}`)
			case strings.Contains(request, `"op":"run-pod-sandbox"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true,"sandboxID":"sandbox-1","pod":"nginx","source":"workload","state":"Running"}`)
			case strings.Contains(request, `"op":"run-in-sandbox"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true,"id":"pid-1","pod":"nginx","sandboxID":"sandbox-1","state":"Running"}`)
			case strings.Contains(request, `"op":"inspect"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true,"id":"pid-1","pod":"nginx","state":"Running"}`)
			case strings.Contains(request, `"op":"stop"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true}`)
			case strings.Contains(request, `"op":"stop-pod-sandbox"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true}`)
			}
			connection.Close()
		}
	}()

	client := NewClient(listener.Addr().String())
	ctx := context.Background()
	pod := api.Pod{ObjectMeta: api.ObjectMeta{Name: "nginx"}, Spec: api.PodSpec{Containers: []api.Container{{Image: "nginx"}}}}
	containers, err := client.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].PodName != "nginx" {
		t.Fatalf("List should decode the runtime response: %#v", containers)
	}
	if container, err := client.Inspect(ctx, "nginx"); err != nil || container.ID != "pid-1" {
		t.Fatalf("Inspect should return the selected container: %#v, %v", container, err)
	}
	sandboxes, err := client.ListSandboxes(ctx)
	if err != nil || len(sandboxes) != 1 || sandboxes[0].Source != Workload {
		t.Fatalf("ListSandboxes should decode sandbox metadata: %#v, %v", sandboxes, err)
	}
	sandbox, err := client.RunPodSandbox(ctx, pod, Workload)
	if err != nil || sandbox.ID != "sandbox-1" {
		t.Fatalf("RunPodSandbox should decode the sandbox: %#v, %v", sandbox, err)
	}
	if _, err := client.RunInSandbox(ctx, pod, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(ctx, "nginx"); err != nil {
		t.Fatal(err)
	}
	if err := client.StopPodSandbox(ctx, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	<-done
}
