package bootstrap

import (
	"net/netip"
	"testing"
)

func TestSubnetAssignsOneNonOverlappingPodCIDRPerWorker(t *testing.T) {
	prefix := netip.MustParsePrefix("10.244.0.0/16")

	first, err := subnet(prefix, 24, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := subnet(prefix, 24, 1)
	if err != nil {
		t.Fatal(err)
	}

	if first.String() != "10.244.0.0/24" || second.String() != "10.244.1.0/24" || first.Overlaps(second) {
		t.Fatalf("workers should receive distinct Pod CIDRs: %s, %s", first, second)
	}
}
