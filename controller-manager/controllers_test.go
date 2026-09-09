package controllermanager

import (
	"testing"

	"toy-kubernetes/api"
)

func TestManagedPodsRequireOwnerAndSelectorMatch(t *testing.T) {
	replicaSet := api.ReplicaSet{
		ObjectMeta: api.ObjectMeta{
			Name: "web-rs",
		},
		Spec: api.ReplicaSetSpec{Selector: map[string]string{"app": "web"}},
	}
	pods := []api.Pod{
		{ObjectMeta: api.ObjectMeta{Name: "owned", Labels: map[string]string{"app": "web"}, OwnerReferences: []api.OwnerReference{{Kind: "ReplicaSet", Name: "web-rs"}}}},
		{ObjectMeta: api.ObjectMeta{Name: "unowned", Labels: map[string]string{"app": "web"}}},
		{ObjectMeta: api.ObjectMeta{Name: "different", Labels: map[string]string{"app": "api"}, OwnerReferences: []api.OwnerReference{{Kind: "ReplicaSet", Name: "web-rs"}}}},
	}

	managed := managedPodsForReplicaSet(replicaSet, pods)
	if len(managed) != 1 || managed[0].Name != "owned" {
		t.Fatalf("only owned Pods matching the selector should be managed: %#v", managed)
	}
}

func TestReplicaSetTemplateCreatesStablePodNames(t *testing.T) {
	replicaSet := api.ReplicaSet{ObjectMeta: api.ObjectMeta{Name: "web-rs"}}
	if name := nextPodName(replicaSet, nil); name != "web-rs-1" {
		t.Fatalf("first Pod should use the first stable name: %s", name)
	}
	if name := nextPodName(replicaSet, []api.Pod{{ObjectMeta: api.ObjectMeta{Name: "web-rs-1"}}}); name != "web-rs-2" {
		t.Fatalf("existing Pod names should not be reused: %s", name)
	}
}
