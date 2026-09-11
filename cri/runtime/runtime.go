package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"toy-kubernetes/cni"
	"toy-kubernetes/cri"

	"golang.org/x/sys/unix"
)

// Runtime は node 上で container process を起動し、CRI 用に socket を提供する
type Runtime struct {
	socketPath string
	bundleDir  string
	network    cni.CNI

	mu         sync.Mutex
	containers map[string]*managedContainer
	sandboxes  map[string]*managedSandbox
	watchers   map[chan cri.Event]struct{}
}

type managedSandbox struct {
	sandbox     cri.Sandbox
	process     *os.Process
	done        chan struct{}
	netnsPath   string
	hostNetwork bool
}

type managedContainer struct {
	container cri.Container
	process   *os.Process
	done      chan struct{}
	sandboxID string
	rootfs    string
}

type request struct {
	Operation   string            `json:"op"`
	Pod         string            `json:"pod,omitempty"`
	Image       string            `json:"image,omitempty"`
	Command     []string          `json:"command,omitempty"`
	SandboxID   string            `json:"sandboxID,omitempty"`
	Source      cri.SandboxSource `json:"source,omitempty"`
	HostNetwork bool              `json:"hostNetwork,omitempty"`
}

type response struct {
	OK         bool              `json:"ok"`
	Error      string            `json:"error,omitempty"`
	ID         string            `json:"id,omitempty"`
	Pod        string            `json:"pod,omitempty"`
	State      cri.State         `json:"state,omitempty"`
	SandboxID  string            `json:"sandboxID,omitempty"`
	Source     cri.SandboxSource `json:"source,omitempty"`
	IP         string            `json:"ip,omitempty"`
	Containers []cri.Container   `json:"containers,omitempty"`
	Sandboxes  []cri.Sandbox     `json:"sandboxes,omitempty"`
	EventType  string            `json:"eventType,omitempty"`
	Container  cri.Container     `json:"container,omitempty"`
	Sandbox    cri.Sandbox       `json:"sandbox,omitempty"`
}

type bundleConfig struct {
	Root    bundleRoot    `json:"root"`
	Process bundleProcess `json:"process"`
}

type bundleRoot struct {
	Path string `json:"path"`
}

type bundleProcess struct {
	Args []string `json:"args"`
}

func NewRuntime(socketPath, bundleDir string) *Runtime {
	return &Runtime{
		socketPath: socketPath,
		bundleDir:  bundleDir,
		containers: make(map[string]*managedContainer),
		sandboxes:  make(map[string]*managedSandbox),
		watchers:   make(map[chan cri.Event]struct{}),
	}
}

func NewRuntimeWithNetwork(socketPath, bundleDir string, network cni.CNI) *Runtime {
	runtime := NewRuntime(socketPath, bundleDir)
	runtime.network = network
	return runtime
}

// Unix socket で request を受け付け、container lifecycle を管理する
func (runtime *Runtime) Serve(ctx context.Context) error {
	if err := os.Remove(runtime.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove CRI socket: %w", err)
	}

	listener, err := net.Listen("unix", runtime.socketPath)
	if err != nil {
		return fmt.Errorf("listen on CRI socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(runtime.socketPath)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept CRI connection: %w", err)
		}

		go runtime.handleConnection(connection)
	}
}

func (runtime *Runtime) handleConnection(connection net.Conn) {
	defer connection.Close()

	var input request
	if err := json.NewDecoder(connection).Decode(&input); err != nil {
		writeResponse(connection, response{Error: "invalid CRI request"})
		return
	}
	if input.Operation == "watch" {
		runtime.watch(connection)
		return
	}

	result, err := runtime.handleRequest(input)
	if err != nil {
		writeResponse(connection, response{Error: err.Error()})
		return
	}

	result.OK = true
	writeResponse(connection, result)
}

func (runtime *Runtime) watch(connection net.Conn) {
	events := make(chan cri.Event, 1)
	runtime.mu.Lock()
	runtime.watchers[events] = struct{}{}
	runtime.mu.Unlock()
	defer func() {
		runtime.mu.Lock()
		delete(runtime.watchers, events)
		runtime.mu.Unlock()
		close(events)
	}()

	if err := json.NewEncoder(connection).Encode(response{OK: true}); err != nil {
		return
	}
	encoder := json.NewEncoder(connection)
	for event := range events {
		if err := encoder.Encode(response{
			OK:        true,
			EventType: event.Type,
			Container: event.Container,
			Sandbox:   event.Sandbox,
		}); err != nil {
			return
		}
	}
}

func (runtime *Runtime) publish(event cri.Event) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	for events := range runtime.watchers {
		// List が正本なので、連続した状態変更は一つにまとめても再同期できる
		select {
		case events <- event:
		default:
		}
	}
}

func writeResponse(connection net.Conn, result response) {
	_ = json.NewEncoder(connection).Encode(result)
}

func (runtime *Runtime) handleRequest(input request) (response, error) {
	switch input.Operation {
	case "list":
		return runtime.list(), nil
	case "list-sandboxes":
		return runtime.listSandboxes(), nil
	case "run-pod-sandbox":
		sandbox, err := runtime.runPodSandbox(input.Pod, input.Source, input.HostNetwork)
		if err != nil {
			return response{}, err
		}
		return response{SandboxID: sandbox.ID, Pod: sandbox.PodName, Source: sandbox.Source, State: sandbox.State, IP: sandbox.IP}, nil
	case "run-in-sandbox":
		container, err := runtime.runInSandbox(input.Pod, input.Image, input.Command, input.SandboxID)
		if err != nil {
			return response{}, err
		}
		return response{ID: container.ID, Pod: container.PodName, State: container.State, SandboxID: container.SandboxID}, nil
	case "stop-pod-sandbox":
		if err := runtime.stopPodSandbox(input.SandboxID); err != nil {
			return response{}, err
		}
		return response{SandboxID: input.SandboxID, State: cri.Stopped}, nil
	case "stop":
		if err := runtime.stop(input.Pod); err != nil {
			return response{}, err
		}
		return response{Pod: input.Pod, State: cri.Stopped}, nil
	case "inspect":
		container, err := runtime.inspect(input.Pod)
		if err != nil {
			return response{}, err
		}
		return response{ID: container.ID, Pod: container.PodName, State: container.State}, nil
	default:
		return response{}, errors.New("invalid CRI operation")
	}
}

func (runtime *Runtime) list() response {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	containers := make([]cri.Container, 0, len(runtime.containers))
	for _, managed := range runtime.containers {
		containers = append(containers, managed.container)
	}
	return response{Containers: containers}
}

func (runtime *Runtime) listSandboxes() response {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	sandboxes := make([]cri.Sandbox, 0, len(runtime.sandboxes))
	for _, managed := range runtime.sandboxes {
		sandboxes = append(sandboxes, managed.sandbox)
	}
	return response{Sandboxes: sandboxes}
}

func (runtime *Runtime) runPodSandbox(podName string, source cri.SandboxSource, hostNetwork bool) (cri.Sandbox, error) {
	runtime.mu.Lock()
	for _, sandbox := range runtime.sandboxes {
		if sandbox.sandbox.PodName == podName && sandbox.sandbox.Source == source && sandbox.sandbox.State == cri.Running {
			runtime.mu.Unlock()
			return sandbox.sandbox, nil
		}
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		runtime.mu.Unlock()
		return cri.Sandbox{}, err
	}
	command := exec.Command(os.Args[0], "--sandbox-child", "--network-ready-fd", "3")
	command.ExtraFiles = []*os.File{reader}
	cloneFlags := uintptr(unix.CLONE_NEWPID | unix.CLONE_NEWNS | unix.CLONE_NEWUTS)
	if !hostNetwork {
		cloneFlags |= unix.CLONE_NEWNET
	}
	command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: cloneFlags}
	if err := command.Start(); err != nil {
		runtime.mu.Unlock()
		return cri.Sandbox{}, err
	}
	_ = reader.Close()
	netnsPath := fmt.Sprintf("/proc/%d/ns/net", command.Process.Pid)
	result := cni.Result{}
	if runtime.network != nil {
		if !hostNetwork {
			result, err = runtime.network.Add(context.Background(), podName, netnsPath)
			if err != nil {
				_ = command.Process.Kill()
				runtime.mu.Unlock()
				return cri.Sandbox{}, err
			}
		}
	}
	if _, err := writer.Write([]byte{1}); err != nil {
		_ = command.Process.Kill()
		runtime.mu.Unlock()
		return cri.Sandbox{}, err
	}
	_ = writer.Close()
	id := fmt.Sprintf("sandbox-%d", command.Process.Pid)
	ip := ""
	if result.IP != nil {
		ip = result.IP.String()
	}
	sandbox := &managedSandbox{sandbox: cri.Sandbox{ID: id, PodName: podName, Source: source, IP: ip, State: cri.Running}, process: command.Process, done: make(chan struct{}), netnsPath: netnsPath, hostNetwork: hostNetwork}
	runtime.sandboxes[id] = sandbox
	runtime.mu.Unlock()
	go func() { _ = command.Wait(); close(sandbox.done) }()
	runtime.publish(cri.Event{Type: "sandbox-added", Sandbox: sandbox.sandbox})
	if sandbox.sandbox.IP != "" {
		log.Printf("started Pod sandbox %s (%s), IP %s", podName, source, sandbox.sandbox.IP)
	} else {
		log.Printf("started Pod sandbox %s (%s)", podName, source)
	}
	return sandbox.sandbox, nil
}

func (runtime *Runtime) runInSandbox(podName, image string, command []string, sandboxID string) (cri.Container, error) {
	runtime.mu.Lock()
	sandbox, ok := runtime.sandboxes[sandboxID]
	runtime.mu.Unlock()
	if !ok || sandbox.sandbox.State != cri.Running {
		return cri.Container{}, errors.New("Pod sandbox is not running")
	}
	config, configDir, err := readBundleConfig(runtime.bundleDir, image)
	if err != nil {
		return cri.Container{}, err
	}
	rootfs, err := filepath.Abs(filepath.Join(configDir, config.Root.Path))
	if err != nil {
		return cri.Container{}, err
	}
	processArgs := config.Process.Args
	if len(command) > 0 {
		processArgs = command
	}
	containerRootfs, err := copyRootfs(rootfs)
	if err != nil {
		return cri.Container{}, err
	}
	started, err := startContainerInSandbox(containerRootfs, processArgs, sandbox.process.Pid)
	if err != nil {
		_ = os.RemoveAll(containerRootfs)
		return cri.Container{}, err
	}
	container := cri.Container{ID: fmt.Sprintf("pid-%d", started.Process.Pid), PodName: podName, SandboxID: sandboxID, State: cri.Running}
	managed := &managedContainer{container: container, process: started.Process, done: make(chan struct{}), sandboxID: sandboxID, rootfs: containerRootfs}
	runtime.mu.Lock()
	runtime.containers[podName] = managed
	runtime.mu.Unlock()
	go runtime.waitForContainer(podName, started, managed.done)
	runtime.publish(cri.Event{Type: "container-added", Container: container})
	log.Printf("started container for Pod %s", podName)
	return container, nil
}

func (runtime *Runtime) stopPodSandbox(id string) error {
	runtime.mu.Lock()
	sandbox, ok := runtime.sandboxes[id]
	runtime.mu.Unlock()
	if !ok {
		return cri.ErrNotFound
	}
	for name, container := range runtime.containers {
		if container.sandboxID == id {
			_ = runtime.stop(name)
		}
	}
	if runtime.network != nil && !sandbox.hostNetwork {
		_ = runtime.network.Del(context.Background(), sandbox.sandbox.PodName, sandbox.netnsPath)
	}
	_ = sandbox.process.Kill()
	runtime.mu.Lock()
	delete(runtime.sandboxes, id)
	runtime.mu.Unlock()
	runtime.publish(cri.Event{Type: "sandbox-removed", Sandbox: sandbox.sandbox})
	log.Printf("stopped Pod sandbox %s", sandbox.sandbox.PodName)
	return nil
}

func startContainerInSandbox(rootfs string, processArgs []string, sandboxPID int) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	network, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", sandboxPID))
	if err != nil {
		return nil, fmt.Errorf("open sandbox network namespace: %w", err)
	}
	defer network.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	args := append([]string{"--container-child", "--rootfs", rootfs, "--network-fd", "3", "--network-ready-fd", "4", "--"}, processArgs...)
	command := exec.Command(executable, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWPID | unix.CLONE_NEWNS | unix.CLONE_NEWUTS}
	// tutorial の標準出力は cluster の状態遷移に使うため、container の生ログは流さない。
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.ExtraFiles = []*os.File{network, reader}
	if err := command.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	_ = reader.Close()
	if _, err := writer.Write([]byte{1}); err != nil {
		_ = command.Process.Kill()
		return nil, err
	}
	_ = writer.Close()
	return command, nil
}

func readBundleConfig(bundleDir, image string) (bundleConfig, string, error) {
	imageDir := filepath.Join(bundleDir, image)
	data, err := os.ReadFile(filepath.Join(imageDir, "config.json"))
	if err != nil {
		return bundleConfig{}, "", fmt.Errorf("read %s bundle config: %w", image, err)
	}

	var config bundleConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return bundleConfig{}, "", fmt.Errorf("decode %s bundle config: %w", image, err)
	}
	return config, imageDir, nil
}

func (runtime *Runtime) waitForContainer(podName string, command *exec.Cmd, done chan struct{}) {
	if err := command.Wait(); err != nil {
		log.Printf("container %s exited: %v", podName, err)
	}
	// コンテナの再起動では Pod sandbox を維持するため、CNI の DEL は呼ばない
	runtime.mu.Lock()
	var stopped cri.Container
	if managed, ok := runtime.containers[podName]; ok && managed.done == done {
		managed.container.State = cri.Stopped
		managed.process = nil
		_ = os.RemoveAll(managed.rootfs)
		stopped = managed.container
	}
	runtime.mu.Unlock()
	if stopped.ID != "" {
		runtime.publish(cri.Event{Type: "container-stopped", Container: stopped})
		log.Printf("container for Pod %s stopped", podName)
	}
	close(done)
}

// image bundle は複数 Pod から共有するため、container ごとに書き込み用 rootfs を作る。
func copyRootfs(source string) (string, error) {
	target, err := os.MkdirTemp("", "toy-kubernetes-rootfs-")
	if err != nil {
		return "", fmt.Errorf("create container rootfs: %w", err)
	}
	if output, err := exec.Command("cp", "-a", source+"/.", target).CombinedOutput(); err != nil {
		_ = os.RemoveAll(target)
		return "", fmt.Errorf("copy container rootfs: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return target, nil
}

func (runtime *Runtime) stop(podName string) error {
	runtime.mu.Lock()
	managed, ok := runtime.containers[podName]
	if !ok {
		runtime.mu.Unlock()
		return cri.ErrNotFound
	}
	process := managed.process
	done := managed.done
	runtime.mu.Unlock()

	if process == nil {
		return nil
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop container %s: %w", podName, err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = process.Kill()
		<-done
	}
	return nil
}

func (runtime *Runtime) inspect(podName string) (cri.Container, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	managed, ok := runtime.containers[podName]
	if !ok {
		return cri.Container{}, cri.ErrNotFound
	}
	return managed.container, nil
}

// runtime 終了時に管理下の process を残さず停止する
func (runtime *Runtime) StopAll() {
	runtime.mu.Lock()
	podNames := make([]string, 0, len(runtime.containers))
	for podName := range runtime.containers {
		podNames = append(podNames, podName)
	}
	runtime.mu.Unlock()

	for _, podName := range podNames {
		_ = runtime.stop(podName)
	}
}

// namespace 内で rootfs に入って bundle の process を exec する
func RunContainerChild(rootfs string, processArgs []string) error {
	if len(processArgs) == 0 {
		return errors.New("container process command is empty")
	}
	commandPath, err := resolveCommand(rootfs, processArgs[0])
	if err != nil {
		return err
	}
	// container の mount を host や他の namespace へ伝播させない。
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make container mounts private: %w", err)
	}
	if err := unix.Chroot(rootfs); err != nil {
		return fmt.Errorf("enter container rootfs: %w", err)
	}
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("change to container root: %w", err)
	}
	if err := unix.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		return fmt.Errorf("mount container proc: %w", err)
	}

	processArgs[0] = commandPath
	return unix.Exec(commandPath, processArgs, containerEnvironment())
}

func WaitForNetwork(fd int) error {
	file := os.NewFile(uintptr(fd), "network-ready")
	if file == nil {
		return errors.New("network readiness file descriptor is invalid")
	}
	defer file.Close()
	buffer := []byte{0}
	if _, err := file.Read(buffer); err != nil {
		return fmt.Errorf("wait for CNI setup: %w", err)
	}
	return nil
}

func JoinNetworkNamespaceFD(fd int) error {
	if err := unix.Setns(fd, unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("join Pod network namespace: %w", err)
	}
	return nil
}

func containerEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "PATH=") {
			continue
		}
		environment = append(environment, value)
	}
	environment = append(environment, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	return environment
}

func resolveCommand(rootfs, command string) (string, error) {
	if strings.HasPrefix(command, "/") {
		if _, err := os.Stat(filepath.Join(rootfs, command)); err != nil {
			return "", fmt.Errorf("container command is unavailable: %w", err)
		}
		return command, nil
	}

	for _, directory := range strings.Split("/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", ":") {
		path := filepath.Join(rootfs, directory, command)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return filepath.Join(directory, command), nil
		}
	}
	return "", fmt.Errorf("container command is unavailable: %s", command)
}
