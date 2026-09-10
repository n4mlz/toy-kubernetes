package config

const (
	APIServerURL = "http://127.0.0.1:8080"

	ControlPlaneName = "control-plane"
	WorkerNamePrefix = "worker-"

	DefaultWorkerCount = 2
	PodCIDR            = "10.244.0.0/16"
	NodePrefix         = 24
	BridgeName         = "cni0"
	GatewayHost        = 1

	BinaryDir   = ".toy/bin"
	NodeDataDir = ".toy/nodes"
	BundleDir   = "bundles"

	RuntimeBinary        = BinaryDir + "/runtime"
	KubeletBinary        = BinaryDir + "/kubelet"
	NodeSupervisorBinary = BinaryDir + "/node-supervisor"
)
