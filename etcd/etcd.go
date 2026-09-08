package etcd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	clientv3 "go.etcd.io/etcd/client/v3"
)

var (
	ErrNotFound      = errors.New("object not found")
	ErrAlreadyExists = errors.New("object already exists")
	ErrConflict      = errors.New("resource version conflict")
)

type EventType string

const (
	Added    EventType = "ADDED"
	Modified EventType = "MODIFIED"
	Deleted  EventType = "DELETED"
)

type Event struct {
	Type            EventType
	Kind            string
	Name            string
	ResourceVersion int64
	Object          json.RawMessage
	Err             error
}

// Etcd は API server が etcd に要求する interface
// resource version は etcd の更新番号 (API の競合検出に使用される)
type Etcd interface {
	Get(ctx context.Context, kind, name string, result any) (int64, error)
	List(ctx context.Context, kind string, result any) (int64, error)
	Create(ctx context.Context, kind, name string, object any) (int64, error)
	Update(ctx context.Context, kind, name string, object any, resourceVersion int64) (int64, error)
	Delete(ctx context.Context, kind, name string, resourceVersion int64) error
	Watch(ctx context.Context, kind string) (<-chan Event, error)
}

type EtcdClient struct {
	client *clientv3.Client
}

var _ Etcd = (*EtcdClient)(nil)

func NewEtcdClient(client *clientv3.Client) *EtcdClient {
	return &EtcdClient{client: client}
}

// ここからは interface の実装

func (c *EtcdClient) Get(ctx context.Context, kind, name string, result any) (int64, error) {
	response, err := c.client.Get(ctx, objectKey(kind, name))

	if err != nil {
		return 0, err
	}

	if len(response.Kvs) == 0 {
		return 0, ErrNotFound
	}

	if err := json.Unmarshal(response.Kvs[0].Value, result); err != nil {
		return 0, fmt.Errorf("decode stored object: %w", err)
	}

	return response.Kvs[0].ModRevision, nil
}

func (c *EtcdClient) List(ctx context.Context, kind string, result any) (int64, error) {
	response, err := c.client.Get(ctx, objectPrefix(kind), clientv3.WithPrefix())

	if err != nil {
		return 0, err
	}

	values := make([]json.RawMessage, len(response.Kvs))
	for i, pair := range response.Kvs {
		values[i] = pair.Value
	}

	data, err := json.Marshal(values)
	if err != nil {
		return 0, fmt.Errorf("encode list: %w", err)
	}

	if err := json.Unmarshal(data, result); err != nil {
		return 0, fmt.Errorf("decode object list: %w", err)
	}

	return response.Header.Revision, nil
}

func (c *EtcdClient) Create(ctx context.Context, kind, name string, object any) (int64, error) {
	value, err := json.Marshal(object)
	if err != nil {
		return 0, fmt.Errorf("encode object: %w", err)
	}

	response, err := c.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(objectKey(kind, name)), "=", 0)).
		Then(clientv3.OpPut(objectKey(kind, name), string(value))).
		Commit()

	if err != nil {
		return 0, err
	}

	if !response.Succeeded {
		return 0, ErrAlreadyExists
	}

	return response.Header.Revision, nil
}

func (c *EtcdClient) Update(ctx context.Context, kind, name string, object any, resourceVersion int64) (int64, error) {
	value, err := json.Marshal(object)
	if err != nil {
		return 0, fmt.Errorf("encode object: %w", err)
	}

	response, err := c.client.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(objectKey(kind, name)), "=", resourceVersion)).
		Then(clientv3.OpPut(objectKey(kind, name), string(value))).
		Commit()

	if err != nil {
		return 0, err
	}

	if !response.Succeeded {
		return 0, ErrConflict
	}

	return response.Header.Revision, nil
}

func (c *EtcdClient) Delete(ctx context.Context, kind, name string, resourceVersion int64) error {
	compare := clientv3.Compare(clientv3.Version(objectKey(kind, name)), ">", 0)
	if resourceVersion != 0 {
		compare = clientv3.Compare(clientv3.ModRevision(objectKey(kind, name)), "=", resourceVersion)
	}

	response, err := c.client.Txn(ctx).If(compare).Then(clientv3.OpDelete(objectKey(kind, name))).Commit()

	if err != nil {
		return err
	}

	if !response.Succeeded {
		return ErrNotFound
	}

	return nil
}

func (c *EtcdClient) Watch(ctx context.Context, kind string) (<-chan Event, error) {
	watch := c.client.Watch(ctx, objectPrefix(kind), clientv3.WithPrefix(), clientv3.WithPrevKV())
	events := make(chan Event, 16)

	go func() {
		defer close(events)

		for response := range watch {
			if err := response.Err(); err != nil {
				events <- Event{Kind: kind, Err: err}
				return
			}

			for _, event := range response.Events {
				eventObject := event.Kv.Value
				eventKind := Modified

				if event.Type == clientv3.EventTypeDelete {
					eventKind = Deleted
					eventObject = event.PrevKv.Value
				} else if event.PrevKv == nil {
					eventKind = Added
				}
				events <- Event{
					Type:            eventKind,
					Kind:            kind,
					Name:            nameFromKey(string(event.Kv.Key)),
					ResourceVersion: event.Kv.ModRevision,
					Object:          eventObject,
				}
			}
		}
	}()

	return events, nil
}

func objectPrefix(kind string) string { return "/toy-kubernetes/" + kind + "/" }

func objectKey(kind, name string) string { return objectPrefix(kind) + name }

func nameFromKey(storedKey string) string {
	parts := strings.Split(strings.TrimSuffix(storedKey, "/"), "/")
	return parts[len(parts)-1]
}
