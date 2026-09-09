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
	Operation string `json:"op"`
	Pod       string `json:"pod,omitempty"`
	Image     string `json:"image,omitempty"`
}

type response struct {
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	ID         string      `json:"id,omitempty"`
	Pod        string      `json:"pod,omitempty"`
	State      State       `json:"state,omitempty"`
	Containers []Container `json:"containers,omitempty"`
}

func (client *Client) List(ctx context.Context) ([]Container, error) {
	result, err := client.request(ctx, request{Operation: "list"})
	if err != nil {
		return nil, err
	}
	return result.Containers, nil
}

func (client *Client) Run(ctx context.Context, pod api.Pod) (Container, error) {
	if len(pod.Spec.Containers) == 0 {
		return Container{}, errors.New("Pod has no container")
	}

	result, err := client.request(ctx, request{
		Operation: "run",
		Pod:       pod.Name,
		Image:     pod.Spec.Containers[0].Image,
	})
	if err != nil {
		return Container{}, err
	}
	return Container{ID: result.ID, PodName: result.Pod, State: result.State}, nil
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
