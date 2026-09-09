package kubelet

import (
	"testing"

	"toy-kubernetes/api"
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
