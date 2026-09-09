package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"toy-kubernetes/cri"

	"golang.org/x/sys/unix"
)

// Runtime は node 上で container process を起動し、CRI 用に socket を提供する
type Runtime struct {
	socketPath string
	bundleDir  string

	mu         sync.Mutex
	containers map[string]*managedContainer
}

type managedContainer struct {
	container cri.Container
	process   *os.Process
	done      chan struct{}
}

type request struct {
	Operation string `json:"op"`
	Pod       string `json:"pod,omitempty"`
	Image     string `json:"image,omitempty"`
}

type response struct {
	OK         bool            `json:"ok"`
	Error      string          `json:"error,omitempty"`
	ID         string          `json:"id,omitempty"`
	Pod        string          `json:"pod,omitempty"`
	State      cri.State       `json:"state,omitempty"`
	Containers []cri.Container `json:"containers,omitempty"`
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
	}
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

	result, err := runtime.handleRequest(input)
	if err != nil {
		writeResponse(connection, response{Error: err.Error()})
		return
	}

	result.OK = true
	writeResponse(connection, result)
}

func writeResponse(connection net.Conn, result response) {
	_ = json.NewEncoder(connection).Encode(result)
}

func (runtime *Runtime) handleRequest(input request) (response, error) {
	switch input.Operation {
	case "list":
		return runtime.list(), nil
	case "run":
		container, err := runtime.run(input.Pod, input.Image)
		if err != nil {
			return response{}, err
		}
		return response{ID: container.ID, Pod: container.PodName, State: container.State}, nil
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

func (runtime *Runtime) run(podName, image string) (cri.Container, error) {
	if image != "nginx" {
		return cri.Container{}, errors.New("only the nginx image is supported")
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	if managed, ok := runtime.containers[podName]; ok && managed.container.State == cri.Running {
		return managed.container, nil
	}

	config, err := readBundleConfig(runtime.bundleDir)
	if err != nil {
		return cri.Container{}, err
	}
	if config.Root.Path == "" {
		return cri.Container{}, errors.New("bundle config has no root path")
	}

	rootfs, err := filepath.Abs(filepath.Join(runtime.bundleDir, config.Root.Path))
	if err != nil {
		return cri.Container{}, fmt.Errorf("resolve bundle rootfs: %w", err)
	}
	if _, err := os.Stat(rootfs); err != nil {
		return cri.Container{}, fmt.Errorf("bundle rootfs is unavailable: %w", err)
	}

	process, err := startContainerProcess(rootfs, config.Process.Args)
	if err != nil {
		return cri.Container{}, err
	}

	container := cri.Container{
		ID:      fmt.Sprintf("pid-%d", process.Process.Pid),
		PodName: podName,
		State:   cri.Running,
	}
	managed := &managedContainer{
		container: container,
		process:   process.Process,
		done:      make(chan struct{}),
	}
	runtime.containers[podName] = managed

	go runtime.waitForContainer(podName, process, managed.done)
	return container, nil
}

func startContainerProcess(rootfs string, processArgs []string) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find CRI runtime executable: %w", err)
	}

	processArgs = append([]string{}, processArgs...)
	if len(processArgs) == 0 {
		return nil, errors.New("bundle has no process command")
	}
	commandArgs := append([]string{"--container-child", "--rootfs", rootfs, "--"}, processArgs...)
	command := exec.Command(executable, commandArgs...)
	command.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: unix.CLONE_NEWPID | unix.CLONE_NEWNS | unix.CLONE_NEWUTS | unix.CLONE_NEWNET,
	}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start container in namespaces: %w", err)
	}
	return command, nil
}

func readBundleConfig(bundleDir string) (bundleConfig, error) {
	data, err := os.ReadFile(filepath.Join(bundleDir, "config.json"))
	if err != nil {
		return bundleConfig{}, fmt.Errorf("read bundle config: %w", err)
	}

	var config bundleConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return bundleConfig{}, fmt.Errorf("decode bundle config: %w", err)
	}
	return config, nil
}

func (runtime *Runtime) waitForContainer(podName string, command *exec.Cmd, done chan struct{}) {
	_ = command.Wait()

	runtime.mu.Lock()
	if managed, ok := runtime.containers[podName]; ok && managed.done == done {
		managed.container.State = cri.Stopped
		managed.process = nil
	}
	runtime.mu.Unlock()
	close(done)
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
