package cni

import "testing"

func TestNewLinuxCNIRejectsGatewayOutsidePodCIDR(t *testing.T) {
	if _, err := NewLinuxCNI("cni0", "10.244.0.0/24", "10.245.0.1"); err == nil {
		t.Fatal("gateway outside Pod CIDR should be rejected")
	}
}

func TestLinkNamesAreStableAndDifferentForDifferentPods(t *testing.T) {
	hostA, podA := linkNames("pod-a")
	hostAgain, podAgain := linkNames("pod-a")
	hostB, podB := linkNames("pod-b")

	if hostA != hostAgain || podA != podAgain {
		t.Fatal("the same Pod should receive stable link names")
	}
	if hostA == hostB || podA == podB {
		t.Fatal("different Pods should receive different link names")
	}
}
