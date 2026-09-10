#!/bin/sh

set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
runtime_path=$project_root/.toy/bin/runtime
bundle_dir=$project_root/bundles
socket_path=$project_root/.toy/cni-test.sock
log_path=$project_root/.toy/cni-test.log
bridge_name=toy-cni-test0
pod_cidr=10.244.77.0/24
gateway=10.244.77.1
sandbox_a=
sandbox_b=
runtime_pid=

cleanup() {
	if [ -n "$sandbox_a" ] && [ -S "$socket_path" ]; then
		printf '%s\n' "{\"op\":\"stop-pod-sandbox\",\"sandboxID\":\"$sandbox_a\"}" |
			socat - UNIX-CONNECT:"$socket_path" >/dev/null 2>&1 || true
	fi
	if [ -n "$sandbox_b" ] && [ -S "$socket_path" ]; then
		printf '%s\n' "{\"op\":\"stop-pod-sandbox\",\"sandboxID\":\"$sandbox_b\"}" |
			socat - UNIX-CONNECT:"$socket_path" >/dev/null 2>&1 || true
	fi
	if [ -n "$runtime_pid" ]; then
		kill "$runtime_pid" 2>/dev/null || true
		wait "$runtime_pid" 2>/dev/null || true
	fi
	ip link del "$bridge_name" 2>/dev/null || true
	rm -f "$socket_path"
}
trap cleanup EXIT INT TERM

command -v curl >/dev/null 2>&1 || { echo '[cni] curl is required' >&2; exit 1; }
command -v ip >/dev/null 2>&1 || { echo '[cni] ip is required' >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo '[cni] jq is required' >&2; exit 1; }
command -v nsenter >/dev/null 2>&1 || { echo '[cni] nsenter is required' >&2; exit 1; }
command -v socat >/dev/null 2>&1 || { echo '[cni] socat is required' >&2; exit 1; }
test -x "$runtime_path" || { echo '[cni] runtime is not built' >&2; exit 1; }
test -f "$bundle_dir/config.json" || { echo '[cni] bundle is not prepared' >&2; exit 1; }

mkdir -p "$project_root/.toy"
rm -f "$socket_path"
"$runtime_path" \
	--socket "$socket_path" \
	--bundle-dir "$bundle_dir" \
	--bridge "$bridge_name" \
	--pod-cidr "$pod_cidr" \
	--gateway "$gateway" >"$log_path" 2>&1 &
runtime_pid=$!

for attempt in $(seq 1 50); do
	test -S "$socket_path" && break
	sleep 0.1
done
test -S "$socket_path"

response=$(printf '%s\n' '{"op":"run-pod-sandbox","pod":"http-a","source":"workload"}' | socat - UNIX-CONNECT:"$socket_path")
sandbox_a=$(printf '%s' "$response" | jq -er '.sandboxID')
response=$(printf '%s\n' '{"op":"run-pod-sandbox","pod":"http-b","source":"workload"}' | socat - UNIX-CONNECT:"$socket_path")
sandbox_b=$(printf '%s' "$response" | jq -er '.sandboxID')
ip_b=$(printf '%s' "$response" | jq -er '.ip')

printf '%s\n' "{\"op\":\"run-in-sandbox\",\"pod\":\"http-a\",\"image\":\"nginx\",\"sandboxID\":\"$sandbox_a\"}" |
	socat - UNIX-CONNECT:"$socket_path" | jq -e '.ok and .state == "Running"' >/dev/null
printf '%s\n' "{\"op\":\"run-in-sandbox\",\"pod\":\"http-b\",\"image\":\"nginx\",\"sandboxID\":\"$sandbox_b\"}" |
	socat - UNIX-CONNECT:"$socket_path" | jq -e '.ok and .state == "Running"' >/dev/null

sleep 2
pod_a_pid=$(printf '%s\n' '{"op":"inspect","pod":"http-a"}' | socat - UNIX-CONNECT:"$socket_path" | jq -er '.id | sub("pid-"; "")')
nsenter -t "$pod_a_pid" -n curl --fail --silent --show-error --max-time 5 "http://$ip_b/" |
	grep -F 'Hello from toy-kubernetes!' >/dev/null

ip -o link show master "$bridge_name" | grep -q .
printf '%s\n' "{\"op\":\"stop-pod-sandbox\",\"sandboxID\":\"$sandbox_a\"}" | socat - UNIX-CONNECT:"$socket_path" | jq -e '.ok' >/dev/null
printf '%s\n' "{\"op\":\"stop-pod-sandbox\",\"sandboxID\":\"$sandbox_b\"}" | socat - UNIX-CONNECT:"$socket_path" | jq -e '.ok' >/dev/null
sandbox_a=
sandbox_b=

if ip -o link show master "$bridge_name" | grep -q .; then
	echo '[cni] veth cleanup failed' >&2
	exit 1
fi

echo '[cni] Pod-to-Pod HTTP and veth cleanup ok'
