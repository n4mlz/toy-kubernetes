package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPodKeepsItsMeaningThroughJSONAndYAML(t *testing.T) {
	original := Pod{
		TypeMeta: TypeMeta{APIVersion: APIVersionV1, Kind: "Pod"},
		ObjectMeta: ObjectMeta{
			Name:   "nginx-1",
			Labels: map[string]string{"app": "nginx"},
		},
		Spec: PodSpec{
			NodeName: "worker-1",
			Containers: []Container{{
				Name:  "nginx",
				Image: "nginx",
				Ports: []ContainerPort{{ContainerPort: 80}},
			}},
		},
		Status: PodStatus{Phase: PodRunning, PodIP: "pod-ip"},
	}

	t.Run("JSON", func(t *testing.T) {
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"apiVersion":"v1"`) || !strings.Contains(string(data), `"nodeName":"worker-1"`) {
			t.Fatalf("JSON should use the Kubernetes field names: %s", data)
		}

		var decoded Pod
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(decoded, original) {
			t.Fatalf("decoded Pod differs from original:\nwant: %#v\ngot:  %#v", original, decoded)
		}
	})

	t.Run("YAML", func(t *testing.T) {
		data, err := yaml.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "apiVersion: v1") || !strings.Contains(string(data), "nodeName: worker-1") {
			t.Fatalf("YAML should use the Kubernetes field names: %s", data)
		}

		var decoded Pod
		if err := yaml.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(decoded, original) {
			t.Fatalf("decoded Pod differs from original:\nwant: %#v\ngot:  %#v", original, decoded)
		}
	})
}

func TestLabelsMatchWhenEverySelectorLabelIsPresent(t *testing.T) {
	labels := map[string]string{"app": "nginx", "tier": "frontend"}

	if !LabelsMatch(map[string]string{"app": "nginx"}, labels) {
		t.Fatal("a selector matching the app label should match")
	}
	if LabelsMatch(map[string]string{"app": "api"}, labels) {
		t.Fatal("a selector with a different app label should not match")
	}
	if LabelsMatch(map[string]string{"missing": "label"}, labels) {
		t.Fatal("a selector with a missing label should not match")
	}
}

func TestEmptySelectorMatchesAnyLabels(t *testing.T) {
	if !LabelsMatch(nil, map[string]string{"app": "nginx"}) {
		t.Fatal("an empty selector should match labels")
	}
}

func TestGeneratedNamesKeepTheirPrefixAndAreDistinct(t *testing.T) {
	first, err := GenerateName("nginx-")
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateName("nginx-")
	if err != nil {
		t.Fatal(err)
	}

	if len(first) <= len("nginx-") || len(second) <= len("nginx-") {
		t.Fatalf("generated names should contain a suffix: %q, %q", first, second)
	}
	if first[:len("nginx-")] != "nginx-" || second[:len("nginx-")] != "nginx-" {
		t.Fatalf("generated names should keep their prefix: %q, %q", first, second)
	}
	if first == second {
		t.Fatalf("generated names should be distinct: %q", first)
	}
}
