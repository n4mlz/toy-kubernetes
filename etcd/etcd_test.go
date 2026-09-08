package etcd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os/exec"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type object struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func TestEtcdCRUDKeepsObjectsAndVersions(t *testing.T) {
	etcd := newTestEtcd(t)
	ctx := context.Background()

	firstVersion, err := etcd.Create(ctx, "Pod", "one", object{Name: "one", Value: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if firstVersion == 0 {
		t.Fatal("created object should have a resource version")
	}

	if _, err := etcd.Create(ctx, "Pod", "two", object{Name: "two", Value: "second"}); err != nil {
		t.Fatal(err)
	}

	var pod object
	version, err := etcd.Get(ctx, "Pod", "one", &pod)
	if err != nil {
		t.Fatal(err)
	}
	if pod != (object{Name: "one", Value: "first"}) || version != firstVersion {
		t.Fatalf("get should return the stored object and version: %#v, %d", pod, version)
	}

	entries, _, err := etcd.List(ctx, "Pod")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("list should contain both Pods: %#v", entries)
	}
	var first, second object
	if err := json.Unmarshal(entries[0].Object, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entries[1].Object, &second); err != nil {
		t.Fatal(err)
	}
	if first.Name != "one" || second.Name != "two" || entries[0].ResourceVersion == 0 || entries[1].ResourceVersion == 0 {
		t.Fatalf("list should contain objects and their revisions: %#v", entries)
	}

	secondVersion, err := etcd.Update(ctx, "Pod", "one", object{Name: "one", Value: "updated"}, version)
	if err != nil {
		t.Fatal(err)
	}
	if secondVersion <= firstVersion {
		t.Fatalf("update should advance the resource version: %d -> %d", firstVersion, secondVersion)
	}

	if err := etcd.Delete(ctx, "Pod", "one", secondVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := etcd.Get(ctx, "Pod", "one", &pod); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted object should not be readable, got %v", err)
	}
}

func TestEtcdWatchPublishesChangesAndRejectsStaleUpdates(t *testing.T) {
	etcd := newTestEtcd(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	version, err := etcd.Create(context.Background(), "Pod", "one", object{Name: "one"})
	if err != nil {
		t.Fatal(err)
	}

	firstWatcher, err := etcd.Watch(ctx, "Pod", version)
	if err != nil {
		t.Fatal(err)
	}
	secondWatcher, err := etcd.Watch(ctx, "Pod", version)
	if err != nil {
		t.Fatal(err)
	}

	staleVersion := version
	version, err = etcd.Update(context.Background(), "Pod", "one", object{Name: "one", Value: "updated"}, staleVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := etcd.Update(context.Background(), "Pod", "one", object{Name: "one", Value: "stale"}, staleVersion); !errors.Is(err, ErrConflict) {
		t.Fatalf("a stale update should be rejected with ErrConflict, got %v", err)
	}

	for _, events := range []<-chan Event{firstWatcher, secondWatcher} {
		event := receiveEvent(t, events)
		if event.Type != Modified || event.Name != "one" || event.ResourceVersion != version {
			t.Fatalf("update should publish a MODIFIED event: %#v", event)
		}
	}

	if err := etcd.Delete(context.Background(), "Pod", "one", version); err != nil {
		t.Fatal(err)
	}
	for _, events := range []<-chan Event{firstWatcher, secondWatcher} {
		event := receiveEvent(t, events)
		if event.Type != Deleted || event.Name != "one" {
			t.Fatalf("delete should publish a DELETED event: %#v", event)
		}
	}
}

func newTestEtcd(t *testing.T) *EtcdClient {
	t.Helper()
	clientURL := freeURL(t)
	peerURL := freeURL(t)

	command := exec.Command("etcd",
		"--name", "toy-kubernetes-test",
		"--data-dir", t.TempDir(),
		"--listen-client-urls", clientURL,
		"--advertise-client-urls", clientURL,
		"--listen-peer-urls", peerURL,
		"--initial-advertise-peer-urls", peerURL,
		"--initial-cluster", "toy-kubernetes-test="+peerURL,
		"--initial-cluster-state", "new",
	)
	command.Stdout = io.Discard
	command.Stderr = io.Discard

	if err := command.Start(); err != nil {
		t.Fatalf("start test etcd: %v", err)
	}

	client, err := clientv3.New(clientv3.Config{Endpoints: []string{clientURL}, DialTimeout: 2 * time.Second})
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("create etcd client: %v", err)
	}

	deadline, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for deadline.Err() == nil {
		if _, err := client.Get(deadline, "health"); err == nil {
			t.Cleanup(func() {
				_ = client.Close()
				_ = command.Process.Kill()
				_ = command.Wait()
			})
			return NewEtcdClient(client)
		}
		time.Sleep(50 * time.Millisecond)
	}

	_ = client.Close()
	_ = command.Process.Kill()
	_ = command.Wait()
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

func receiveEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		if event.Err != nil {
			t.Fatalf("watch failed: %v", event.Err)
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not receive an event")
		return Event{}
	}
}
