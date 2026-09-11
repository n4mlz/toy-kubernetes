package kubelet

import (
	"context"
	"fmt"
	"log"
	"reflect"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
)

const (
	mirrorAnnotation = "kubernetes.io/config.mirror"
	sourceAnnotation = "kubernetes.io/config.source"
)

// local Static Pod を API server から見える Pod として登録する
func (kubelet *Kubelet) reconcileMirrorPods(ctx context.Context, staticPods []api.Pod) error {
	pods, err := kubelet.apiClient.Pods().List(ctx)
	if err != nil {
		return fmt.Errorf("list mirror Pods: %w", err)
	}

	current := make(map[string]api.Pod)
	for _, pod := range pods.Items {
		if isMirrorPod(pod) {
			current[pod.Name] = pod
		}
	}

	desired := make(map[string]api.Pod, len(staticPods))
	for _, staticPod := range staticPods {
		mirror := mirrorPod(staticPod, kubelet.nodeName)
		desired[mirror.Name] = mirror

		existing, ok := current[mirror.Name]
		if !ok {
			if _, err := kubelet.apiClient.Pods().Create(ctx, mirror); err != nil {
				return fmt.Errorf("create mirror Pod %s: %w", mirror.Name, err)
			}
			log.Printf("created mirror Pod %s", mirror.Name)
			continue
		}

		mirror.ResourceVersion = existing.ResourceVersion
		if reflect.DeepEqual(existing.Spec, mirror.Spec) && reflect.DeepEqual(existing.Annotations, mirror.Annotations) {
			continue
		}
		if _, err := kubelet.apiClient.Pods().Update(ctx, existing.Name, mirror); err != nil {
			return fmt.Errorf("update mirror Pod %s: %w", mirror.Name, err)
		}
	}

	for name, pod := range current {
		if _, ok := desired[name]; ok {
			continue
		}
		if err := kubelet.apiClient.Pods().Delete(ctx, pod.Name, pod.ResourceVersion); err != nil && !apiserver.IsNotFound(err) {
			return fmt.Errorf("delete mirror Pod %s: %w", pod.Name, err)
		}
		log.Printf("deleted mirror Pod %s", pod.Name)
	}
	return nil
}

func mirrorPod(staticPod api.Pod, nodeName string) api.Pod {
	staticName := staticPod.Name
	staticPod.Name = staticName + "-" + nodeName
	staticPod.Spec.NodeName = nodeName
	staticPod.Status = api.PodStatus{}
	staticPod.Annotations = copyAnnotations(staticPod.Annotations)
	staticPod.Annotations[mirrorAnnotation] = staticName
	staticPod.Annotations[sourceAnnotation] = "file"
	return staticPod
}

func isMirrorPod(pod api.Pod) bool {
	_, ok := pod.Annotations[mirrorAnnotation]
	return ok
}

func copyAnnotations(annotations map[string]string) map[string]string {
	result := make(map[string]string, len(annotations)+2)
	for key, value := range annotations {
		result[key] = value
	}
	return result
}
