package cni

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Pod network namespace の接続と切断を担当する
type CNI interface {
	Add(context.Context, string, string) (Result, error)
	Del(context.Context, string, string) error
}

type Result struct {
	IP      net.IP
	Gateway net.IP
}

type LinuxCNI struct {
	bridge  string
	podCIDR *net.IPNet
	gateway net.IP
	pods    map[string]attachment
}

type attachment struct {
	result   Result
	hostLink string
}

func NewLinuxCNI(bridge, podCIDR, gateway string) (*LinuxCNI, error) {
	ip, network, err := net.ParseCIDR(podCIDR)
	if err != nil {
		return nil, fmt.Errorf("parse Pod CIDR: %w", err)
	}
	parsedGateway := net.ParseIP(gateway).To4()
	if parsedGateway == nil || !network.Contains(parsedGateway) {
		return nil, fmt.Errorf("gateway is outside Pod CIDR")
	}
	network.IP = ip.To4()
	return &LinuxCNI{bridge: bridge, podCIDR: network, gateway: parsedGateway, pods: make(map[string]attachment)}, nil
}

// bridge に veth pair を接続し、Pod namespace に IP と route を設定する
func (plugin *LinuxCNI) Add(ctx context.Context, podName, netnsPath string) (Result, error) {
	if attachment, ok := plugin.pods[podName]; ok {
		return attachment.result, nil
	}
	netnsTarget, err := namespaceTarget(netnsPath)
	if err != nil {
		return Result{}, err
	}

	if err := plugin.ensureBridge(ctx); err != nil {
		return Result{}, err
	}
	ip, err := plugin.nextIP()
	if err != nil {
		return Result{}, err
	}
	hostName, peerName := linkNames(podName)
	if err := run(ctx, "ip", "link", "add", hostName, "type", "veth", "peer", "name", peerName); err != nil {
		return Result{}, fmt.Errorf("create veth for Pod %s: %w", podName, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = run(context.Background(), "ip", "link", "del", hostName)
		}
	}()

	if err := run(ctx, "ip", "link", "set", peerName, "netns", netnsTarget); err != nil {
		return Result{}, fmt.Errorf("move veth into Pod namespace: %w", err)
	}
	if err := run(ctx, "ip", "link", "set", hostName, "master", plugin.bridge); err != nil {
		return Result{}, fmt.Errorf("connect veth to bridge: %w", err)
	}
	if err := run(ctx, "ip", "link", "set", hostName, "up"); err != nil {
		return Result{}, fmt.Errorf("enable host veth: %w", err)
	}
	if err := plugin.configurePod(ctx, netnsPath, peerName, ip); err != nil {
		return Result{}, err
	}

	cleanup = false
	result := Result{IP: ip, Gateway: plugin.gateway}
	plugin.pods[podName] = attachment{result: result, hostLink: hostName}
	return result, nil
}

func (plugin *LinuxCNI) Del(ctx context.Context, podName, netnsPath string) error {
	attachment, ok := plugin.pods[podName]
	if !ok {
		return nil
	}
	if err := run(ctx, "ip", "link", "del", attachment.hostLink); err != nil && !strings.Contains(err.Error(), "Cannot find device") {
		return fmt.Errorf("remove Pod interface %s: %w", podName, err)
	}
	delete(plugin.pods, podName)
	return nil
}

func (plugin *LinuxCNI) ensureBridge(ctx context.Context) error {
	if err := run(ctx, "ip", "link", "show", plugin.bridge); err != nil {
		if err := run(ctx, "ip", "link", "add", plugin.bridge, "type", "bridge"); err != nil {
			return fmt.Errorf("create bridge: %w", err)
		}
		prefix, _ := plugin.podCIDR.Mask.Size()
		if err := run(ctx, "ip", "addr", "add", plugin.gateway.String()+"/"+strconv.Itoa(prefix), "dev", plugin.bridge); err != nil {
			return fmt.Errorf("assign bridge gateway: %w", err)
		}
		if err := run(ctx, "ip", "link", "set", plugin.bridge, "up"); err != nil {
			return fmt.Errorf("enable bridge: %w", err)
		}
	}
	return nil
}

func (plugin *LinuxCNI) configurePod(ctx context.Context, netnsPath, peer string, ip net.IP) error {
	prefix, _ := plugin.podCIDR.Mask.Size()
	nsenterArgs, err := namespaceCommand(netnsPath)
	if err != nil {
		return err
	}
	if err := run(ctx, "nsenter", append(nsenterArgs, "ip", "link", "set", peer, "name", podInterface)...); err != nil {
		return fmt.Errorf("rename Pod interface: %w", err)
	}
	if err := run(ctx, "nsenter", append(nsenterArgs, "ip", "link", "set", loopback, "up")...); err != nil {
		return fmt.Errorf("enable Pod loopback: %w", err)
	}
	if err := run(ctx, "nsenter", append(nsenterArgs, "ip", "addr", "add", ip.String()+"/"+strconv.Itoa(prefix), "dev", podInterface)...); err != nil {
		return fmt.Errorf("assign Pod IP: %w", err)
	}
	if err := run(ctx, "nsenter", append(nsenterArgs, "ip", "link", "set", podInterface, "up")...); err != nil {
		return fmt.Errorf("enable Pod interface: %w", err)
	}
	if err := run(ctx, "nsenter", append(nsenterArgs, "ip", "route", "add", "default", "via", plugin.gateway.String())...); err != nil {
		return fmt.Errorf("set Pod default route: %w", err)
	}
	return nil
}

func (plugin *LinuxCNI) nextIP() (net.IP, error) {
	// gateway は host 部分 1 を使うため、Pod には 2 から割り当てる
	const firstPodHost = 2

	for host := firstPodHost; host < 255; host++ {
		candidate := append(net.IP(nil), plugin.podCIDR.IP.To4()...)
		candidate[3] = byte(host)
		if candidate.Equal(plugin.gateway) {
			continue
		}
		used := false
		for _, attachment := range plugin.pods {
			used = used || attachment.result.IP.Equal(candidate)
		}
		if !used {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("Pod CIDR has no available IP")
}

var processNetns = regexp.MustCompile(`^/proc/([0-9]+)/ns/net$`)

func namespaceTarget(path string) (string, error) {
	match := processNetns.FindStringSubmatch(path)
	if match == nil {
		return "", fmt.Errorf("network namespace must be a process namespace: %s", path)
	}
	return match[1], nil
}

func namespaceCommand(path string) ([]string, error) {
	target, err := namespaceTarget(path)
	if err != nil {
		return nil, err
	}
	return []string{"-t", target, "-n"}, nil
}

func linkNames(podName string) (string, string) {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(podName))
	suffix := fmt.Sprintf("%08x", hash.Sum32())[:8]
	return hostVethPrefix + suffix, podVethPrefix + suffix
}

func run(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}
