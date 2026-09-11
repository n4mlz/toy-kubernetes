#!/bin/sh

set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
api_server=http://10.200.0.2:8080
control_plane=control-plane
toyctl=$project_root/.toy/bin/toyctl
run_log=$project_root/.toy/e2e.log
run_pid=

cleanup() {
	if [ -n "$run_pid" ]; then
		kill -TERM "$run_pid" 2>/dev/null || true
		wait "$run_pid" 2>/dev/null || true
	fi
	sh "$project_root/node/scripts/clean.sh" >/dev/null 2>&1 || true
}

fail() {
	echo "[e2e] $1" >&2
	if [ -f "$run_log" ]; then
		cat "$run_log" >&2
	fi
	exit 1
}

wait_for_api() {
	for attempt in $(seq 1 100); do
		if ip netns exec "$control_plane" curl --fail --silent --connect-timeout 1 --max-time 2 "$api_server/pods" >/dev/null 2>&1; then
			return 0
		fi
		sleep 0.2
	done
	fail 'API server did not become ready'
}

pod_json() {
	for attempt in $(seq 1 150); do
		value=$(ip netns exec "$control_plane" curl --fail --silent --connect-timeout 1 --max-time 2 "$api_server/pods" 2>/dev/null | jq -c '[.items[] | select(.metadata.labels.app == "nginx" and .status.phase == "Running" and .spec.nodeName != "")][0] // empty' 2>/dev/null || true)
		if [ -n "$value" ]; then
			printf '%s\n' "$value"
			return 0
		fi
		sleep 0.2
	done
	return 1
}

assert_service() {
	node_port=$1
	for attempt in $(seq 1 30); do
		for node in worker-1 worker-2; do
		case "$node" in
		worker-1) node_address=10.200.0.3 ;;
		worker-2) node_address=10.200.0.4 ;;
		esac
		if ip netns exec "$node" curl --fail --silent --connect-timeout 1 --max-time 1 "http://$node_address:$node_port/" | grep -F 'Hello from toy-kubernetes!' >/dev/null; then
				return 0
			fi
		done
		sleep 0.2
	done
	return 1
}

check_requirements() {
	test -x "$toyctl" || fail 'toyctl is not built'
	command -v curl >/dev/null 2>&1 || fail 'curl is required'
	command -v jq >/dev/null 2>&1 || fail 'jq is required'
	command -v ip >/dev/null 2>&1 || fail 'iproute2 is required'
}

check_requirements
trap cleanup EXIT INT TERM

sh "$project_root/node/scripts/run.sh" --workers 2 >"$run_log" 2>&1 &
run_pid=$!
wait_for_api

ip netns exec "$control_plane" "$toyctl" apply -f "$project_root/manifests/nginx.yaml" >/dev/null || fail 'applying nginx manifest failed'

pod=$(pod_json) || fail 'nginx Pod did not become Running'
pod_name=$(printf '%s' "$pod" | jq -er '.metadata.name')
node_name=$(printf '%s' "$pod" | jq -er '.spec.nodeName')
service=$(ip netns exec "$control_plane" curl --fail --silent --connect-timeout 1 --max-time 2 "$api_server/services/nginx")
node_port=$(printf '%s' "$service" | jq -er '.spec.nodePort')

assert_service "$node_port" || fail 'NodePort Service did not return the nginx response'

socket="$project_root/.toy/nodes/$node_name/runtime.sock"
container_pid=$(printf '{"op":"inspect","pod":"%s"}\n' "$pod_name" | socat - UNIX-CONNECT:"$socket" | jq -er '.id' | sed 's/^pid-//')
[ -n "$container_pid" ] || fail 'nginx container PID was not found'
kill -KILL "$container_pid"

recovered=false
for attempt in $(seq 1 150); do
	if current=$(pod_json 2>/dev/null); then
		current_name=$(printf '%s' "$current" | jq -er '.metadata.name')
		if [ "$current_name" = "$pod_name" ] && assert_service "$node_port"; then
			recovered=true
			break
		fi
	fi
	sleep 0.2
done
[ "$recovered" = true ] || fail 'nginx did not recover after its container was killed'

echo '[e2e] apply, scheduling, Service HTTP, and container recovery ok'
