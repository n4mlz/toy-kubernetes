#!/bin/sh

set -eu

bundle_dir=${1:-bundles/nginx}
image=${NGINX_IMAGE:-nginx:latest}
work_dir=$(mktemp -d)
container_id=

cleanup() {
	if [ -n "$container_id" ]; then
		docker rm "$container_id" >/dev/null
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

docker pull "$image"
container_id=$(docker create "$image")
docker export "$container_id" -o "$work_dir/rootfs.tar"

rootfs="$bundle_dir/rootfs"
rm -rf "$rootfs"
mkdir -p "$rootfs"
tar -xf "$work_dir/rootfs.tar" -C "$rootfs"

echo "nginx bundle ready: $bundle_dir"
