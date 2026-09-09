package scheduler

import (
	"context"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
)

type Scheduler struct {
	apiClient *apiserver.Client
	nextNode  int
}

var _ api.Reconciler = (*Scheduler)(nil)

func NewScheduler(apiClient *apiserver.Client) *Scheduler {
	return &Scheduler{apiClient: apiClient}
}

// 未割り当て Pod に Ready な Node を選び、bind 結果を API server に保存する
func (scheduler *Scheduler) Reconcile(ctx context.Context) error {
	nodes, err := scheduler.apiClient.Nodes().List(ctx)
	if err != nil {
		return err
	}
	// TODO: 現在は control plane Node を登録していないため、Ready な Node を worker とみなす。
	// 正しくは role label や taint を見て、通常の Pod を worker にだけ割り当てる。

	pods, err := scheduler.apiClient.Pods().List(ctx)
	if err != nil {
		return err
	}

	for index := range pods.Items {
		pod := pods.Items[index]
		if pod.Spec.NodeName != "" {
			continue
		}

		node, nextNode, found := nextReadyNode(nodes.Items, scheduler.nextNode)
		if !found {
			return nil
		}

		pod.Spec.NodeName = node.Name
		if _, err := scheduler.apiClient.Pods().Update(ctx, pod.Name, pod); err != nil {
			return err
		}
		scheduler.nextNode = nextNode
	}

	return nil
}

func nextReadyNode(nodes []api.Node, start int) (api.Node, int, bool) {
	if len(nodes) == 0 {
		return api.Node{}, 0, false
	}

	for offset := 0; offset < len(nodes); offset++ {
		index := (start + offset) % len(nodes)
		if nodes[index].Status.Phase == api.NodeReady {
			return nodes[index], (index + 1) % len(nodes), true
		}
	}

	return api.Node{}, start, false
}
