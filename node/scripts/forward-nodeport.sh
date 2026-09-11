#!/bin/sh

set -eu

# nested な worker namespace の NodePort を devcontainer の port に公開する。
port=$1
node_address=$2

exec socat "TCP-LISTEN:$port,bind=0.0.0.0,reuseaddr,fork" "TCP:$node_address:$port"
