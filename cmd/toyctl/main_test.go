package main

import (
	"testing"

	"toy-kubernetes/api"
)

func TestDecodeManifestKeepsTheResourceKindAndName(t *testing.T) {
	object, err := decodeManifest(map[string]any{
		"apiVersion": api.APIVersionV1,
		"kind":       "Pod",
		"metadata": map[string]any{
			"name": "nginx",
		},
		"spec": map[string]any{
			"containers": []any{map[string]any{"name": "nginx", "image": "nginx"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	pod, ok := object.(*api.Pod)
	if !ok || pod.Name != "nginx" || len(pod.Spec.Containers) != 1 {
		t.Fatalf("manifest should decode into the requested resource: %#v", object)
	}
}

func TestSplitResourceAcceptsResourceAndName(t *testing.T) {
	kind, name, err := splitResource("pods/nginx")
	if err != nil {
		t.Fatal(err)
	}
	if kind != "Pod" || name != "nginx" {
		t.Fatalf("resource reference should be split into kind and name: %s/%s", kind, name)
	}
}
