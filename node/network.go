package node

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"

	"toy-kubernetes/bootstrap"
	"toy-kubernetes/config"
)

// node namespace に underlay の address、forwarding、Pod CIDR route を設定する
func ConfigureNetwork(ctx context.Context, nodeIndex, nodeCount int) error {
	if nodeCount < 1 || nodeIndex < 0 || nodeIndex >= nodeCount {
		return fmt.Errorf("invalid node index %d for %d nodes", nodeIndex, nodeCount)
	}

	underlayAddress, err := bootstrap.NodeUnderlayAddress(nodeIndex)
	if err != nil {
		return err
	}
	underlayPrefix, err := netip.ParsePrefix(config.UnderlayCIDR)
	if err != nil {
		return fmt.Errorf("parse underlay CIDR: %w", err)
	}
	if err := runNetworkCommand(ctx, "ip", "addr", "add", underlayAddress.String()+"/"+strconv.Itoa(underlayPrefix.Bits()), "dev", config.UnderlayInterface); err != nil {
		return fmt.Errorf("assign node underlay address: %w", err)
	}
	if err := runNetworkCommand(ctx, "ip", "link", "set", config.UnderlayInterface, "up"); err != nil {
		return fmt.Errorf("enable node underlay interface: %w", err)
	}
	if err := runNetworkCommand(ctx, "ip", "link", "set", "lo", "up"); err != nil {
		return fmt.Errorf("enable node loopback: %w", err)
	}
	if err := runNetworkCommand(ctx, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable node forwarding: %w", err)
	}

	for targetIndex := 0; targetIndex < nodeCount; targetIndex++ {
		if targetIndex == nodeIndex {
			continue
		}
		podCIDR, _, err := bootstrap.NodeNetwork(targetIndex)
		if err != nil {
			return err
		}
		nextHop, err := bootstrap.NodeUnderlayAddress(targetIndex)
		if err != nil {
			return err
		}
		if err := runNetworkCommand(ctx, "ip", "route", "add", podCIDR.String(), "via", nextHop.String(), "dev", config.UnderlayInterface); err != nil {
			return fmt.Errorf("route Pod CIDR %s: %w", podCIDR, err)
		}
	}

	return nil
}

func runNetworkCommand(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}
