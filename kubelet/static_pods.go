package kubelet

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"toy-kubernetes/api"
	"toy-kubernetes/cri"

	"gopkg.in/yaml.v3"
)

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

	desired := make(map[string]api.Pod, len(pods))
	for _, pod := range pods {
		desired[pod.Name] = pod
		if container, ok := containerForPod(containers, pod.Name); !ok || container.State != cri.Running {
			if _, err := runtime.Run(ctx, pod); err != nil {
				return fmt.Errorf("start static Pod %s: %w", pod.Name, err)
			}
		}
	}

	for _, container := range containers {
		if _, ok := desired[container.PodName]; !ok && container.State == cri.Running {
			if err := runtime.Stop(ctx, container.PodName); err != nil {
				return fmt.Errorf("stop removed static Pod %s: %w", container.PodName, err)
			}
		}
	}
	return nil
}

func containerForPod(containers []cri.Container, podName string) (cri.Container, bool) {
	for _, container := range containers {
		if container.PodName == podName {
			return container, true
		}
	}
	return cri.Container{}, false
}
