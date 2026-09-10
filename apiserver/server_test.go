package apiserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"toy-kubernetes/api"
	"toy-kubernetes/etcd"
)

func TestServerCreatesAndReturnsValidPodsAndRejectsInvalidPods(t *testing.T) {
	server := newTestServer(t)
	client := NewClient(server.URL)

	created, err := client.Pods().Create(context.Background(), api.Pod{
		TypeMeta:   api.TypeMeta{APIVersion: api.APIVersionV1, Kind: "Pod"},
		ObjectMeta: api.ObjectMeta{Name: "nginx"},
		Spec:       api.PodSpec{Containers: []api.Container{{Name: "nginx", Image: "nginx"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if created.Name != "nginx" || created.ResourceVersion == 0 {
		t.Fatalf("created Pod should have its name and resource version: %#v", created)
	}

	fetched, err := client.Pods().Get(context.Background(), "nginx")
	if err != nil {
		t.Fatal(err)
	}

	if fetched.Name != created.Name || fetched.ResourceVersion != created.ResourceVersion {
		t.Fatalf("get should return the created Pod: %#v", fetched)
	}

	invalidRequest, err := http.NewRequest(http.MethodPost, server.URL+"/pods", strings.NewReader(`{"metadata":{"name":"empty"},"spec":{"containers":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse, err := http.DefaultClient.Do(invalidRequest)
	if err != nil {
		t.Fatal(err)
	}

	defer invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid Pod should be rejected with 400, got %d", invalidResponse.StatusCode)
	}
}

func TestClientListAndWatchObserveTheSamePod(t *testing.T) {
	server := newTestServer(t)
	client := NewClient(server.URL)

	list, err := client.Pods().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 || list.ResourceVersion == 0 {
		t.Fatalf("empty list should include its resource version: %#v", list)
	}

	watchContext, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()
	events, err := client.Pods().Watch(watchContext, list.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Pods().Create(context.Background(), api.Pod{
		ObjectMeta: api.ObjectMeta{Name: "nginx"},
		Spec:       api.PodSpec{Containers: []api.Container{{Name: "nginx", Image: "nginx"}}},
	}); err != nil {
		t.Fatal(err)
	}

	var event WatchEvent[api.Pod]
	select {
	case event = <-events:
	case <-time.After(time.Second):
		t.Fatal("watch should publish the created Pod")
	}

	if event.Err != nil || event.Type != string(etcd.Added) || event.Object.Name != "nginx" || event.Object.ResourceVersion == 0 {
		t.Fatalf("watch should publish an ADDED event for nginx: %#v", event)
	}

	updatedList, err := client.Pods().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(updatedList.Items) != 1 || updatedList.Items[0].Name != event.Object.Name {
		t.Fatalf("list should observe the Pod published by watch: %#v", updatedList)
	}
}

func TestServerAssignsDistinctClusterIPsToServices(t *testing.T) {
	server := newTestServer(t)
	client := NewClient(server.URL)

	first, err := client.Services().Create(context.Background(), api.Service{ObjectMeta: api.ObjectMeta{Name: "web"}, Spec: api.ServiceSpec{Port: 80, TargetPort: 80}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Services().Create(context.Background(), api.Service{ObjectMeta: api.ObjectMeta{Name: "api"}, Spec: api.ServiceSpec{Port: 80, TargetPort: 80}})
	if err != nil {
		t.Fatal(err)
	}

	if first.Spec.ClusterIP == "" || first.Spec.ClusterIP == second.Spec.ClusterIP {
		t.Fatalf("services should receive distinct ClusterIPs: %q, %q", first.Spec.ClusterIP, second.Spec.ClusterIP)
	}
}

func TestServerAssignsNodePortToNodePortService(t *testing.T) {
	server := newTestServer(t)
	client := NewClient(server.URL)

	created, err := client.Services().Create(context.Background(), api.Service{
		ObjectMeta: api.ObjectMeta{Name: "web"},
		Spec:       api.ServiceSpec{Type: api.ServiceNodePort, Port: 80, TargetPort: 80},
	})
	if err != nil {
		t.Fatal(err)
	}

	if created.Spec.Type != api.ServiceNodePort || created.Spec.ClusterIP == "" || created.Spec.NodePort < 30000 || created.Spec.NodePort > 32767 {
		t.Fatalf("NodePort Service should receive ClusterIP and NodePort: %#v", created.Spec)
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	clientURL := freeURL(t)
	peerURL := freeURL(t)
	etcdCommand := exec.Command("etcd",
		"--name", "toy-kubernetes-apiserver-test",
		"--data-dir", t.TempDir(),
		"--listen-client-urls", clientURL,
		"--advertise-client-urls", clientURL,
		"--listen-peer-urls", peerURL,
		"--initial-advertise-peer-urls", peerURL,
		"--initial-cluster", "toy-kubernetes-apiserver-test="+peerURL,
		"--initial-cluster-state", "new",
	)
	etcdCommand.Stdout = io.Discard
	etcdCommand.Stderr = io.Discard

	if err := etcdCommand.Start(); err != nil {
		t.Fatalf("start test etcd: %v", err)
	}

	etcdClient, err := clientv3.New(clientv3.Config{Endpoints: []string{clientURL}, DialTimeout: 2 * time.Second})
	if err != nil {
		_ = etcdCommand.Process.Kill()
		_ = etcdCommand.Wait()
		t.Fatalf("create etcd client: %v", err)
	}

	deadline, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for deadline.Err() == nil {
		if _, err := etcdClient.Get(deadline, "health"); err == nil {
			httpServer := httptest.NewServer(NewServer(etcd.NewEtcdClient(etcdClient)).Handler())

			t.Cleanup(func() {
				httpServer.Close()
				_ = etcdClient.Close()
				_ = etcdCommand.Process.Kill()
				_ = etcdCommand.Wait()
			})
			return httpServer
		}
		time.Sleep(50 * time.Millisecond)
	}

	_ = etcdClient.Close()
	_ = etcdCommand.Process.Kill()
	_ = etcdCommand.Wait()
	t.Fatalf("test etcd did not become ready: %v", deadline.Err())
	return nil
}

func freeURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + address
}
