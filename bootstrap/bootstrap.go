package bootstrap

import (
	"context"
	"fmt"
	"net/netip"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
)

type Config struct {
	WorkerCount int
	PodCIDR     string
	NodePrefix  int
}

// TODO: 現在は API server から Node object を直接登録している。
// node supervisor と kubelet の起動後に、kubelet の自己登録へ置き換える。
func RegisterWorkers(ctx context.Context, client *apiserver.Client, config Config) error {
	if config.WorkerCount < 0 {
		return fmt.Errorf("worker count must not be negative")
	}

	if config.PodCIDR == "" {
		config.PodCIDR = DefaultPodCIDR
	}
	if config.NodePrefix == 0 {
		config.NodePrefix = DefaultNodePrefix
	}

	prefix, err := netip.ParsePrefix(config.PodCIDR)
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("Pod CIDR must be an IPv4 network: %q", config.PodCIDR)
	}
	if config.NodePrefix < prefix.Bits() || config.NodePrefix > 32 {
		return fmt.Errorf("node prefix must be between %d and 32", prefix.Bits())
	}

	for index := 0; index < config.WorkerCount; index++ {
		podCIDR, err := subnet(prefix, config.NodePrefix, index)
		if err != nil {
			return err
		}

		node := api.Node{
			TypeMeta: api.TypeMeta{APIVersion: api.APIVersionV1, Kind: "Node"},
			ObjectMeta: api.ObjectMeta{
				Name: fmt.Sprintf("worker-%d", index+1),
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
