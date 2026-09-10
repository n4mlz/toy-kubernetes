package controllermanager

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
)

// Deployment と ReplicaSet の controller を同じ controller-manager で起動する
func Run(ctx context.Context, client *apiserver.Client) error {
	deployment := NewDeploymentController(client)
	replicaSet := NewReplicaSetController(client)
	errors := make(chan error, 2)
	go func() { errors <- deployment.Run(ctx) }()
	go func() { errors <- replicaSet.Run(ctx) }()

	for range 2 {
		if err := <-errors; err != nil {
			return err
		}
	}
	return nil
}

type DeploymentController struct {
	apiClient *apiserver.Client
}

var _ api.Reconciler = (*DeploymentController)(nil)

func NewDeploymentController(client *apiserver.Client) *DeploymentController {
	return &DeploymentController{apiClient: client}
}

// Deployment の desired state に対応する ReplicaSet を作成・更新する
func (controller *DeploymentController) Reconcile(ctx context.Context) error {
	deployments, err := controller.apiClient.Deployments().List(ctx)
	if err != nil {
		return err
	}

	replicaSets, err := controller.apiClient.ReplicaSets().List(ctx)
	if err != nil {
		return err
	}

	for _, deployment := range deployments.Items {
		// Deployment が管理する ReplicaSet を見つけ、なければ作成する
		replicaSet := replicaSetForDeployment(deployment, replicaSets.Items)
		if replicaSet == nil {
			if _, err := controller.apiClient.ReplicaSets().Create(ctx, newReplicaSet(deployment)); err != nil {
				return err
			}
			continue
		}

		desired := newReplicaSet(deployment)
		desired.ResourceVersion = replicaSet.ResourceVersion
		desired.Status = replicaSet.Status
		if reflect.DeepEqual(replicaSet.Spec, desired.Spec) && reflect.DeepEqual(replicaSet.OwnerReferences, desired.OwnerReferences) {
			continue
		}

		if _, err := controller.apiClient.ReplicaSets().Update(ctx, replicaSet.Name, desired); err != nil {
			return err
		}
	}

	return nil
}

func (controller *DeploymentController) Run(ctx context.Context) error {
	// Deployment または ReplicaSet の変更を契機に、管理対象の ReplicaSet を再確認する
	return api.Run(ctx, controller, controller.watch)
}

func (controller *DeploymentController) watch(ctx context.Context) (<-chan error, error) {
	return api.CombineWatches(ctx, controller.watchDeployments, controller.watchReplicaSets)
}

func (controller *DeploymentController) watchDeployments(ctx context.Context) (<-chan error, error) {
	deployments, err := controller.apiClient.Deployments().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := controller.apiClient.Deployments().Watch(ctx, deployments.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
}

func (controller *DeploymentController) watchReplicaSets(ctx context.Context) (<-chan error, error) {
	replicaSets, err := controller.apiClient.ReplicaSets().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := controller.apiClient.ReplicaSets().Watch(ctx, replicaSets.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
}

type ReplicaSetController struct {
	apiClient *apiserver.Client
}

var _ api.Reconciler = (*ReplicaSetController)(nil)

func NewReplicaSetController(client *apiserver.Client) *ReplicaSetController {
	return &ReplicaSetController{apiClient: client}
}

// ReplicaSet が管理する Pod 数を desired replicas に一致させる
func (controller *ReplicaSetController) Reconcile(ctx context.Context) error {
	replicaSets, err := controller.apiClient.ReplicaSets().List(ctx)
	if err != nil {
		return err
	}

	pods, err := controller.apiClient.Pods().List(ctx)
	if err != nil {
		return err
	}

	for _, replicaSet := range replicaSets.Items {
		// owner reference と selector の両方で、この ReplicaSet の Pod だけを数える
		managedPods := managedPodsForReplicaSet(replicaSet, pods.Items)
		if err := controller.reconcilePods(ctx, replicaSet, managedPods); err != nil {
			return err
		}
	}

	return nil
}

func (controller *ReplicaSetController) Run(ctx context.Context) error {
	// ReplicaSet または Pod の変更を契機に、Pod 数と status を再確認する
	return api.Run(ctx, controller, controller.watch)
}

func (controller *ReplicaSetController) watch(ctx context.Context) (<-chan error, error) {
	return api.CombineWatches(ctx, controller.watchReplicaSets, controller.watchPods)
}

func (controller *ReplicaSetController) watchReplicaSets(ctx context.Context) (<-chan error, error) {
	replicaSets, err := controller.apiClient.ReplicaSets().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := controller.apiClient.ReplicaSets().Watch(ctx, replicaSets.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
}

func (controller *ReplicaSetController) watchPods(ctx context.Context) (<-chan error, error) {
	pods, err := controller.apiClient.Pods().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := controller.apiClient.Pods().Watch(ctx, pods.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
}

// ReplicaSet の管理対象の Pod を増減させ、desired replicas に収束させる
func (controller *ReplicaSetController) reconcilePods(ctx context.Context, replicaSet api.ReplicaSet, managedPods []api.Pod) error {
	sort.Slice(managedPods, func(i, j int) bool {
		return managedPods[i].Name < managedPods[j].Name
	})

	desiredReplicas := replicaSet.Spec.Replicas
	if desiredReplicas < 0 {
		desiredReplicas = 0
	}

	for index := len(managedPods); index < desiredReplicas; index++ {
		// 既存 Pod の名前を再利用し、reconcile を繰り返しても重複を作らない
		name := nextPodName(replicaSet, managedPods)
		managedPods = append(managedPods, api.Pod{ObjectMeta: api.ObjectMeta{Name: name}})

		if _, err := controller.apiClient.Pods().Create(ctx, newPod(replicaSet, name)); err != nil {
			return err
		}
	}

	for index := desiredReplicas; index < len(managedPods); index++ {
		// desired replicas を超えた Pod は末尾から削除する
		pod := managedPods[index]
		if err := controller.apiClient.Pods().Delete(ctx, pod.Name, pod.ResourceVersion); err != nil {
			return err
		}
	}

	if replicaSet.Status.Replicas == desiredReplicas {
		return nil
	}

	replicaSet.Status.Replicas = desiredReplicas
	_, err := controller.apiClient.ReplicaSets().Update(ctx, replicaSet.Name, replicaSet)
	return err
}

func replicaSetForDeployment(deployment api.Deployment, replicaSets []api.ReplicaSet) *api.ReplicaSet {
	for index := range replicaSets {
		replicaSet := &replicaSets[index]
		if hasOwner(replicaSet.OwnerReferences, "Deployment", deployment.Name, deployment.UID) {
			return replicaSet
		}
	}
	return nil
}

func managedPodsForReplicaSet(replicaSet api.ReplicaSet, pods []api.Pod) []api.Pod {
	managed := make([]api.Pod, 0)
	for _, pod := range pods {
		if hasOwner(pod.OwnerReferences, "ReplicaSet", replicaSet.Name, replicaSet.UID) && api.LabelsMatch(replicaSet.Spec.Selector, pod.Labels) {
			managed = append(managed, pod)
		}
	}
	return managed
}

func hasOwner(owners []api.OwnerReference, kind, name, uid string) bool {
	for _, owner := range owners {
		if owner.Kind == kind && owner.Name == name && (uid == "" || owner.UID == uid) {
			return true
		}
	}
	return false
}

func newReplicaSet(deployment api.Deployment) api.ReplicaSet {
	// Deployment の template と selector を ReplicaSet の desired state に引き継ぐ
	return api.ReplicaSet{
		TypeMeta: api.TypeMeta{APIVersion: api.APIVersionV1, Kind: "ReplicaSet"},
		ObjectMeta: api.ObjectMeta{
			Name:            deployment.Name + "-rs",
			Labels:          copyLabels(deployment.Spec.Template.ObjectMeta.Labels),
			OwnerReferences: []api.OwnerReference{{Kind: "Deployment", Name: deployment.Name, UID: deployment.UID}},
		},
		Spec: api.ReplicaSetSpec{
			Replicas: deployment.Spec.Replicas,
			Selector: copyLabels(deployment.Spec.Selector),
			Template: deployment.Spec.Template,
		},
	}
}

func newPod(replicaSet api.ReplicaSet, name string) api.Pod {
	// ReplicaSet の template を Pod に展開し、親子関係を owner reference に残す
	template := replicaSet.Spec.Template
	return api.Pod{
		TypeMeta: api.TypeMeta{APIVersion: api.APIVersionV1, Kind: "Pod"},
		ObjectMeta: api.ObjectMeta{
			Name:            name,
			Labels:          copyLabels(template.ObjectMeta.Labels),
			OwnerReferences: []api.OwnerReference{{Kind: "ReplicaSet", Name: replicaSet.Name, UID: replicaSet.UID}},
		},
		Spec: template.Spec,
	}
}

func nextPodName(replicaSet api.ReplicaSet, pods []api.Pod) string {
	used := make(map[string]bool, len(pods))
	for _, pod := range pods {
		used[pod.Name] = true
	}

	for index := 1; ; index++ {
		name := fmt.Sprintf("%s-%d", replicaSet.Name, index)
		if !used[name] {
			return name
		}
	}
}

func copyLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}

	copy := make(map[string]string, len(labels))
	for key, value := range labels {
		copy[key] = value
	}
	return copy
}
