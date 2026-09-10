#!/bin/sh

set -eu

bundle_dir=${1:-bundles}
image=toy-kubernetes-nginx:latest
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
docker_container_id=

cleanup() {
	if [ -n "$docker_container_id" ]; then
		docker rm "$docker_container_id" >/dev/null
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

docker build -t "$image" "$script_dir"
docker_container_id=$(docker create "$image")
docker export "$docker_container_id" -o "$work_dir/rootfs.tar"
entrypoint=$(docker inspect --format '{{json .Config.Entrypoint}}' "$image")
command=$(docker inspect --format '{{json .Config.Cmd}}' "$image")
process_args=$(jq -cn --argjson entrypoint "$entrypoint" --argjson command "$command" '$entrypoint + $command')

rootfs="$bundle_dir/rootfs"
rm -rf "$rootfs"
mkdir -p "$rootfs"
tar -xf "$work_dir/rootfs.tar" -C "$rootfs"
mkdir -p "$rootfs/var/lib/etcd"
printf '127.0.0.1 localhost\n::1 localhost\n' > "$rootfs/etc/hosts"

# docker export で失われる起動設定を OCI bundle の config.json として保存する
printf '{"ociVersion":"1.0.2","root":{"path":"rootfs"},"process":{"cwd":"/","args":%s}}\n' \
	"$process_args" > "$bundle_dir/config.json"

prepare_control_plane_bundle() {
	image=$1
	binary=$2
	shift 2

	test -x "$binary" || { echo "binary not found: $binary" >&2; exit 1; }
	mkdir -p "$rootfs/toy/bin" "$bundle_dir/$image"
	cp "$binary" "$rootfs/toy/bin/$(basename "$binary")"

	args=$(printf '%s\n' "$@" | jq -Rsc 'split("\n")[:-1]')
	printf '{"ociVersion":"1.0.2","root":{"path":"../rootfs"},"process":{"cwd":"/","args":%s}}\n' \
		"$args" > "$bundle_dir/$image/config.json"
}

prepare_control_plane_bundle etcd "$(command -v etcd)" /toy/bin/etcd \
	--name toy-control-plane \
	--data-dir /var/lib/etcd \
	--listen-peer-urls http://127.0.0.1:2380 \
	--initial-advertise-peer-urls http://127.0.0.1:2380 \
	--initial-cluster toy-control-plane=http://127.0.0.1:2380 \
	--initial-cluster-state new \
	--listen-client-urls http://127.0.0.1:2379 \
	--advertise-client-urls http://127.0.0.1:2379
prepare_control_plane_bundle kube-apiserver .toy/bin/kube-apiserver \
	/toy/bin/kube-apiserver -listen 0.0.0.0:8080 -etcd-endpoint http://127.0.0.1:2379
prepare_control_plane_bundle kube-scheduler .toy/bin/kube-scheduler \
	/toy/bin/kube-scheduler -api-server http://127.0.0.1:8080
prepare_control_plane_bundle kube-controller-manager .toy/bin/kube-controller-manager \
	/toy/bin/kube-controller-manager -api-server http://127.0.0.1:8080

echo "bundle ready: $bundle_dir"
