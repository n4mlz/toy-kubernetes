package api

const APIVersionV1 = "v1"

type TypeMeta struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
}

type ObjectMeta struct {
	Name            string            `json:"name" yaml:"name"`
	UID             string            `json:"uid,omitempty" yaml:"uid,omitempty"`
	ResourceVersion int64             `json:"resourceVersion,omitempty" yaml:"resourceVersion,omitempty"`
	Labels          map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	OwnerReferences []OwnerReference  `json:"ownerReferences,omitempty" yaml:"ownerReferences,omitempty"`
}

type OwnerReference struct {
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
	UID  string `json:"uid" yaml:"uid"`
}

type Node struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec       NodeSpec   `json:"spec" yaml:"spec"`
	Status     NodeStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type NodeSpec struct {
	PodCIDR string `json:"podCIDR" yaml:"podCIDR"`
}

type NodeStatus struct {
	Phase NodePhase `json:"phase,omitempty" yaml:"phase,omitempty"`
}

type NodePhase string

const (
	NodeReady    NodePhase = "Ready"
	NodeNotReady NodePhase = "NotReady"
)

type Pod struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec       PodSpec   `json:"spec" yaml:"spec"`
	Status     PodStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type PodSpec struct {
	NodeName   string      `json:"nodeName,omitempty" yaml:"nodeName,omitempty"`
	Containers []Container `json:"containers" yaml:"containers"`
}

type Container struct {
	Name    string          `json:"name" yaml:"name"`
	Image   string          `json:"image" yaml:"image"`
	Command []string        `json:"command,omitempty" yaml:"command,omitempty"`
	Ports   []ContainerPort `json:"ports,omitempty" yaml:"ports,omitempty"`
}

type ContainerPort struct {
	Name          string `json:"name,omitempty" yaml:"name,omitempty"`
	ContainerPort int    `json:"containerPort" yaml:"containerPort"`
}

type PodStatus struct {
	Phase PodPhase `json:"phase,omitempty" yaml:"phase,omitempty"`
	PodIP string   `json:"podIP,omitempty" yaml:"podIP,omitempty"`
}

type PodPhase string

const (
	PodPending   PodPhase = "Pending"
	PodRunning   PodPhase = "Running"
	PodSucceeded PodPhase = "Succeeded"
	PodFailed    PodPhase = "Failed"
)

type Deployment struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec       DeploymentSpec   `json:"spec" yaml:"spec"`
	Status     DeploymentStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type DeploymentSpec struct {
	Replicas int               `json:"replicas" yaml:"replicas"`
	Selector map[string]string `json:"selector" yaml:"selector"`
	Template PodTemplateSpec   `json:"template" yaml:"template"`
}

type DeploymentStatus struct {
	Replicas int `json:"replicas,omitempty" yaml:"replicas,omitempty"`
}

type ReplicaSet struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec       ReplicaSetSpec   `json:"spec" yaml:"spec"`
	Status     ReplicaSetStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type ReplicaSetSpec struct {
	Replicas int               `json:"replicas" yaml:"replicas"`
	Selector map[string]string `json:"selector" yaml:"selector"`
	Template PodTemplateSpec   `json:"template" yaml:"template"`
}

type ReplicaSetStatus struct {
	Replicas int `json:"replicas,omitempty" yaml:"replicas,omitempty"`
}

type PodTemplateSpec struct {
	ObjectMeta ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec       PodSpec    `json:"spec" yaml:"spec"`
}

type Service struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec       ServiceSpec `json:"spec" yaml:"spec"`
}

type ServiceSpec struct {
	Selector   map[string]string `json:"selector" yaml:"selector"`
	ClusterIP  string            `json:"clusterIP,omitempty" yaml:"clusterIP,omitempty"`
	Port       int               `json:"port" yaml:"port"`
	TargetPort int               `json:"targetPort" yaml:"targetPort"`
}
