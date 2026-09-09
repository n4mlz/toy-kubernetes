package kubelet

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"toy-kubernetes/api"
	"toy-kubernetes/cri"
)

func TestLoadStaticPodsReadsManifestFilesInOrder(t *testing.T) {
	directory := t.TempDir()
	writeManifest(t, directory, "02-api.yaml", "api")
	writeManifest(t, directory, "01-etcd.yaml", "etcd")
	if err := os.WriteFile(filepath.Join(directory, "README"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	pods, err := LoadStaticPods(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 2 || pods[0].Name != "etcd" || pods[1].Name != "api" {
		t.Fatalf("static Pods should be read in filename order: %#v", pods)
	}
}

func TestReconcileStaticPodsStartsMissingAndStopsRemovedPods(t *testing.T) {
	runtime := &staticRuntime{containers: []cri.Container{{PodName: "removed", State: cri.Running}}}
	pods := []api.Pod{{ObjectMeta: api.ObjectMeta{Name: "api"}, Spec: api.PodSpec{Containers: []api.Container{{Image: "nginx"}}}}}

	if err := ReconcileStaticPods(context.Background(), runtime, pods); err != nil {
		t.Fatal(err)
	}
	if len(runtime.started) != 1 || runtime.started[0] != "api" {
		t.Fatalf("missing static Pod should be started: %#v", runtime.started)
	}
	if len(runtime.stopped) != 1 || runtime.stopped[0] != "removed" {
		t.Fatalf("removed static Pod should be stopped: %#v", runtime.stopped)
	}
}

func writeManifest(t *testing.T, directory, filename, name string) {
	t.Helper()
	manifest := "kind: Pod\nmetadata:\n  name: " + name + "\nspec:\n  containers:\n    - name: main\n      image: nginx\n"
	if err := os.WriteFile(filepath.Join(directory, filename), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

type staticRuntime struct {
	containers []cri.Container
	started    []string
	stopped    []string
}

func (runtime *staticRuntime) List(context.Context) ([]cri.Container, error) {
	return runtime.containers, nil
}

func (runtime *staticRuntime) Run(_ context.Context, pod api.Pod) (cri.Container, error) {
	runtime.started = append(runtime.started, pod.Name)
	return cri.Container{PodName: pod.Name, State: cri.Running}, nil
}

func (runtime *staticRuntime) Stop(_ context.Context, podName string) error {
	runtime.stopped = append(runtime.stopped, podName)
	return nil
}
