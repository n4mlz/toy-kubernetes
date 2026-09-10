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

func TestReconcileStaticPodsStartsMissingWithoutStoppingNormalContainers(t *testing.T) {
	runtime := &staticRuntime{containers: []cri.Container{{PodName: "removed", State: cri.Running}}}
	pods := []api.Pod{{ObjectMeta: api.ObjectMeta{Name: "api"}, Spec: api.PodSpec{Containers: []api.Container{{Image: "nginx"}}}}}

	if err := ReconcileStaticPods(context.Background(), runtime, pods); err != nil {
		t.Fatal(err)
	}
	if len(runtime.started) != 1 || runtime.started[0] != "api" {
		t.Fatalf("missing static Pod should be started: %#v", runtime.started)
	}
	if len(runtime.stopped) != 0 {
		t.Fatalf("normal containers should not be stopped by static Pod reconcile: %#v", runtime.stopped)
	}
}

func TestReconcileStaticPodsStopsOnlyRemovedStaticPods(t *testing.T) {
	runtime := &staticRuntime{
		sandboxes: []cri.Sandbox{
			{ID: "static-removed", PodName: "removed-static", Source: cri.StaticPod, State: cri.Running},
			{ID: "workload", PodName: "removed-workload", Source: cri.Workload, State: cri.Running},
			{ID: "static-kept", PodName: "kept-static", Source: cri.StaticPod, State: cri.Running},
		},
	}
	pods := []api.Pod{{ObjectMeta: api.ObjectMeta{Name: "kept-static"}, Spec: api.PodSpec{Containers: []api.Container{{Image: "nginx"}}}}}

	if err := ReconcileStaticPods(context.Background(), runtime, pods); err != nil {
		t.Fatal(err)
	}
	if len(runtime.stoppedSandboxes) != 1 || runtime.stoppedSandboxes[0] != "static-removed" {
		t.Fatalf("only the removed static Pod should be stopped: %#v", runtime.stoppedSandboxes)
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
	containers       []cri.Container
	sandboxes        []cri.Sandbox
	started          []string
	stopped          []string
	stoppedSandboxes []string
}

func (runtime *staticRuntime) Watch(context.Context) (<-chan cri.Event, error) {
	return make(chan cri.Event), nil
}

func (runtime *staticRuntime) ListSandboxes(context.Context) ([]cri.Sandbox, error) {
	return runtime.sandboxes, nil
}

func (runtime *staticRuntime) RunPodSandbox(_ context.Context, pod api.Pod, source cri.SandboxSource) (cri.Sandbox, error) {
	sandbox := cri.Sandbox{ID: "sandbox-static-" + pod.Name, PodName: pod.Name, Source: source, State: cri.Running}
	runtime.sandboxes = append(runtime.sandboxes, sandbox)
	return sandbox, nil
}

func (runtime *staticRuntime) RunInSandbox(_ context.Context, pod api.Pod, sandboxID string) (cri.Container, error) {
	runtime.started = append(runtime.started, pod.Name)
	return cri.Container{PodName: pod.Name, SandboxID: sandboxID, State: cri.Running}, nil
}

func (runtime *staticRuntime) StopPodSandbox(_ context.Context, sandboxID string) error {
	runtime.stoppedSandboxes = append(runtime.stoppedSandboxes, sandboxID)
	return nil
}

func (runtime *staticRuntime) List(context.Context) ([]cri.Container, error) {
	return runtime.containers, nil
}

func (runtime *staticRuntime) Stop(_ context.Context, podName string) error {
	runtime.stopped = append(runtime.stopped, podName)
	return nil
}
