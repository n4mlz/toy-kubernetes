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

type Entry struct {
	ResourceVersion int64
	Object          json.RawMessage
}

// API server から etcd を使うための interface
type Etcd interface {
	Get(ctx context.Context, kind, name string, result any) (int64, error)
	List(ctx context.Context, kind string) ([]Entry, int64, error)
	Create(ctx context.Context, kind, name string, object any) (int64, error)
	Update(ctx context.Context, kind, name string, object any, resourceVersion int64) (int64, error)
	Delete(ctx context.Context, kind, name string, resourceVersion int64) error
	Watch(ctx context.Context, kind string, resourceVersion int64) (<-chan Event, error)
}

type EtcdClient struct {
	client *clientv3.Client
}

var _ Etcd = (*EtcdClient)(nil)

func NewEtcdClient(client *clientv3.Client) *EtcdClient {
	return &EtcdClient{client: client}
}

// 名前でリソースを取得し、保存時の ResourceVersion を返す
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

// 種類が一致するリソースをすべて取得し、それらのバージョンを返す
func (c *EtcdClient) List(ctx context.Context, kind string) ([]Entry, int64, error) {
	response, err := c.client.Get(ctx, objectPrefix(kind), clientv3.WithPrefix())

	if err != nil {
		return nil, 0, err
	}

	entries := make([]Entry, len(response.Kvs))
	for i, pair := range response.Kvs {
		entries[i] = Entry{ResourceVersion: pair.ModRevision, Object: pair.Value}
	}

	return entries, response.Header.Revision, nil
}

// リソースを新規保存し、etcd が割り当てたバージョンを返す
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

// 現在のバージョンが一致する場合だけリソースを更新する
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

// 現在のバージョンが一致する場合だけリソースを削除する
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

// 指定した種類のリソース変更をイベントの channel として返す
func (c *EtcdClient) Watch(ctx context.Context, kind string, resourceVersion int64) (<-chan Event, error) {
	options := []clientv3.OpOption{clientv3.WithPrefix(), clientv3.WithPrevKV()}
	if resourceVersion > 0 {
		options = append(options, clientv3.WithRev(resourceVersion+1))
	}

	watch := c.client.Watch(ctx, objectPrefix(kind), options...)
	events := make(chan Event, 16)

	go func() {
		defer close(events)

		for response := range watch {
			if err := response.Err(); err != nil {
				events <- Event{Kind: kind, Err: err}
				return
			}

			for _, event := range response.Events {
				if event.Kv == nil {
					continue
				}
				eventObject := event.Kv.Value
				eventKind := Modified

				if event.Type == clientv3.EventTypeDelete {
					if event.PrevKv == nil {
						continue
					}
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
