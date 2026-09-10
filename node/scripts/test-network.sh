#!/bin/sh

set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
runtime_path=$project_root/.toy/bin/runtime
supervisor_path=$project_root/.toy/bin/node-supervisor
kubelet_path=$project_root/.toy/bin/kubelet
bundle_dir=$project_root/bundles
run_log=$project_root/.toy/node-network-test.log
run_pid=

worker_one_socket=$project_root/.toy/nodes/worker-1/runtime.sock
worker_two_socket=$project_root/.toy/nodes/worker-2/runtime.sock

check_requirements() {
	test -x "$runtime_path" || { echo '[node] runtime is not built' >&2; exit 1; }
	test -x "$supervisor_path" || { echo '[node] node-supervisor is not built' >&2; exit 1; }
	test -x "$kubelet_path" || { echo '[node] kubelet is not built' >&2; exit 1; }
	test -f "$bundle_dir/config.json" || { echo '[node] nginx bundle is not prepared' >&2; exit 1; }
	command -v jq >/dev/null 2>&1 || { echo '[node] jq is required' >&2; exit 1; }
	command -v nsenter >/dev/null 2>&1 || { echo '[node] nsenter is required' >&2; exit 1; }
	command -v curl >/dev/null 2>&1 || { echo '[node] curl is required' >&2; exit 1; }
}

cleanup() {
	if [ -n "$run_pid" ]; then
		kill -TERM "$run_pid" 2>/dev/null || true
		wait "$run_pid" 2>/dev/null || true
	fi
}

start_cluster() {
	sh "$project_root/node/scripts/run.sh" --workers 2 >"$run_log" 2>&1 &
	run_pid=$!

	for attempt in $(seq 1 50); do
		if test -S "$worker_one_socket" && test -S "$worker_two_socket"; then
			return 0
		fi
		sleep 0.1
	done

	cat "$run_log" >&2
	exit 1
}

# stdin の JSON request を指定した runtime socket に送る
request() {
	socat - UNIX-CONNECT:"$1"
}

run_sandbox() {
	request "$1" <<EOF
{"op":"run-pod-sandbox","pod":"$2","source":"workload"}
EOF
}

run_container() {
	request "$1" <<EOF
{"op":"run-in-sandbox","pod":"$2","image":"nginx","sandboxID":"$3"}
EOF
}

stop_sandbox() {
	request "$1" <<EOF
{"op":"stop-pod-sandbox","sandboxID":"$2"}
EOF
}

assert_http() {
	pod_pid=$1
	pod_ip=$2
	for attempt in $(seq 1 50); do
		if body=$(nsenter -t "$pod_pid" -n curl --fail --silent "http://$pod_ip/" 2>/dev/null) && printf '%s' "$body" | grep -F 'Hello from toy-kubernetes!' >/dev/null; then
			return 0
		fi
		sleep 0.1
	done
	return 1
}

check_requirements
trap cleanup EXIT INT TERM
start_cluster

sandbox_one=$(run_sandbox "$worker_one_socket" node-a)
sandbox_one_id=$(printf '%s' "$sandbox_one" | jq -er '.sandboxID')
container_one=$(run_container "$worker_one_socket" node-a "$sandbox_one_id")
container_one_pid=$(printf '%s' "$container_one" | jq -er '.id' | sed 's/^pid-//')

sandbox_two=$(run_sandbox "$worker_two_socket" node-b)
sandbox_two_id=$(printf '%s' "$sandbox_two" | jq -er '.sandboxID')
run_container "$worker_two_socket" node-b "$sandbox_two_id" >/dev/null
pod_two_ip=$(printf '%s' "$sandbox_two" | jq -er '.ip')

assert_http "$container_one_pid" "$pod_two_ip"
stop_sandbox "$worker_one_socket" "$sandbox_one_id" >/dev/null

sandbox_three=$(run_sandbox "$worker_one_socket" node-a-again)
sandbox_three_id=$(printf '%s' "$sandbox_three" | jq -er '.sandboxID')
container_three=$(run_container "$worker_one_socket" node-a-again "$sandbox_three_id")
container_three_pid=$(printf '%s' "$container_three" | jq -er '.id' | sed 's/^pid-//')
assert_http "$container_three_pid" "$pod_two_ip"

stop_sandbox "$worker_one_socket" "$sandbox_three_id" >/dev/null
stop_sandbox "$worker_two_socket" "$sandbox_two_id" >/dev/null

echo '[node] inter-node Pod HTTP and route persistence ok'
