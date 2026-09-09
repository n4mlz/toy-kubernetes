package kubelet

import (
	"testing"

	"toy-kubernetes/api"
)

func TestMirrorPodIsVisibleButNotManagedAsANormalPod(t *testing.T) {
	staticPod := api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name: "api-server",
			Annotations: map[string]string{
				"example": "kept",
			},
		},
		Spec: api.PodSpec{NodeName: "control-plane"},
	}

	mirror := mirrorPod(staticPod, "control-plane")
	if mirror.Name != "api-server-control-plane" || !isMirrorPod(mirror) {
		t.Fatalf("static Pod should become a mirror Pod: %#v", mirror)
	}
	if mirror.Annotations["example"] != "kept" || mirror.Annotations[sourceAnnotation] != "file" {
		t.Fatalf("mirror Pod should preserve labels: %#v", mirror.Annotations)
	}
	if assigned := assignedPods([]api.Pod{mirror}, "control-plane"); len(assigned) != 0 {
		t.Fatalf("mirror Pod should not become a normal workload: %#v", assigned)
	}
}
