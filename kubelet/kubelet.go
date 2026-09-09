package kubelet

import (
	"context"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
	"toy-kubernetes/cri"
)

type Kubelet struct {
	apiClient *apiserver.Client
	runtime   cri.CRI
	nodeName  string
}

var _ api.Reconciler = (*Kubelet)(nil)

func New(apiClient *apiserver.Client, runtime cri.CRI, nodeName string) *Kubelet {
	return &Kubelet{apiClient: apiClient, runtime: runtime, nodeName: nodeName}
}

// 担当する Node の Pod を CRI で起動・停止し、Pod status を更新する
func (kubelet *Kubelet) Reconcile(ctx context.Context) error {
	pods, err := kubelet.apiClient.Pods().List(ctx)
	if err != nil {
		return err
	}

	containers, err := kubelet.runtime.List(ctx)
	if err != nil {
		return err
	}

	desiredPods := assignedPods(pods.Items, kubelet.nodeName)
	current := containersByPod(containers)

	for _, pod := range desiredPods {
		container, ok := current[pod.Name]
		if !ok || container.State != cri.Running {
			if _, err := kubelet.runtime.Run(ctx, pod); err != nil {
				return err
			}
			if err := kubelet.updateStatus(ctx, pod, api.PodRunning); err != nil {
				return err
			}
			continue
		}

		if pod.Status.Phase != api.PodRunning {
			if err := kubelet.updateStatus(ctx, pod, api.PodRunning); err != nil {
				return err
			}
		}
	}

	for _, container := range containers {
		if _, ok := desiredPods[container.PodName]; !ok && container.State == cri.Running {
			if err := kubelet.runtime.Stop(ctx, container.PodName); err != nil {
				return err
			}
		}
	}

	return nil
}

func (kubelet *Kubelet) updateStatus(ctx context.Context, pod api.Pod, phase api.PodPhase) error {
	pod.Status.Phase = phase
	_, err := kubelet.apiClient.Pods().Update(ctx, pod.Name, pod)
	return err
}

func assignedPods(pods []api.Pod, nodeName string) map[string]api.Pod {
	assigned := make(map[string]api.Pod)
	for _, pod := range pods {
		if pod.Spec.NodeName == nodeName {
			assigned[pod.Name] = pod
		}
	}
	return assigned
}

func containersByPod(containers []cri.Container) map[string]cri.Container {
	current := make(map[string]cri.Container, len(containers))
	for _, container := range containers {
		current[container.PodName] = container
	}
	return current
}
