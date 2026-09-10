package bootstrap

import (
	"context"
	"fmt"
	"net/netip"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
	"toy-kubernetes/config"
)

// TODO: 現在は API server から Node object を直接登録している。
// node supervisor と kubelet の起動後に、kubelet の自己登録へ置き換える。
func RegisterWorkers(ctx context.Context, client *apiserver.Client, workerCount int) error {
	if workerCount < 0 {
		return fmt.Errorf("worker count must not be negative")
	}

	prefix, err := netip.ParsePrefix(config.PodCIDR)
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("Pod CIDR must be an IPv4 network: %q", config.PodCIDR)
	}
	if config.NodePrefix < prefix.Bits() || config.NodePrefix > 32 {
		return fmt.Errorf("node prefix must be between %d and 32", prefix.Bits())
	}

	for index := 0; index < workerCount; index++ {
		podCIDR, err := subnet(prefix, config.NodePrefix, index+1)
		if err != nil {
			return err
		}

		node := api.Node{
			TypeMeta: api.TypeMeta{APIVersion: api.APIVersionV1, Kind: "Node"},
			ObjectMeta: api.ObjectMeta{
				Name: fmt.Sprintf("%s%d", config.WorkerNamePrefix, index+1),
			},
			Spec:   api.NodeSpec{PodCIDR: podCIDR.String()},
			Status: api.NodeStatus{Phase: api.NodeReady},
		}

		if _, err := client.Nodes().Create(ctx, node); err != nil {
			return fmt.Errorf("register %s: %w", node.Name, err)
		}
	}

	return nil
}

func NodeNetwork(index int) (netip.Prefix, netip.Addr, error) {
	prefix, err := netip.ParsePrefix(config.PodCIDR)
	if err != nil {
		return netip.Prefix{}, netip.Addr{}, err
	}
	podCIDR, err := subnet(prefix, config.NodePrefix, index)
	if err != nil {
		return netip.Prefix{}, netip.Addr{}, err
	}

	address := podCIDR.Addr().As4()
	address[3] = config.GatewayHost
	gateway := netip.AddrFrom4(address)
	return podCIDR, gateway, nil
}

func NodeUnderlayAddress(index int) (netip.Addr, error) {
	prefix, err := netip.ParsePrefix(config.UnderlayCIDR)
	if err != nil || !prefix.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("underlay CIDR must be an IPv4 network: %q", config.UnderlayCIDR)
	}
	if index < 0 {
		return netip.Addr{}, fmt.Errorf("node index must not be negative")
	}

	address := prefix.Addr().As4()
	host := uint32(config.FirstNodeHost + index)
	if host >= uint32(1)<<(32-prefix.Bits()) {
		return netip.Addr{}, fmt.Errorf("node index %d does not fit in underlay CIDR %s", index, prefix)
	}
	value := uint32(address[0])<<24 | uint32(address[1])<<16 | uint32(address[2])<<8 | uint32(address[3])
	value += host
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}), nil
}

func subnet(prefix netip.Prefix, bits, index int) (netip.Prefix, error) {
	available := bits - prefix.Bits()
	if available < 32 && uint64(index) >= uint64(1)<<available {
		return netip.Prefix{}, fmt.Errorf("Pod CIDR has no subnet for worker-%d", index+1)
	}

	base := prefix.Masked().Addr().As4()
	baseNumber := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	size := uint32(1) << (32 - bits)
	number := baseNumber + uint32(index)*size
	address := netip.AddrFrom4([4]byte{byte(number >> 24), byte(number >> 16), byte(number >> 8), byte(number)})

	return netip.PrefixFrom(address, bits), nil
}
