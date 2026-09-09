package cri

import (
	"context"
	"errors"
	"fmt"

	"toy-kubernetes/api"
)

var ErrNotFound = errors.New("container not found")

type State string

// State の文字列値は Go client と C runtime の protocol で共有する
const (
	Running State = "Running"
	Stopped State = "Stopped"
)

// Unix socket の JSON protocol で Go client と C runtime が共有する状態
// JSON tag は C runtime の response field 名と一致させる
type Container struct {
	ID      string `json:"id"`
	PodName string `json:"pod"`
	State   State  `json:"state"`
}

// kubelet が Pod の実行状態を操作するための CRI の interface
type CRI interface {
	List(context.Context) ([]Container, error)
	Run(context.Context, api.Pod) (Container, error)
	Stop(context.Context, string) error
}

// TODO: 現在は kubelet の reconcile を確認するため in-memory runtime を使っている。
// 最終的には Unix socket の CRI client に置き換え、実際の runtime process へ接続する。
type FakeCRI struct {
	containers map[string]Container
	nextID     int
}

var _ CRI = (*FakeCRI)(nil)

func NewFakeCRI() *FakeCRI {
	return &FakeCRI{containers: make(map[string]Container)}
}

func (runtime *FakeCRI) List(context.Context) ([]Container, error) {
	containers := make([]Container, 0, len(runtime.containers))
	for _, container := range runtime.containers {
		containers = append(containers, container)
	}
	return containers, nil
}

func (runtime *FakeCRI) Run(_ context.Context, pod api.Pod) (Container, error) {
	if container, ok := runtime.containers[pod.Name]; ok {
		container.State = Running
		runtime.containers[pod.Name] = container
		return container, nil
	}

	runtime.nextID++
	container := Container{
		ID:      fmt.Sprintf("fake-%d", runtime.nextID),
		PodName: pod.Name,
		State:   Running,
	}
	runtime.containers[pod.Name] = container
	return container, nil
}

func (runtime *FakeCRI) Stop(_ context.Context, podName string) error {
	container, ok := runtime.containers[podName]
	if !ok {
		return ErrNotFound
	}
	container.State = Stopped
	runtime.containers[podName] = container
	return nil
}
