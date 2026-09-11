package scheduler

import (
	"context"
	"log"

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
	// control plane は Node resource として登録しないため、登録済みの Ready な Node は worker とみなす

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
		log.Printf("bound Pod %s to Node %s", pod.Name, node.Name)
		scheduler.nextNode = nextNode
	}

	return nil
}

func (scheduler *Scheduler) Run(ctx context.Context) error {
	// Pod または Node の変更を契機に、未割り当て Pod の bind を再確認する
	return api.Run(ctx, scheduler, scheduler.watch)
}

func (scheduler *Scheduler) watch(ctx context.Context) (<-chan error, error) {
	return api.CombineWatches(ctx, scheduler.watchNodes, scheduler.watchPods)
}

func (scheduler *Scheduler) watchNodes(ctx context.Context) (<-chan error, error) {
	nodes, err := scheduler.apiClient.Nodes().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := scheduler.apiClient.Nodes().Watch(ctx, nodes.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
}

func (scheduler *Scheduler) watchPods(ctx context.Context) (<-chan error, error) {
	pods, err := scheduler.apiClient.Pods().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := scheduler.apiClient.Pods().Watch(ctx, pods.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
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
