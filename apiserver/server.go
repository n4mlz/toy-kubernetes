package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"toy-kubernetes/api"
	"toy-kubernetes/config"
	"toy-kubernetes/etcd"
)

// Server は API server 側で HTTP API を提供する
// client.go の Client からは通常、別の component やプロセスから HTTP で接続されることになる
type Server struct {
	etcd etcd.Etcd
}

type resourceList struct {
	ResourceVersion int64          `json:"resourceVersion"`
	Items           []api.Resource `json:"items"`
}

func NewServer(etcdClient etcd.Etcd) *Server {
	return &Server{etcd: etcdClient}
}

func (server *Server) Handler() http.Handler {
	return http.HandlerFunc(server.serveHTTP)
}

// URL をリソース操作または watch 操作に振り分ける
func (server *Server) serveHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	path := strings.Trim(request.URL.Path, "/")
	pathParts := strings.Split(path, "/")

	if len(pathParts) == 2 && pathParts[0] == "watch" {
		server.watch(responseWriter, request, pathParts[1])
		return
	}

	if len(pathParts) < 1 || len(pathParts) > 2 {
		writeError(responseWriter, http.StatusNotFound, "resource path not found")
		return
	}

	resource := pathParts[0]
	kind, err := kindForResource(resource)
	if err != nil {
		writeError(responseWriter, http.StatusNotFound, err.Error())
		return
	}

	if len(pathParts) == 1 {
		server.collection(responseWriter, request, resource, kind)
		return
	}

	server.object(responseWriter, request, resource, kind, pathParts[1])
}

// リソースの一覧取得と新規作成を処理する
func (server *Server) collection(responseWriter http.ResponseWriter, request *http.Request, resource, kind string) {
	switch request.Method {
	case http.MethodGet:
		entries, version, err := server.etcd.List(request.Context(), kind)
		if err != nil {
			writeStoreError(responseWriter, err)
			return
		}

		objects, err := decodeList(kind, entries)
		if err != nil {
			writeError(responseWriter, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(responseWriter, http.StatusOK, resourceList{ResourceVersion: version, Items: objects})
	case http.MethodPost:
		object, name, err := decodeResource(resource, request.Body)

		if err != nil {
			writeError(responseWriter, http.StatusBadRequest, err.Error())
			return
		}
		if service, ok := object.(*api.Service); ok {
			if err := server.prepareService(request.Context(), service, ""); err != nil {
				writeError(responseWriter, http.StatusBadRequest, err.Error())
				return
			}
		}

		version, err := server.etcd.Create(request.Context(), kind, name, object)
		if err != nil {
			writeStoreError(responseWriter, err)
			return
		}

		object.SetResourceVersion(version)
		writeJSON(responseWriter, http.StatusCreated, object)
	default:
		writeError(responseWriter, http.StatusMethodNotAllowed, "method is not allowed for a resource collection")
	}
}

func (server *Server) allocateServiceIP(ctx context.Context) (string, error) {
	prefix, err := netip.ParsePrefix(config.ServiceCIDR)
	if err != nil {
		return "", fmt.Errorf("parse service CIDR: %w", err)
	}
	if !prefix.Addr().Is4() {
		return "", errors.New("service CIDR must be IPv4")
	}
	entries, _, err := server.etcd.List(ctx, "Service")
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(entries))
	for _, entry := range entries {
		service := &api.Service{}
		if err := json.Unmarshal(entry.Object, service); err != nil {
			return "", fmt.Errorf("decode Service: %w", err)
		}
		used[service.Spec.ClusterIP] = true
	}

	address := prefix.Addr().As4()
	base := uint32(address[0])<<24 | uint32(address[1])<<16 | uint32(address[2])<<8 | uint32(address[3])
	// network address は使えないため、host 部分 2 から割り当てる。host 1 は予約する
	const firstServiceHost = 2
	lastHost := (uint32(1) << uint(32-prefix.Bits())) - 1
	for host := uint32(firstServiceHost); host < lastHost; host++ {
		candidate := serviceAddress(base, host)
		if !used[candidate] {
			return candidate, nil
		}
	}
	return "", errors.New("service CIDR has no available address")
}

func (server *Server) prepareService(ctx context.Context, service *api.Service, excludeName string) error {
	if service.Spec.Type == "" {
		service.Spec.Type = api.ServiceClusterIP
	}
	if service.Spec.Type != api.ServiceClusterIP && service.Spec.Type != api.ServiceNodePort {
		return fmt.Errorf("unsupported Service type %q", service.Spec.Type)
	}
	if service.Spec.ClusterIP == "" {
		clusterIP, err := server.allocateServiceIP(ctx)
		if err != nil {
			return err
		}
		service.Spec.ClusterIP = clusterIP
	}
	if service.Spec.Type == api.ServiceNodePort {
		port, err := server.allocateNodePort(ctx, service.Spec.NodePort, excludeName)
		if err != nil {
			return err
		}
		service.Spec.NodePort = port
		return nil
	}
	if service.Spec.NodePort != 0 {
		return errors.New("nodePort is only valid for NodePort Service")
	}
	return nil
}

func (server *Server) allocateNodePort(ctx context.Context, requested int, excludeName string) (int, error) {
	// Kubernetes の既定 NodePort 範囲
	const (
		firstNodePort = 30000
		lastNodePort  = 32767
	)
	if requested != 0 && (requested < firstNodePort || requested > lastNodePort) {
		return 0, fmt.Errorf("nodePort must be between %d and %d", firstNodePort, lastNodePort)
	}

	entries, _, err := server.etcd.List(ctx, "Service")
	if err != nil {
		return 0, err
	}
	used := make(map[int]bool, len(entries))
	for _, entry := range entries {
		service := &api.Service{}
		if err := json.Unmarshal(entry.Object, service); err != nil {
			return 0, fmt.Errorf("decode Service: %w", err)
		}
		if service.Name != excludeName && service.Spec.NodePort != 0 {
			used[service.Spec.NodePort] = true
		}
	}
	if requested != 0 {
		if used[requested] {
			return 0, fmt.Errorf("nodePort %d is already allocated", requested)
		}
		return requested, nil
	}
	for port := firstNodePort; port <= lastNodePort; port++ {
		if !used[port] {
			return port, nil
		}
	}
	return 0, errors.New("NodePort range has no available port")
}

func serviceAddress(base, host uint32) string {
	value := base + host
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}).String()
}

// 名前を指定したリソースの取得・更新・削除を処理する
func (server *Server) object(responseWriter http.ResponseWriter, request *http.Request, resource, kind, name string) {
	switch request.Method {
	case http.MethodGet:
		object := newResource(kind)

		version, err := server.etcd.Get(request.Context(), kind, name, object)
		if err != nil {
			writeStoreError(responseWriter, err)
			return
		}

		object.SetResourceVersion(version)
		writeJSON(responseWriter, http.StatusOK, object)
	case http.MethodPut:
		object, bodyName, err := decodeResource(resource, request.Body)

		if err != nil {
			writeError(responseWriter, http.StatusBadRequest, err.Error())
			return
		}

		if bodyName != name {
			writeError(responseWriter, http.StatusBadRequest, "metadata.name must match the URL")
			return
		}

		if service, ok := object.(*api.Service); ok {
			if err := server.prepareService(request.Context(), service, name); err != nil {
				writeError(responseWriter, http.StatusBadRequest, err.Error())
				return
			}
		}

		resourceVersion := object.GetResourceVersion()
		if resourceVersion == 0 {
			writeError(responseWriter, http.StatusBadRequest, "metadata.resourceVersion is required for update")
			return
		}

		version, err := server.etcd.Update(request.Context(), kind, name, object, resourceVersion)
		if err != nil {
			writeStoreError(responseWriter, err)
			return
		}

		object.SetResourceVersion(version)
		writeJSON(responseWriter, http.StatusOK, object)
	case http.MethodDelete:
		resourceVersion, err := queryResourceVersion(request)

		if err != nil {
			writeError(responseWriter, http.StatusBadRequest, err.Error())
			return
		}

		if err := server.etcd.Delete(request.Context(), kind, name, resourceVersion); err != nil {
			writeStoreError(responseWriter, err)
			return
		}

		responseWriter.WriteHeader(http.StatusNoContent)
	default:
		writeError(responseWriter, http.StatusMethodNotAllowed, "method is not allowed for a resource")
	}
}

// etcd の変更を HTTP のストリームとしてクライアントへ渡す
func (server *Server) watch(responseWriter http.ResponseWriter, request *http.Request, resource string) {
	if request.Method != http.MethodGet {
		writeError(responseWriter, http.StatusMethodNotAllowed, "watch only supports GET")
		return
	}
	kind, err := kindForResource(resource)
	if err != nil {
		writeError(responseWriter, http.StatusNotFound, err.Error())
		return
	}

	resourceVersion, err := queryResourceVersion(request)
	if err != nil {
		writeError(responseWriter, http.StatusBadRequest, err.Error())
		return
	}

	events, err := server.etcd.Watch(request.Context(), kind, resourceVersion)
	if err != nil {
		writeStoreError(responseWriter, err)
		return
	}

	responseWriter.Header().Set("Content-Type", "application/x-ndjson")
	responseWriter.WriteHeader(http.StatusOK)
	flusher, canFlush := responseWriter.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}
	encoder := json.NewEncoder(responseWriter)
	for event := range events {
		if event.Err != nil {
			return
		}

		object := newResource(kind)
		if err := json.Unmarshal(event.Object, object); err != nil {
			return
		}
		object.SetResourceVersion(event.ResourceVersion)

		watchEvent := struct {
			Type   etcd.EventType `json:"type"`
			Object api.Resource   `json:"object"`
		}{event.Type, object}
		if err := encoder.Encode(watchEvent); err != nil {
			return
		}

		if canFlush {
			flusher.Flush()
		}
	}
}

// URL に対応する型へ JSON を読み込み、基本的な入力を検証する
func decodeResource(resource string, body io.Reader) (api.Resource, string, error) {
	object := newResource(kindForResourceName(resource))

	if err := json.NewDecoder(body).Decode(object); err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", resource, err)
	}

	name := object.GetName()
	if name == "" {
		return nil, "", errors.New("metadata.name is required")
	}

	if err := validateResource(kindForResourceName(resource), object); err != nil {
		return nil, "", err
	}
	return object, name, nil
}

func kindForResource(resource string) (string, error) {
	kind := kindForResourceName(resource)
	if kind == "" {
		return "", fmt.Errorf("unknown resource %q", resource)
	}
	return kind, nil
}

func kindForResourceName(resource string) string {
	switch resource {
	case "nodes":
		return "Node"
	case "pods":
		return "Pod"
	case "deployments":
		return "Deployment"
	case "replicasets":
		return "ReplicaSet"
	case "services":
		return "Service"
	default:
		return ""
	}
}

func newResource(kind string) api.Resource {
	switch kind {
	case "Node":
		return &api.Node{}
	case "Pod":
		return &api.Pod{}
	case "Deployment":
		return &api.Deployment{}
	case "ReplicaSet":
		return &api.ReplicaSet{}
	case "Service":
		return &api.Service{}
	default:
		return nil
	}
}

func decodeList(kind string, entries []etcd.Entry) ([]api.Resource, error) {
	objects := make([]api.Resource, len(entries))

	for i, entry := range entries {
		object := newResource(kind)
		if err := json.Unmarshal(entry.Object, object); err != nil {
			return nil, fmt.Errorf("decode stored %s: %w", kind, err)
		}
		object.SetResourceVersion(entry.ResourceVersion)
		objects[i] = object
	}

	return objects, nil
}

func validateResource(kind string, object api.Resource) error {
	if kind == "Pod" && len(object.(*api.Pod).Spec.Containers) == 0 {
		return errors.New("spec.containers is required for Pod")
	}
	return nil
}

func queryResourceVersion(request *http.Request) (int64, error) {
	value := request.URL.Query().Get("resourceVersion")
	if value == "" {
		return 0, nil
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 1 {
		return 0, errors.New("resourceVersion must be a positive integer")
	}
	return version, nil
}

func writeStoreError(responseWriter http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, etcd.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, etcd.ErrAlreadyExists), errors.Is(err, etcd.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, context.Canceled):
		return
	}
	writeError(responseWriter, status, err.Error())
}

func writeJSON(responseWriter http.ResponseWriter, status int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(status)
	_ = json.NewEncoder(responseWriter).Encode(value)
}

func writeError(responseWriter http.ResponseWriter, status int, message string) {
	writeJSON(responseWriter, status, map[string]string{"error": message})
}
