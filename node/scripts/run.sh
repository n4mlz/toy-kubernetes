#!/bin/sh

set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

# 固定値の正本は config/const.go。shell から Go の定数を参照できないため、
# ここには node の起動に必要な値だけを写している。変更時は config/const.go と同期する
workers=2
control_plane_name=control-plane
worker_name_prefix=worker-
bin_dir=$project_root/.toy/bin
runtime_path=$bin_dir/runtime
kubelet_path=$bin_dir/kubelet
supervisor_path=$bin_dir/node-supervisor
root_dir=$project_root/.toy/nodes
bundle_dir=$project_root/bundles

while [ "$#" -gt 0 ]; do
	case "$1" in
	--workers)
		[ "$#" -ge 2 ] || { echo '--workers requires COUNT' >&2; exit 2; }
		workers=$2
		shift 2
		;;
	*)
		echo "usage: $0 [--workers COUNT]" >&2
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

command -v ip >/dev/null 2>&1 || { echo '[node] ip failed' >&2; exit 1; }
test -x "$supervisor_path" || { echo '[node] node-supervisor is not built' >&2; exit 1; }
test -x "$runtime_path" || { echo '[node] runtime is not built' >&2; exit 1; }
test -x "$kubelet_path" || { echo '[node] kubelet is not built' >&2; exit 1; }

mkdir -p "$root_dir"
supervisor_pids=
node_names=
created_nodes=

cleanup() {
	for pid in $supervisor_pids; do
		kill -TERM "$pid" 2>/dev/null || true
	done
	for pid in $supervisor_pids; do
		wait "$pid" 2>/dev/null || true
	done
	for node_name in $created_nodes; do
		ip netns del "$node_name" 2>/dev/null || true
	done
}
trap cleanup EXIT INT TERM

node_names=$control_plane_name
# TODO: control plane bootstrap should place its static Pod manifests in this directory before startup.
for index in $(seq 1 "$workers"); do
	node_names="$node_names $worker_name_prefix$index"
done

for node_name in $node_names; do
	if ip netns list | awk '{print $1}' | grep -Fx "$node_name" >/dev/null 2>&1; then
		echo "[node] $node_name already exists" >&2
		exit 1
	fi
done

for node_name in $node_names; do
	node_dir="$root_dir/$node_name"
	mkdir -p "$node_dir/manifests" "$node_dir/logs"
	case "$node_name" in
	control-plane) node_index=0 ;;
	${worker_name_prefix}*) node_index=${node_name#"$worker_name_prefix"} ;;
	esac
	ip netns add "$node_name"
	created_nodes="$created_nodes $node_name"
	ip netns exec "$node_name" "$supervisor_path" \
		--node "$node_name" \
		--node-index "$node_index" \
		--runtime "$runtime_path" \
		--kubelet "$kubelet_path" \
		--socket "$node_dir/runtime.sock" \
		--bundle-dir "$bundle_dir" \
		--manifests "$node_dir/manifests" \
		--api-server "${TOY_API_SERVER:-}" \
		--log-dir "$node_dir/logs" &
	supervisor_pids="$supervisor_pids $!"
done

echo "nodes ready: control-plane + $workers worker(s)"
wait
