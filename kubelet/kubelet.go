package kubelet

import (
	"context"
	"log"
	"time"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
	"toy-kubernetes/cri"
)

type Kubelet struct {
	apiClient   *apiserver.Client
	runtime     cri.CRI
	nodeName    string
	manifestDir string
	podCIDR     string
	register    bool
}

var _ api.Reconciler = (*Kubelet)(nil)

func New(apiClient *apiserver.Client, runtime cri.CRI, nodeName string) *Kubelet {
	return &Kubelet{apiClient: apiClient, runtime: runtime, nodeName: nodeName, register: true}
}

func NewWithManifestDir(apiClient *apiserver.Client, runtime cri.CRI, nodeName, manifestDir string) *Kubelet {
	kubelet := New(apiClient, runtime, nodeName)
	kubelet.manifestDir = manifestDir
	return kubelet
}

func NewWithManifestDirAndPodCIDR(apiClient *apiserver.Client, runtime cri.CRI, nodeName, manifestDir, podCIDR string) *Kubelet {
	kubelet := NewWithManifestDir(apiClient, runtime, nodeName, manifestDir)
	kubelet.podCIDR = podCIDR
	return kubelet
}

func (kubelet *Kubelet) SetRegisterNode(register bool) {
	kubelet.register = register
}

// 担当する Node の Pod を CRI で起動・停止し、Pod status を更新する
func (kubelet *Kubelet) Reconcile(ctx context.Context) error {
	if kubelet.manifestDir != "" {
		pods, err := kubelet.reconcileStaticPods(ctx)
		if err != nil {
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

	sandboxes, err := kubelet.runtime.ListSandboxes(ctx)
	if err != nil {
		return err
	}
	staticSandboxIDs := make(map[string]struct{})
	for _, sandbox := range sandboxes {
		if sandbox.Source == cri.StaticPod {
			staticSandboxIDs[sandbox.ID] = struct{}{}
		}
	}

	desiredPods := assignedPods(pods.Items, kubelet.nodeName)
	current := containersByPod(containers)

	for _, pod := range desiredPods {
		container, ok := current[pod.Name]
		if !ok || container.State != cri.Running {
			sandbox, err := kubelet.runPod(ctx, pod)
			if err != nil {
				return err
			}
			pod.Status.PodIP = sandbox.IP
			if err := kubelet.updateStatus(ctx, pod, api.PodRunning); err != nil {
				return err
			}
			log.Printf("started Pod %s on Node %s", pod.Name, kubelet.nodeName)
			continue
		}

		if pod.Status.Phase != api.PodRunning {
			if err := kubelet.updateStatus(ctx, pod, api.PodRunning); err != nil {
				return err
			}
		}
	}

	for _, container := range containers {
		if _, ok := staticSandboxIDs[container.SandboxID]; ok {
			continue
		}
		if _, ok := desiredPods[container.PodName]; !ok && container.State == cri.Running {
			if err := kubelet.runtime.Stop(ctx, container.PodName); err != nil {
				return err
			}
			if container.SandboxID != "" {
				if err := kubelet.runtime.StopPodSandbox(ctx, container.SandboxID); err != nil {
					return err
				}
			}
			log.Printf("stopped Pod %s because it is no longer desired", container.PodName)
		}
	}

	return nil
}

func (kubelet *Kubelet) reconcileStaticPods(ctx context.Context) ([]api.Pod, error) {
	pods, err := LoadStaticPods(kubelet.manifestDir)
	if err != nil {
		return nil, err
	}
	if err := ReconcileStaticPods(ctx, kubelet.runtime, pods); err != nil {
		return nil, err
	}
	return pods, nil
}

// Pod、static manifest、CRI の変更を契機に、担当 Pod の desired state を再確認する
func (kubelet *Kubelet) Run(ctx context.Context) error {
	if err := kubelet.registerNode(ctx); err != nil {
		return err
	}
	if kubelet.manifestDir != "" {
		// control plane は API server 自身を static Pod として起動するため、先に local manifest だけを反映する。
		if _, err := kubelet.reconcileStaticPods(ctx); err != nil {
			return err
		}
	}
	return api.Run(ctx, kubelet, kubelet.watch)
}

func (kubelet *Kubelet) registerNode(ctx context.Context) error {
	if !kubelet.register || kubelet.apiClient == nil || kubelet.podCIDR == "" {
		return nil
	}

	// kubelet 起動時に Node を登録し、scheduler が利用できる状態を作る
	node := api.Node{
		TypeMeta:   api.TypeMeta{APIVersion: api.APIVersionV1, Kind: "Node"},
		ObjectMeta: api.ObjectMeta{Name: kubelet.nodeName},
		Spec:       api.NodeSpec{PodCIDR: kubelet.podCIDR},
		Status:     api.NodeStatus{Phase: api.NodeReady},
	}
	for {
		if current, err := kubelet.apiClient.Nodes().Get(ctx, kubelet.nodeName); err == nil {
			node.ResourceVersion = current.ResourceVersion
			if _, err := kubelet.apiClient.Nodes().Update(ctx, kubelet.nodeName, node); err == nil {
				log.Printf("registered Node %s", kubelet.nodeName)
				return nil
			}
		} else if _, err := kubelet.apiClient.Nodes().Create(ctx, node); err == nil {
			log.Printf("registered Node %s", kubelet.nodeName)
			return nil
		}

		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (kubelet *Kubelet) watch(ctx context.Context) (<-chan error, error) {
	if kubelet.apiClient == nil {
		return api.CombineWatches(ctx, kubelet.watchManifests, kubelet.watchRuntime)
	}

	return api.CombineWatches(ctx, kubelet.watchPods, kubelet.watchRuntime)
}

func (kubelet *Kubelet) watchPods(ctx context.Context) (<-chan error, error) {
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

func (kubelet *Kubelet) runPod(ctx context.Context, pod api.Pod) (cri.Sandbox, error) {
	sandbox, err := kubelet.runtime.RunPodSandbox(ctx, pod, cri.Workload)
	if err != nil {
		return cri.Sandbox{}, err
	}
	if _, err := kubelet.runtime.RunInSandbox(ctx, pod, sandbox.ID); err != nil {
		return cri.Sandbox{}, err
	}
	return sandbox, nil
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
