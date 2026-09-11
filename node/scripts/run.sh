#!/bin/sh

set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

# 固定値の正本は config/const.go。shell から Go の定数を参照できないため、
# ここには node の起動に必要な値だけを写している。変更時は config/const.go と同期する。
workers=2
control_plane_name=control-plane
worker_name_prefix=worker-
bin_dir=$project_root/.toy/bin
runtime_path=$bin_dir/runtime
kubelet_path=$bin_dir/kubelet
kube_proxy_path=$bin_dir/kube-proxy
supervisor_path=$bin_dir/node-supervisor
root_dir=$project_root/.toy/nodes
bundle_dir=$project_root/bundles
control_plane_manifest_dir=$project_root/node/manifests/control-plane
underlay_bridge=toy-underlay0
underlay_interface=underlay0
# config/const.go の UnderlayGatewayHost と同期する
underlay_gateway=10.200.0.1

supervisor_pids=
created_nodes=
nodeport_forward_pid=
nodeport_forward_pid_file=

usage() {
	echo "usage: $0 [--workers COUNT]" >&2
}

parse_args() {
	while [ "$#" -gt 0 ]; do
		case "$1" in
		--workers)
			[ "$#" -ge 2 ] || { echo '--workers requires COUNT' >&2; usage; exit 2; }
			workers=$2
			shift 2
			;;
		*)
			echo "unknown argument: $1" >&2
			usage
			exit 2
			;;
		esac
	done

	case "$workers" in
	''|*[!0-9]*) echo 'workers must be a non-negative integer' >&2; exit 2 ;;
	esac
	if [ "$workers" -lt 1 ]; then
		echo 'workers must be at least 1' >&2
		exit 2
	fi
}

check_requirements() {
	command -v ip >/dev/null 2>&1 || { echo '[node] ip failed' >&2; exit 1; }
	command -v socat >/dev/null 2>&1 || { echo '[node] socat failed' >&2; exit 1; }
	test -x "$supervisor_path" || { echo '[node] node-supervisor is not built' >&2; exit 1; }
	test -x "$runtime_path" || { echo '[node] runtime is not built' >&2; exit 1; }
	test -x "$kubelet_path" || { echo '[node] kubelet is not built' >&2; exit 1; }
	test -x "$kube_proxy_path" || { echo '[node] kube-proxy is not built' >&2; exit 1; }
}

namespace_exists() {
	ip netns list | awk '{print $1}' | grep -Fx "$1" >/dev/null 2>&1
}

check_resources_are_available() {
	namespace_exists "$control_plane_name" && {
		echo "[node] $control_plane_name already exists" >&2
		exit 1
	}

	for index in $(seq 1 "$workers"); do
		node_name=$worker_name_prefix$index
		namespace_exists "$node_name" && {
			echo "[node] $node_name already exists" >&2
			exit 1
		}
	done

	if ip link show "$underlay_bridge" >/dev/null 2>&1; then
		echo "[node] $underlay_bridge already exists" >&2
		exit 1
	fi
}

create_underlay() {
	ip link add "$underlay_bridge" type bridge
	ip addr add "$underlay_gateway/24" dev "$underlay_bridge"
	ip link set "$underlay_bridge" up
}

prepare_control_plane_manifests() {
	control_plane_dir=$root_dir/$control_plane_name/manifests
	mkdir -p "$control_plane_dir"

	if [ ! -d "$control_plane_manifest_dir" ]; then
		echo "[node] control plane manifest directory not found: $control_plane_manifest_dir" >&2
		return 1
	fi

	manifest_found=false
	for manifest in "$control_plane_manifest_dir"/*.yaml "$control_plane_manifest_dir"/*.yml "$control_plane_manifest_dir"/*.json; do
		[ -f "$manifest" ] || continue
		cp "$manifest" "$control_plane_dir/"
		manifest_found=true
	done
	if [ "$manifest_found" = false ]; then
		echo "[node] no control plane manifest found in $control_plane_manifest_dir" >&2
		return 1
	fi
}

start_node() {
	node_name=$1
	node_index=$2
	node_count=$3
	node_dir=$root_dir/$node_name
	host_peer=toy-ul$node_index
	api_server=
	register_node=true
	if [ "$node_index" -eq 0 ]; then
		api_server=${TOY_API_SERVER:-http://127.0.0.1:8080}
		register_node=false
	else
		api_server=${TOY_API_SERVER:-http://10.200.0.2:8080}
	fi
	mkdir -p "$node_dir/manifests" "$node_dir/logs"
	# .toy はこのスクリプトが管理する実行時データなので、起動ごとにログを初期化する
	rm -f "$node_dir/logs"/*.log
	ip netns add "$node_name"
	created_nodes="$created_nodes $node_name"

	ip link add "$host_peer" type veth peer name "$underlay_interface"
	ip link set "$underlay_interface" netns "$node_name"
	ip link set "$host_peer" master "$underlay_bridge"
	ip link set "$host_peer" up

	# supervisor が node namespace 内の runtime と kubelet を起動する
	ip netns exec "$node_name" "$supervisor_path" \
		--node "$node_name" \
		--node-index "$node_index" \
		--node-count "$node_count" \
		--runtime "$runtime_path" \
		--kubelet "$kubelet_path" \
		--kube-proxy "$kube_proxy_path" \
		--socket "$node_dir/runtime.sock" \
		--bundle-dir "$bundle_dir" \
		--manifests "$node_dir/manifests" \
		--api-server "$api_server" \
		--register-node="$register_node" \
		--log-dir "$node_dir/logs" &
	supervisor_pids="$supervisor_pids $!"
}

cleanup() {
	if [ -n "$nodeport_forward_pid" ]; then
		kill -TERM "$nodeport_forward_pid" 2>/dev/null || true
		wait "$nodeport_forward_pid" 2>/dev/null || true
	fi
	for pid in $supervisor_pids; do
		kill -TERM "$pid" 2>/dev/null || true
	done
	for pid in $supervisor_pids; do
		wait "$pid" 2>/dev/null || true
	done
	for node_name in $created_nodes; do
		ip netns del "$node_name" 2>/dev/null || true
	done
	ip link del "$underlay_bridge" 2>/dev/null || true
	[ -z "$nodeport_forward_pid_file" ] || rm -f "$nodeport_forward_pid_file"
}

parse_args "$@"
check_requirements
check_resources_are_available

mkdir -p "$root_dir"
trap cleanup EXIT INT TERM

node_count=$((workers + 1))
create_underlay
prepare_control_plane_manifests

start_node "$control_plane_name" 0 "$node_count"
for index in $(seq 1 "$workers"); do
	start_node "$worker_name_prefix$index" "$index" "$node_count"
done

# worker-1 の NodePort を devcontainer の port forwarding へ渡す (便宜上)
nodeport_forward_pid_file=$root_dir/nodeport-forward.pid
sh "$project_root/node/scripts/forward-nodeport.sh" 30000 10.200.0.3 &
nodeport_forward_pid=$!
printf '%s\n' "$nodeport_forward_pid" >"$nodeport_forward_pid_file"

echo "nodes ready: control-plane + $workers worker(s)"
wait
