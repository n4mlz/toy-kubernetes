package kubelet

import (
	"testing"

	"toy-kubernetes/api"
	"toy-kubernetes/cri"
)

func TestAssignedPodsKeepsOnlyPodsForThisNode(t *testing.T) {
	pods := []api.Pod{
		{ObjectMeta: api.ObjectMeta{Name: "on-worker-1"}, Spec: api.PodSpec{NodeName: "worker-1"}},
		{ObjectMeta: api.ObjectMeta{Name: "on-worker-2"}, Spec: api.PodSpec{NodeName: "worker-2"}},
		{ObjectMeta: api.ObjectMeta{Name: "pending"}},
	}

	assigned := assignedPods(pods, "worker-1")
	if len(assigned) != 1 || assigned["on-worker-1"].Name != "on-worker-1" {
		t.Fatalf("kubelet should observe only Pods assigned to its Node: %#v", assigned)
	}
}

func TestContainersByPodMakesRuntimeStateEasyToCompare(t *testing.T) {
	containers := containersByPod([]cri.Container{
		{PodName: "nginx", State: cri.Running},
		{PodName: "sidecar", State: cri.Stopped},
	})

	if containers["nginx"].State != cri.Running || containers["sidecar"].State != cri.Stopped {
		t.Fatalf("runtime state should be indexed by Pod name: %#v", containers)
	}
}
