package kubeproxy

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"toy-kubernetes/api"
	"toy-kubernetes/apiserver"
)

// Service から一つの Pod endpoint へ転送する規則
type Rule struct {
	ClusterIP  string
	Port       int
	NodePort   int
	PodIP      string
	TargetPort int
}

// node namespace の forwarding state の置き換えを担う
type Forwarder interface {
	Replace(context.Context, []Rule) error
}

// Service と Pod を観測し、node namespace の転送状態の更新を担う
type KubeProxy struct {
	apiClient *apiserver.Client
	forwarder Forwarder
}

func New(apiClient *apiserver.Client, forwarder Forwarder) *KubeProxy {
	return &KubeProxy{apiClient: apiClient, forwarder: forwarder}
}

func (proxy *KubeProxy) Reconcile(ctx context.Context) error {
	services, err := proxy.apiClient.Services().List(ctx)
	if err != nil {
		return err
	}
	pods, err := proxy.apiClient.Pods().List(ctx)
	if err != nil {
		return err
	}

	rules, err := serviceRules(services.Items, pods.Items)
	if err != nil {
		return err
	}
	return proxy.forwarder.Replace(ctx, rules)
}

// Service または Pod の変更を契機に、forwarding state を再構成する
func (proxy *KubeProxy) Run(ctx context.Context) error {
	return api.Run(ctx, proxy, proxy.watch)
}

func (proxy *KubeProxy) watch(ctx context.Context) (<-chan error, error) {
	return api.CombineWatches(ctx, proxy.watchServices, proxy.watchPods)
}

func (proxy *KubeProxy) watchServices(ctx context.Context) (<-chan error, error) {
	services, err := proxy.apiClient.Services().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := proxy.apiClient.Services().Watch(ctx, services.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
}

func (proxy *KubeProxy) watchPods(ctx context.Context) (<-chan error, error) {
	pods, err := proxy.apiClient.Pods().List(ctx)
	if err != nil {
		return nil, err
	}
	events, err := proxy.apiClient.Pods().Watch(ctx, pods.ResourceVersion)
	if err != nil {
		return nil, err
	}
	return apiserver.WatchErrors(ctx, events), nil
}

// Service selector に一致し、Running になっている Pod だけを endpoint にする
func serviceRules(services []api.Service, pods []api.Pod) ([]Rule, error) {
	rules := make([]Rule, 0)
	for _, service := range services {
		if service.Spec.ClusterIP == "" || service.Spec.Port < 1 || service.Spec.TargetPort < 1 {
			continue
		}
		clusterIP, err := netip.ParseAddr(service.Spec.ClusterIP)
		if err != nil {
			return nil, fmt.Errorf("service %s has invalid ClusterIP: %w", service.Name, err)
		}
		if !clusterIP.Is4() {
			return nil, fmt.Errorf("service %s ClusterIP must be IPv4", service.Name)
		}
		nodePort := 0
		if service.Spec.Type == api.ServiceNodePort {
			nodePort = service.Spec.NodePort
		}

		for _, pod := range pods {
			if pod.Status.Phase != api.PodRunning || pod.Status.PodIP == "" || !api.LabelsMatch(service.Spec.Selector, pod.Labels) {
				continue
			}
			podIP, err := netip.ParseAddr(pod.Status.PodIP)
			if err != nil {
				return nil, fmt.Errorf("Pod %s has invalid PodIP: %w", pod.Name, err)
			}
			if !podIP.Is4() {
				return nil, fmt.Errorf("Pod %s PodIP must be IPv4", pod.Name)
			}
			rules = append(rules, Rule{ClusterIP: service.Spec.ClusterIP, Port: service.Spec.Port, NodePort: nodePort, PodIP: pod.Status.PodIP, TargetPort: service.Spec.TargetPort})
		}
	}

	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ClusterIP != rules[j].ClusterIP {
			return rules[i].ClusterIP < rules[j].ClusterIP
		}
		if rules[i].Port != rules[j].Port {
			return rules[i].Port < rules[j].Port
		}
		return rules[i].PodIP < rules[j].PodIP
	})
	return rules, nil
}

type NftablesForwarder struct {
	table string
}

func NewNftablesForwarder(table string) *NftablesForwarder {
	return &NftablesForwarder{table: table}
}

// 現在の Service rule を一度消し、node namespace に再構成する
func (forwarder *NftablesForwarder) Replace(ctx context.Context, rules []Rule) error {
	if err := exec.CommandContext(ctx, "nft", "list", "table", "ip", forwarder.table).Run(); err == nil {
		if err := exec.CommandContext(ctx, "nft", "delete", "table", "ip", forwarder.table).Run(); err != nil {
			return fmt.Errorf("delete nftables table: %w", err)
		}
	}

	commands := []string{
		"add table ip " + forwarder.table,
		"add chain ip " + forwarder.table + " prerouting { type nat hook prerouting priority -100; policy accept; }",
	}
	for _, rule := range rules {
		commands = append(commands, fmt.Sprintf("add rule ip %s prerouting ip daddr %s tcp dport %d dnat to %s:%d", forwarder.table, rule.ClusterIP, rule.Port, rule.PodIP, rule.TargetPort))
		if rule.NodePort != 0 {
			commands = append(commands, fmt.Sprintf("add rule ip %s prerouting tcp dport %d dnat to %s:%d", forwarder.table, rule.NodePort, rule.PodIP, rule.TargetPort))
		}
	}

	command := exec.CommandContext(ctx, "nft", "-f", "-")
	command.Stdin = strings.NewReader(strings.Join(commands, "\n") + "\n")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("apply nftables rules: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (rule Rule) String() string {
	return rule.ClusterIP + ":" + strconv.Itoa(rule.Port) + " -> " + rule.PodIP + ":" + strconv.Itoa(rule.TargetPort)
}
