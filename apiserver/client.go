package apiserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"toy-kubernetes/api"
)

// API server の HTTP API を呼び出すクライアント
// controller や kubelet など、API server とは別に動く component に組み込んで使う
type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: http.DefaultClient,
	}
}

type ResourceClient[T any] struct {
	client   *Client
	resource string
}

type ResourceList[T any] struct {
	ResourceVersion int64 `json:"resourceVersion"`
	Items           []T   `json:"items"`
}

type WatchEvent[T any] struct {
	Type            string
	Object          T
	ResourceVersion int64
	Err             error
}

func WatchErrors[T any](ctx context.Context, events <-chan WatchEvent[T]) <-chan error {
	errors := make(chan error, 1)
	go func() {
		defer close(errors)
		select {
		case event, ok := <-events:
			if ok {
				errors <- event.Err
			}
		case <-ctx.Done():
		}
	}()
	return errors
}

func (client *Client) Pods() *ResourceClient[api.Pod] {
	return &ResourceClient[api.Pod]{client: client, resource: "pods"}
}

func (client *Client) Nodes() *ResourceClient[api.Node] {
	return &ResourceClient[api.Node]{client: client, resource: "nodes"}
}

func (client *Client) Deployments() *ResourceClient[api.Deployment] {
	return &ResourceClient[api.Deployment]{client: client, resource: "deployments"}
}

func (client *Client) ReplicaSets() *ResourceClient[api.ReplicaSet] {
	return &ResourceClient[api.ReplicaSet]{client: client, resource: "replicasets"}
}

func (client *Client) Services() *ResourceClient[api.Service] {
	return &ResourceClient[api.Service]{client: client, resource: "services"}
}

func (resources *ResourceClient[T]) Create(ctx context.Context, object T) (T, error) {
	var created T

	if err := resources.client.request(ctx, http.MethodPost, "/"+resources.resource, object, &created); err != nil {
		return created, err
	}

	return created, nil
}

func (resources *ResourceClient[T]) Get(ctx context.Context, name string) (T, error) {
	var object T

	if err := resources.client.request(ctx, http.MethodGet, "/"+resources.resource+"/"+name, nil, &object); err != nil {
		return object, err
	}

	return object, nil
}

func (resources *ResourceClient[T]) List(ctx context.Context) (ResourceList[T], error) {
	var objects ResourceList[T]

	if err := resources.client.request(ctx, http.MethodGet, "/"+resources.resource, nil, &objects); err != nil {
		return ResourceList[T]{}, err
	}

	return objects, nil
}

// List の revision より後に発生した resource event を受け取る
func (resources *ResourceClient[T]) Watch(ctx context.Context, resourceVersion int64) (<-chan WatchEvent[T], error) {
	path := fmt.Sprintf("/watch/%s?resourceVersion=%d", resources.resource, resourceVersion)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resources.client.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create watch request: %w", err)
	}
	response, err := resources.client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("open %s watch: %w", resources.resource, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		return nil, fmt.Errorf("open %s watch: API request failed with status %d", resources.resource, response.StatusCode)
	}

	events := make(chan WatchEvent[T])
	go func() {
		defer close(events)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			var value struct {
				Type   string          `json:"type"`
				Object json.RawMessage `json:"object"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
				events <- WatchEvent[T]{Err: fmt.Errorf("decode %s watch event: %w", resources.resource, err)}
				return
			}
			var object T
			if err := json.Unmarshal(value.Object, &object); err != nil {
				events <- WatchEvent[T]{Err: fmt.Errorf("decode %s watch object: %w", resources.resource, err)}
				return
			}
			events <- WatchEvent[T]{Type: value.Type, Object: object}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			events <- WatchEvent[T]{Err: fmt.Errorf("read %s watch: %w", resources.resource, err)}
		}
	}()
	return events, nil
}

func (resources *ResourceClient[T]) Update(ctx context.Context, name string, object T) (T, error) {
	var updated T

	if err := resources.client.request(ctx, http.MethodPut, "/"+resources.resource+"/"+name, object, &updated); err != nil {
		return updated, err
	}

	return updated, nil
}

func (resources *ResourceClient[T]) Delete(ctx context.Context, name string, resourceVersion int64) error {
	path := fmt.Sprintf("/%s/%s?resourceVersion=%d", resources.resource, name, resourceVersion)
	return resources.client.request(ctx, http.MethodDelete, path, nil, nil)
}

func (client *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var responseError struct {
			Message string `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&responseError); err != nil {
			return fmt.Errorf("API request failed with status %d", response.StatusCode)
		}
		return fmt.Errorf("API request failed with status %d: %s", response.StatusCode, responseError.Message)
	}

	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
