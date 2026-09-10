package config

const (
	APIServerURL = "http://10.200.0.2:8080"

	ControlPlaneName = "control-plane"
	WorkerNamePrefix = "worker-"

	DefaultWorkerCount  = 2
	PodCIDR             = "10.244.0.0/16"
	NodePrefix          = 24
	BridgeName          = "cni0"
	GatewayHost         = 1
	ServiceCIDR         = "10.96.0.0/24"
	KubeProxyTable      = "toy_kube_proxy"
	UnderlayBridge      = "toy-underlay0"
	UnderlayInterface   = "underlay0"
	UnderlayCIDR        = "10.200.0.0/24"
	FirstNodeHost       = 2
	UnderlayGatewayHost = 1

	BinaryDir               = ".toy/bin"
	NodeDataDir             = ".toy/nodes"
	BundleDir               = "bundles"
	ControlPlaneManifestDir = "node/manifests/control-plane"

	RuntimeBinary        = BinaryDir + "/runtime"
	KubeletBinary        = BinaryDir + "/kubelet"
	KubeProxyBinary      = BinaryDir + "/kube-proxy"
	NodeSupervisorBinary = BinaryDir + "/node-supervisor"
)
