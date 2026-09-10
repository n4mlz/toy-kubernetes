package kubelet

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"toy-kubernetes/api"
	"toy-kubernetes/cri"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

// manifest directory の変更を通知する。変更後の desired state は再度読み込む
func WatchStaticPods(ctx context.Context, directory string) (<-chan struct{}, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("watch static Pod directory: %w", err)
	}
	if _, err := unix.InotifyAddWatch(fd, directory, unix.IN_CREATE|unix.IN_DELETE|unix.IN_MODIFY|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("watch static Pod directory: %w", err)
	}

	changes := make(chan struct{}, 1)
	var closeOnce sync.Once
	closeFD := func() { closeOnce.Do(func() { _ = unix.Close(fd) }) }
	go func() {
		defer close(changes)
		defer closeFD()
		buffer := make([]byte, 4096)
		for {
			count, err := unix.Read(fd, buffer)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				return
			}
			if count == 0 {
				continue
			}
			select {
			case changes <- struct{}{}:
			default:
			}
		}
	}()
	go func() {
		<-ctx.Done()
		closeFD()
	}()
	return changes, nil
}

// manifest directory の Pod manifest を読み込む
func LoadStaticPods(directory string) ([]api.Pod, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read static Pod directory: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !isManifest(entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var pods []api.Pod
	for _, name := range names {
		path := filepath.Join(directory, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read static Pod %s: %w", name, err)
		}

		decoder := yaml.NewDecoder(strings.NewReader(string(data)))
		for {
			var pod api.Pod
			err := decoder.Decode(&pod)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("decode static Pod %s: %w", name, err)
			}
			if pod.Name == "" {
				break
			}
			if pod.Kind != "Pod" {
				return nil, fmt.Errorf("static manifest %s is not a Pod", name)
			}
			if len(pod.Spec.Containers) == 0 {
				return nil, fmt.Errorf("static Pod %s has no container", pod.Name)
			}
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

func isManifest(name string) bool {
	extension := filepath.Ext(name)
	return extension == ".yaml" || extension == ".yml" || extension == ".json"
}

// local manifest の desired state を CRI runtime に反映する
func ReconcileStaticPods(ctx context.Context, runtime cri.CRI, pods []api.Pod) error {
	containers, err := runtime.List(ctx)
	if err != nil {
		return err
	}
	sandboxes, err := runtime.ListSandboxes(ctx)
	if err != nil {
		return err
	}

	desired := make(map[string]struct{}, len(pods))

	for _, pod := range pods {
		desired[pod.Name] = struct{}{}
		sandbox, sandboxExists := staticSandboxForPod(sandboxes, pod.Name)
		if !sandboxExists {
			sandbox, err = runtime.RunPodSandbox(ctx, pod, cri.StaticPod)
			if err != nil {
				return fmt.Errorf("start static Pod sandbox %s: %w", pod.Name, err)
			}
			sandboxes = append(sandboxes, sandbox)
		}
		if container, ok := containerForSandbox(containers, sandbox.ID); !ok || container.State != cri.Running {
			if _, err := runtime.RunInSandbox(ctx, pod, sandbox.ID); err != nil {
				return fmt.Errorf("start static Pod %s: %w", pod.Name, err)
			}
		}
	}

	for _, sandbox := range sandboxes {
		if sandbox.Source != cri.StaticPod || sandbox.State != cri.Running {
			continue
		}
		if _, ok := desired[sandbox.PodName]; ok {
			continue
		}
		if err := runtime.StopPodSandbox(ctx, sandbox.ID); err != nil {
			return fmt.Errorf("stop removed static Pod %s: %w", sandbox.PodName, err)
		}
	}
	return nil
}

func staticSandboxForPod(sandboxes []cri.Sandbox, podName string) (cri.Sandbox, bool) {
	for _, sandbox := range sandboxes {
		if sandbox.PodName == podName && sandbox.Source == cri.StaticPod {
			return sandbox, true
		}
	}
	return cri.Sandbox{}, false
}

func containerForSandbox(containers []cri.Container, sandboxID string) (cri.Container, bool) {
	for _, container := range containers {
		if container.SandboxID == sandboxID {
			return container, true
		}
	}
	return cri.Container{}, false
}
