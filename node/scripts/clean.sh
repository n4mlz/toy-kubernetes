#!/bin/sh

set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

# run.sh が作成する名前だけを対象にする
control_plane_name=control-plane
underlay_bridge=toy-underlay0
nodeport_forward_pid_file="$project_root/.toy/nodes/nodeport-forward.pid"

command -v ip >/dev/null 2>&1 || {
	echo '[node] iproute2 is required' >&2
	exit 1
}

if [ -f "$nodeport_forward_pid_file" ]; then
	forward_pid=$(cat "$nodeport_forward_pid_file")
	forward_args=$(ps -p "$forward_pid" -o args= 2>/dev/null || true)
	case "$forward_args" in
	*socat*TCP-LISTEN:30000*) kill -KILL "$forward_pid" 2>/dev/null || true ;;
	esac
fi

# namespace を先に削除すると、namespace を参照していた process が孤児として
# 残る環境があるため、toy-kubernetes の実行ファイルだけを先に停止する
ps -eo pid=,args= |
	awk -v bin_dir="$project_root/.toy/bin/" '$2 ~ "^" bin_dir "(node-supervisor|runtime|kubelet|kube-proxy)( |$)" {print $1}' |
	while IFS= read -r pid; do
		[ -n "$pid" ] || continue
		kill -KILL "$pid" 2>/dev/null || true
	done

clean_namespace() {
	node=$1
	if ! ip netns list | awk -v node="$node" '$1 == node {found=1} END {exit !found}'; then
		return 0
	fi

	ip netns pids "$node" |
	while IFS= read -r pid; do
		[ -n "$pid" ] || continue
		kill -KILL "$pid" 2>/dev/null || true
	done
	ip netns del "$node" 2>/dev/null || true
}

clean_namespace "$control_plane_name"
ip netns list |
	awk '$1 ~ /^worker-[0-9]+$/ {print $1}' |
	while IFS= read -r node; do
		clean_namespace "$node"
	done

ip -o link show |
	awk -F': ' '$2 ~ /^toy-ul[0-9]+(@|:)/ {sub(/[@:].*/, "", $2); print $2}' |
	while IFS= read -r link; do
		ip link del "$link" 2>/dev/null || true
	done
ip link del "$underlay_bridge" 2>/dev/null || true
rm -rf "$project_root/.toy/nodes" "$project_root/bundles"

echo '[node] clean ok'
