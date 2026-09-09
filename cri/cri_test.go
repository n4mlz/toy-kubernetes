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
		for index := 0; index < 4; index++ {
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
			case strings.Contains(request, `"op":"run"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true,"id":"pid-1","pod":"nginx","state":"Running"}`)
			case strings.Contains(request, `"op":"list"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true,"containers":[{"id":"pid-1","pod":"nginx","state":"Running"}]}`)
			case strings.Contains(request, `"op":"inspect"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true,"id":"pid-1","pod":"nginx","state":"Running"}`)
			case strings.Contains(request, `"op":"stop"`):
				_, _ = fmt.Fprintln(connection, `{"ok":true}`)
			}
			connection.Close()
		}
	}()

	client := NewClient(listener.Addr().String())
	ctx := context.Background()
	if _, err := client.Run(ctx, api.Pod{ObjectMeta: api.ObjectMeta{Name: "nginx"}, Spec: api.PodSpec{Containers: []api.Container{{Image: "nginx"}}}}); err != nil {
		t.Fatal(err)
	}
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
	if err := client.Stop(ctx, "nginx"); err != nil {
		t.Fatal(err)
	}
	<-done
}
