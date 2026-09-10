package cri

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"toy-kubernetes/api"
)

var ErrNotFound = errors.New("container not found")

type State string

const (
	Running State = "Running"
	Stopped State = "Stopped"
)

// kubelet とコンテナランタイムが扱うコンテナの状態
type Container struct {
	ID        string `json:"id"`
	PodName   string `json:"pod"`
	SandboxID string `json:"sandboxID,omitempty"`
	State     State  `json:"state"`
}

type Sandbox struct {
	ID      string        `json:"id"`
	PodName string        `json:"pod"`
	Source  SandboxSource `json:"source"`
	IP      string        `json:"ip,omitempty"`
	State   State         `json:"state"`
}

type SandboxSource string

const (
	StaticPod SandboxSource = "staticPod"
	Workload  SandboxSource = "workload"
)

type Event struct {
	Type      string
	Container Container
	Sandbox   Sandbox
}

// kubelet が Pod の実行状態を操作するための CRI の interface
type CRI interface {
	List(context.Context) ([]Container, error)
	ListSandboxes(context.Context) ([]Sandbox, error)
	Watch(context.Context) (<-chan Event, error)
	RunPodSandbox(context.Context, api.Pod, SandboxSource) (Sandbox, error)
	RunInSandbox(context.Context, api.Pod, string) (Container, error)
	Stop(context.Context, string) error
	StopPodSandbox(context.Context, string) error
}

// Unix socket 経由でコンテナ runtime を操作する CRI client
type Client struct {
	socket  string
	timeout time.Duration
}

var _ CRI = (*Client)(nil)

func NewClient(socket string) *Client {
	return &Client{socket: socket, timeout: 5 * time.Second}
}

type request struct {
	Operation string        `json:"op"`
	Pod       string        `json:"pod,omitempty"`
	Image     string        `json:"image,omitempty"`
	SandboxID string        `json:"sandboxID,omitempty"`
	Source    SandboxSource `json:"source,omitempty"`
}

type response struct {
	OK         bool          `json:"ok"`
	Error      string        `json:"error,omitempty"`
	ID         string        `json:"id,omitempty"`
	Pod        string        `json:"pod,omitempty"`
	State      State         `json:"state,omitempty"`
	SandboxID  string        `json:"sandboxID,omitempty"`
	Source     SandboxSource `json:"source,omitempty"`
	IP         string        `json:"ip,omitempty"`
	Containers []Container   `json:"containers,omitempty"`
	Sandboxes  []Sandbox     `json:"sandboxes,omitempty"`
	EventType  string        `json:"eventType,omitempty"`
	Container  Container     `json:"container,omitempty"`
	Sandbox    Sandbox       `json:"sandbox,omitempty"`
}

func (client *Client) Watch(ctx context.Context) (<-chan Event, error) {
	connection, err := (&net.Dialer{Timeout: client.timeout}).DialContext(ctx, "unix", client.socket)
	if err != nil {
		return nil, fmt.Errorf("connect CRI watch: %w", err)
	}
	if _, err := fmt.Fprintln(connection, `{"op":"watch"}`); err != nil {
		connection.Close()
		return nil, fmt.Errorf("write CRI watch request: %w", err)
	}
	decoder := json.NewDecoder(bufio.NewReader(connection))
	var accepted response
	if err := decoder.Decode(&accepted); err != nil {
		connection.Close()
		return nil, fmt.Errorf("decode CRI watch response: %w", err)
	}
	if !accepted.OK {
		connection.Close()
		return nil, fmt.Errorf("CRI watch failed: %s", strings.TrimSpace(accepted.Error))
	}

	events := make(chan Event)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-done:
		}
	}()
	go func() {
		defer close(events)
		defer connection.Close()
		defer close(done)
		for {
			var value response
			if err := decoder.Decode(&value); err != nil {
				return
			}
			select {
			case events <- Event{Type: value.EventType, Container: value.Container, Sandbox: value.Sandbox}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

func (client *Client) List(ctx context.Context) ([]Container, error) {
	result, err := client.request(ctx, request{Operation: "list"})
	if err != nil {
		return nil, err
	}
	return result.Containers, nil
}

func (client *Client) ListSandboxes(ctx context.Context) ([]Sandbox, error) {
	result, err := client.request(ctx, request{Operation: "list-sandboxes"})
	if err != nil {
		return nil, err
	}
	return result.Sandboxes, nil
}

func (client *Client) RunPodSandbox(ctx context.Context, pod api.Pod, source SandboxSource) (Sandbox, error) {
	result, err := client.request(ctx, request{Operation: "run-pod-sandbox", Pod: pod.Name, Source: source})
	if err != nil {
		return Sandbox{}, err
	}
	return Sandbox{ID: result.SandboxID, PodName: result.Pod, Source: result.Source, IP: result.IP, State: result.State}, nil
}

func (client *Client) RunInSandbox(ctx context.Context, pod api.Pod, sandboxID string) (Container, error) {
	if len(pod.Spec.Containers) == 0 {
		return Container{}, errors.New("Pod has no container")
	}
	result, err := client.request(ctx, request{Operation: "run-in-sandbox", Pod: pod.Name, Image: pod.Spec.Containers[0].Image, SandboxID: sandboxID})
	if err != nil {
		return Container{}, err
	}
	return Container{ID: result.ID, PodName: result.Pod, SandboxID: result.SandboxID, State: result.State}, nil
}

func (client *Client) StopPodSandbox(ctx context.Context, sandboxID string) error {
	_, err := client.request(ctx, request{Operation: "stop-pod-sandbox", SandboxID: sandboxID})
	return err
}

func (client *Client) Stop(ctx context.Context, podName string) error {
	_, err := client.request(ctx, request{Operation: "stop", Pod: podName})
	return err
}

func (client *Client) Inspect(ctx context.Context, podName string) (Container, error) {
	result, err := client.request(ctx, request{Operation: "inspect", Pod: podName})
	if err != nil {
		return Container{}, err
	}
	return Container{ID: result.ID, PodName: result.Pod, State: result.State}, nil
}

func (client *Client) request(ctx context.Context, input request) (response, error) {
	connection, err := (&net.Dialer{Timeout: client.timeout}).DialContext(ctx, "unix", client.socket)
	if err != nil {
		return response{}, fmt.Errorf("connect CRI socket: %w", err)
	}
	defer connection.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetWriteDeadline(time.Now().Add(client.timeout))
	}

	data, err := json.Marshal(input)
	if err != nil {
		return response{}, fmt.Errorf("encode CRI request: %w", err)
	}
	if _, err := fmt.Fprintf(connection, "%s\n", data); err != nil {
		return response{}, fmt.Errorf("write CRI request: %w", err)
	}

	var result response
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&result); err != nil {
		return response{}, fmt.Errorf("decode CRI response: %w", err)
	}
	if !result.OK {
		return response{}, fmt.Errorf("CRI request failed: %s", strings.TrimSpace(result.Error))
	}
	return result, nil
}
