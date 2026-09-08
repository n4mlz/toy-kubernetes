package apiserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	watchRequest, err := http.NewRequestWithContext(watchContext, http.MethodGet, server.URL+"/watch/pods?resourceVersion="+fmt.Sprint(list.ResourceVersion), nil)
	if err != nil {
		t.Fatal(err)
	}
	watchResponse, err := http.DefaultClient.Do(watchRequest)
	if err != nil {
		t.Fatal(err)
	}

	defer watchResponse.Body.Close()
	watchEvents := bufio.NewScanner(watchResponse.Body)

	if _, err := client.Pods().Create(context.Background(), api.Pod{
		ObjectMeta: api.ObjectMeta{Name: "nginx"},
		Spec:       api.PodSpec{Containers: []api.Container{{Name: "nginx", Image: "nginx"}}},
	}); err != nil {
		t.Fatal(err)
	}

	if !watchEvents.Scan() {
		t.Fatalf("watch should publish the created Pod: %v", watchEvents.Err())
	}
	var event struct {
		Type   etcd.EventType `json:"type"`
		Object api.Pod        `json:"object"`
	}
	if err := json.Unmarshal(watchEvents.Bytes(), &event); err != nil {
		t.Fatal(err)
	}

	if event.Type != etcd.Added || event.Object.Name != "nginx" || event.Object.ResourceVersion == 0 {
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
