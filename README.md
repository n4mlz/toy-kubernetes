# toy-kubernetes

Kubernetes の主要な構成要素を、Linux の network namespace と実際のコンテナ process で学ぶための小さな実装です。

## 起動

devcontainer を起動し、コンテナ内で実行します。

```sh
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . task check
devcontainer exec --workspace-folder . task e2e
```

`task e2e` は、control plane 1 台と worker 2 台を起動し、nginx の manifest を apply します。Pod の起動、Service 経由の HTTP、コンテナ process を kill した後の復旧まで確認し、最後に cluster を停止します。

手動で確認する場合は、次のコマンドを実行したままにします。

```sh
task run -- --workers 2
```

別の terminal で manifest を apply します。

```sh
./.toy/bin/toyctl apply -f manifests/nginx.yaml
./.toy/bin/toyctl get pods/nginx-rs-1
```

Service の NodePort は worker namespace の IP で公開されます。デフォルトでは worker-1 が `10.200.0.3`、worker-2 が `10.200.0.4` です。ブラウザで次の URL を開くと nginx にアクセスできます。

```text
http://10.200.0.3:30000/
```

NodePort は Service 作成時に `30000` から割り当てられます。実際の port は次で確認できます。

```sh
curl http://10.200.0.2:8080/services/nginx
```

停止は、`task run` を実行している terminal で `Ctrl-C` を押します。

## 開発用コマンド

```sh
task build
task test
task check
task images:prepare-bundle
task e2e
```
