#!/bin/sh

set -eu

workers=2
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
test -x /tmp/node-supervisor || { echo '[node] node-supervisor is not built' >&2; exit 1; }
test -x /tmp/runtime || { echo '[node] runtime is not built' >&2; exit 1; }

root_dir=${TOY_NODE_ROOT:-/tmp/toy-kubernetes-nodes}
bundle_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)/bundles
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

node_names=control-plane
for index in $(seq 1 "$workers"); do
	node_names="$node_names worker-$index"
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
	ip netns add "$node_name"
	created_nodes="$created_nodes $node_name"
	ip netns exec "$node_name" /tmp/node-supervisor \
		--node "$node_name" \
		--socket "$node_dir/runtime.sock" \
		--bundle-dir "$bundle_dir" \
		--log-dir "$node_dir/logs" &
	supervisor_pids="$supervisor_pids $!"
done

echo "nodes ready: control-plane + $workers worker(s)"
wait
