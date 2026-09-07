FROM mcr.microsoft.com/devcontainers/go:1-24-bookworm

USER root

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        etcd-client \
        etcd-server \
        ethtool \
        iproute2 \
        iptables \
        iputils-ping \
        jq \
        nftables \
        uidmap \
        util-linux \
    && rm -rf /var/lib/apt/lists/*

RUN GOBIN=/usr/local/bin go install github.com/go-task/task/v3/cmd/task@v3.39.0

USER vscode
