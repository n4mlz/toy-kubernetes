#!/bin/sh

set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bundle_dir=${1:-$project_root/bundles}
work_dir=$(mktemp -d)
docker_container_id=

cleanup() {
	if [ -n "$docker_container_id" ]; then
		docker rm "$docker_container_id" >/dev/null 2>&1 || true
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

rm -rf "$bundle_dir"
mkdir -p "$bundle_dir"

# Docker image の rootfs と起動設定から、一つの OCI bundle を作る
prepare_image_bundle() {
	image=$1
	bundle_name=$2
	dockerfile=$3
	binary=${4:-}
	context="$work_dir/$bundle_name"
	mkdir -p "$context"
	cp "$dockerfile" "$context/Dockerfile"
	if [ -n "$binary" ]; then
		test -x "$binary" || { echo "binary not found: $binary" >&2; exit 1; }
		cp "$binary" "$context/$(basename "$binary")"
	fi

	docker build -t "$image" "$context"
	docker_container_id=$(docker create "$image")
	docker export "$docker_container_id" -o "$work_dir/$bundle_name.tar"
	docker rm "$docker_container_id" >/dev/null
	docker_container_id=

	image_dir="$bundle_dir/$bundle_name"
	rootfs="$image_dir/rootfs"
	mkdir -p "$rootfs"
	tar -xf "$work_dir/$bundle_name.tar" -C "$rootfs"
	entrypoint=$(docker inspect --format '{{json .Config.Entrypoint}}' "$image")
	command=$(docker inspect --format '{{json .Config.Cmd}}' "$image")
	process_args=$(jq -cn --argjson entrypoint "$entrypoint" --argjson command "$command" '($entrypoint // []) + ($command // [])')

	printf '{"ociVersion":"1.0.2","root":{"path":"rootfs"},"process":{"cwd":"/","args":%s}}\n' \
		"$process_args" > "$image_dir/config.json"

	if [ "$bundle_name" = "etcd" ]; then
		mkdir -p "$rootfs/var/lib/etcd"
		printf '127.0.0.1 localhost\n::1 localhost\n' > "$rootfs/etc/hosts"
	fi
}

prepare_image_bundle toy-kubernetes-nginx nginx "$script_dir/nginx/Dockerfile"
prepare_image_bundle toy-kubernetes-etcd etcd "$script_dir/etcd/Dockerfile" "$(command -v etcd)"
prepare_image_bundle toy-kubernetes-kube-apiserver kube-apiserver "$script_dir/kube-apiserver/Dockerfile" "$project_root/.toy/bin/kube-apiserver"
prepare_image_bundle toy-kubernetes-kube-scheduler kube-scheduler "$script_dir/kube-scheduler/Dockerfile" "$project_root/.toy/bin/kube-scheduler"
prepare_image_bundle toy-kubernetes-kube-controller-manager kube-controller-manager "$script_dir/kube-controller-manager/Dockerfile" "$project_root/.toy/bin/kube-controller-manager"

echo "bundle ready: $bundle_dir"
