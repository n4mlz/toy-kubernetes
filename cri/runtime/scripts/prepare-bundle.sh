#!/bin/sh

set -eu

bundle_dir=${1:-bundles}
image=toy-kubernetes-nginx:latest
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
container_id=

cleanup() {
	if [ -n "$container_id" ]; then
		docker rm "$container_id" >/dev/null
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

docker build -t "$image" "$script_dir"
container_id=$(docker create "$image")
docker export "$container_id" -o "$work_dir/rootfs.tar"
entrypoint=$(docker inspect --format '{{json .Config.Entrypoint}}' "$image")
command=$(docker inspect --format '{{json .Config.Cmd}}' "$image")
process_args=$(jq -cn --argjson entrypoint "$entrypoint" --argjson command "$command" '$entrypoint + $command')

rootfs="$bundle_dir/rootfs"
rm -rf "$rootfs"
mkdir -p "$rootfs"
tar -xf "$work_dir/rootfs.tar" -C "$rootfs"

# docker export で失われる起動設定を OCI bundle の config.json として保存する
printf '{"ociVersion":"1.0.2","root":{"path":"rootfs"},"process":{"cwd":"/","args":%s}}\n' \
	"$process_args" > "$bundle_dir/config.json"

echo "bundle ready: $bundle_dir"
