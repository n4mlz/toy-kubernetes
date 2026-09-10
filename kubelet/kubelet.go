package kubelet

import (
	"context"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
	"toy-kubernetes/cri"
)

type Kubelet struct {
	apiClient   *apiserver.Client
	runtime     cri.CRI
	nodeName    string
	manifestDir string
}

var _ api.Reconciler = (*Kubelet)(nil)

func New(apiClient *apiserver.Client, runtime cri.CRI, nodeName string) *Kubelet {
	return &Kubelet{apiClient: apiClient, runtime: runtime, nodeName: nodeName}
}

func NewWithManifestDir(apiClient *apiserver.Client, runtime cri.CRI, nodeName, manifestDir string) *Kubelet {
	kubelet := New(apiClient, runtime, nodeName)
	kubelet.manifestDir = manifestDir
	return kubelet
}

// 担当する Node の Pod を CRI で起動・停止し、Pod status を更新する
func (kubelet *Kubelet) Reconcile(ctx context.Context) error {
	if kubelet.manifestDir != "" {
		pods, err := LoadStaticPods(kubelet.manifestDir)
		if err != nil {
			return err
		}
		if err := ReconcileStaticPods(ctx, kubelet.runtime, pods); err != nil {
			return err
		}
		if kubelet.apiClient != nil {
			if err := kubelet.reconcileMirrorPods(ctx, pods); err != nil {
				return err
			}
		}
	}

	if kubelet.apiClient == nil {
		return nil
	}

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
			if err := kubelet.runPod(ctx, pod); err != nil {
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
			if container.SandboxID != "" {
				if err := kubelet.runtime.StopPodSandbox(ctx, container.SandboxID); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Pod、static manifest、CRI の変更を契機に、担当 Pod の desired state を再確認する
func (kubelet *Kubelet) Run(ctx context.Context) error {
	return api.Run(ctx, kubelet, kubelet.watch)
}

func (kubelet *Kubelet) watch(ctx context.Context) (<-chan error, error) {
	if kubelet.apiClient == nil {
		return api.CombineWatches(ctx, kubelet.watchManifests, kubelet.watchRuntime)
	}

	pods, err := kubelet.apiClient.Pods().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := kubelet.apiClient.Pods().Watch(ctx, pods.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
}

func (kubelet *Kubelet) watchManifests(ctx context.Context) (<-chan error, error) {
	changes, err := WatchStaticPods(ctx, kubelet.manifestDir)
	if err != nil {
		return nil, err
	}
	return changeErrors(ctx, changes), nil
}

func (kubelet *Kubelet) watchRuntime(ctx context.Context) (<-chan error, error) {
	events, err := kubelet.runtime.Watch(ctx)
	if err != nil {
		return nil, err
	}
	return criEventErrors(ctx, events), nil
}

func changeErrors(ctx context.Context, changes <-chan struct{}) <-chan error {
	errors := make(chan error, 1)
	go func() {
		defer close(errors)
		select {
		case <-changes:
			errors <- nil
		case <-ctx.Done():
		}
	}()
	return errors
}

func criEventErrors(ctx context.Context, events <-chan cri.Event) <-chan error {
	errors := make(chan error, 1)
	go func() {
		defer close(errors)
		select {
		case _, ok := <-events:
			if ok {
				errors <- nil
			}
		case <-ctx.Done():
		}
	}()
	return errors
}

func (kubelet *Kubelet) runPod(ctx context.Context, pod api.Pod) error {
	sandbox, err := kubelet.runtime.RunPodSandbox(ctx, pod, cri.Workload)
	if err != nil {
		return err
	}
	_, err = kubelet.runtime.RunInSandbox(ctx, pod, sandbox.ID)
	return err
}

func (kubelet *Kubelet) updateStatus(ctx context.Context, pod api.Pod, phase api.PodPhase) error {
	pod.Status.Phase = phase
	_, err := kubelet.apiClient.Pods().Update(ctx, pod.Name, pod)
	return err
}

func assignedPods(pods []api.Pod, nodeName string) map[string]api.Pod {
	assigned := make(map[string]api.Pod)
	for _, pod := range pods {
		if pod.Spec.NodeName == nodeName && !isMirrorPod(pod) {
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
