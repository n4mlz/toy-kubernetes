package kubeproxy

import (
	"testing"

	"toy-kubernetes/api"
)

func TestServiceRulesSelectRunningPods(t *testing.T) {
	services := []api.Service{{
		ObjectMeta: api.ObjectMeta{Name: "web"},
		Spec:       api.ServiceSpec{Selector: map[string]string{"app": "web"}, ClusterIP: "10.96.0.2", Port: 80, TargetPort: 8080},
	}}
	pods := []api.Pod{
		{ObjectMeta: api.ObjectMeta{Name: "ready", Labels: map[string]string{"app": "web"}}, Spec: api.PodSpec{NodeName: "worker-1"}, Status: api.PodStatus{Phase: api.PodRunning, PodIP: "10.244.1.2"}},
		{ObjectMeta: api.ObjectMeta{Name: "pending", Labels: map[string]string{"app": "web"}}, Status: api.PodStatus{Phase: api.PodPending, PodIP: "10.244.1.3"}},
	}

	rules, err := serviceRules(services, pods)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].String() != "10.96.0.2:80 -> 10.244.1.2:8080" {
		t.Fatalf("unexpected endpoint rules: %#v", rules)
	}
}

func TestServiceRulesHaveNoEndpointWhenSelectorDoesNotMatch(t *testing.T) {
	services := []api.Service{{Spec: api.ServiceSpec{Selector: map[string]string{"app": "api"}, ClusterIP: "10.96.0.2", Port: 80, TargetPort: 80}}}
	pods := []api.Pod{{ObjectMeta: api.ObjectMeta{Labels: map[string]string{"app": "web"}}, Status: api.PodStatus{Phase: api.PodRunning, PodIP: "10.244.1.2"}}}

	rules, err := serviceRules(services, pods)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("service without matching endpoints should have no rules: %#v", rules)
	}
}
