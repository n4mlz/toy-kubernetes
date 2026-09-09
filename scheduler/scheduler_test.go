package scheduler

import (
	"testing"

	"toy-kubernetes/api"
)

func TestNextReadyNodeUsesRoundRobinAndSkipsNotReadyNodes(t *testing.T) {
	nodes := []api.Node{
		{ObjectMeta: api.ObjectMeta{Name: "worker-1"}, Status: api.NodeStatus{Phase: api.NodeNotReady}},
		{ObjectMeta: api.ObjectMeta{Name: "worker-2"}, Status: api.NodeStatus{Phase: api.NodeReady}},
		{ObjectMeta: api.ObjectMeta{Name: "worker-3"}, Status: api.NodeStatus{Phase: api.NodeReady}},
	}

	first, next, found := nextReadyNode(nodes, 0)
	if !found || first.Name != "worker-2" || next != 2 {
		t.Fatalf("scheduler should choose the first Ready Node: %#v, next=%d", first, next)
	}

	second, next, found := nextReadyNode(nodes, next)
	if !found || second.Name != "worker-3" || next != 0 {
		t.Fatalf("scheduler should continue round-robin across Ready Nodes: %#v, next=%d", second, next)
	}
}

func TestNextReadyNodeLeavesPodUnscheduledWhenNoNodeIsReady(t *testing.T) {
	nodes := []api.Node{{Status: api.NodeStatus{Phase: api.NodeNotReady}}}

	if _, _, found := nextReadyNode(nodes, 0); found {
		t.Fatal("scheduler should not bind a Pod when no Node is Ready")
	}
}
