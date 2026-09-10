# Toy Kubernetes のアーキテクチャ

## 目的と前提

このプロジェクトは Kubernetes の完全互換実装ではない。宣言的な共有状態を API server に保存し、複数の component がそれぞれの責務を reconcile する流れを、実際に Pod と Service が動くところまで確認するための実装である。

対象環境は Linux の devcontainer 内とし、devcontainer 内の root 権限で動作させる。control plane は 1 固定、worker 数は起動時に指定できる。動作確認のデフォルトは 1 control plane と 2 worker である。

control plane の HA、etcd の複数 node 化、leader election は今回の対象外である。worker 数は 1 以上とし、worker の増減に合わせて namespace、CIDR、route を生成する。

対象リソースは Node、Pod、Deployment、ReplicaSet、Service に限定する。

## 全体構成

~~~text
                    control-plane namespace(s)
  +-------------------------------------------------------------+
  | etcd                                                       |
  |   ^                                                        |
  |   |                                                        |
  | kube-apiserver <---- kube-controller-manager               |
  |       ^  ^              - Deployment reconciler             |
  |       |  |              - ReplicaSet reconciler              |
  |       |  +---------- kube-scheduler                         |
  |       |                                                     |
  |       +---------------- toyctl / clients                    |
  +--------------------------+----------------------------------+
                             | underlay network
              +--------------+---------------+
              |                              |
       worker-1 namespace              worker-2 namespace
       +------------------+             +------------------+
       | kubelet          |             | kubelet          |
       | runtime          |             | runtime          |
       | kube-proxy       |             | kube-proxy       |
       | cni0             |             | cni0             |
       |   |              |             |   |              |
       | Pod netns ...     |             | Pod netns ...     |
       +------------------+             +------------------+
~~~

Linux network namespace は厳密には親子関係を持たない。図では、worker が管理するネットワーク resource を追いやすくするために階層として表現している。

control plane の状態は共有 etcd に保存する。worker は API server を共有し、各 worker の kubelet、runtime、kube-proxy は自分の Node に関係する状態だけを扱う。図の worker-1、worker-2 はデフォルト構成の例であり、実際には指定された数だけ作成する。

## Component の責務

### etcd と kube-apiserver

etcd はクラスタ resource を永続化する stateful な Store である。Kubernetes API の意味付けや reconcile は行わない。

kube-apiserver は resource の作成・取得・更新・削除の入口である。Store の保存形式を隠し、validation、resource version、watch event を提供する。API server 自体は Reconciler ではなく、共有状態を保存・公開する component である。

### kube-controller-manager

複数の Reconciler を一つのプロセスで動かす。

- DeploymentController: Deployment から ReplicaSet を作成・更新する
- ReplicaSetController: desired replicas と管理対象 Pod 数の差分を埋める

Reconciler の基本形は、observe current state → desired state との差分計算 → action の選択 → API state の更新または effector の呼び出し、である。

### kube-scheduler

nodeName が空の Pod と Ready な Node を観測し、Pod を worker に bind する。最初の選択方式は round-robin とする。Pod を起動するのではなく、Pod の配置先を API state に反映する。

### kubelet

自分の Node に bind された Pod を desired state とし、runtime の current state と比較する。必要に応じて runtime に start/stop を依頼し、Pod status を API server に反映する。

Kubelet は worker namespace に常駐する node agent であり、runtime や CNI の内部処理を直接担当しない。

control plane 用 kubelet は API server のリソースを待たず、node の local manifest directory を desired state として扱う。manifest の追加・更新・削除を検出し、static Pod を CRI runtime に起動・停止させる。CRI の PodSandbox には `staticPod` または `workload` の source を付け、manifest から消えた static Pod の sandbox だけを停止する。API server URL が設定されている場合は、static Pod に対応する mirror Pod も API server に登録する。mirror Pod は API 上の表示であり、local manifest が source of truth である。

### CRI runtime と CNI

CRI runtime は kubelet からの簡易 CRI protocol を受け、OCI bundle の `config.json` にある process.args に従って container lifecycle を実行する。`RunPodSandbox` で Pod の namespace とネットワークを用意し、`RunInSandbox` でその namespace に container process を参加させる。これは本家 CRI の `RunPodSandbox`、`CreateContainer`、`StartContainer` を、ハンズオン向けに `RunInSandbox` へまとめた形である。sandbox の待機 process は pause container 相当だが、専用 image は使用しない。CRI runtime は rootful に動作し、コンテナ process の rootfs、PID、mount、UTS、network namespace の作成と終了処理を担当する。リポジトリ上の実装ディレクトリは cri/runtime とする。

CNI は Pod network namespace と worker の bridge を veth pair で接続し、Pod IP と route を設定する。CRI runtime が sandbox 作成時に ADD、sandbox 停止時に DEL を呼び出す。CNI は network resource を作る imperative な effector である。

### kube-proxy

Service と endpoint を観測し、worker namespace の forwarding state を更新する。Service の ClusterIP:port から selector に一致する Pod IP:targetPort へ転送する。

## 主要な状態遷移

~~~text
Deployment applied
        |
        v
ReplicaSet created or updated
        |
        v
Pod objects created with nodeName=""
        |
        v
Scheduler sets pod.spec.nodeName
        |
        v
Kubelet asks runtime to start the Pod
        |
        v
Runtime asks CNI to connect the Pod network
        |
        v
Pod status becomes Running and receives podIP
        |
        v
kube-proxy updates Service forwarding
        |
        v
client -> Service ClusterIP -> nginx Pod
~~~

Pod runtime の lifecycle は次の通りである。

~~~text
Kubelet
  -> RunPodSandbox
       -> sandbox namespace を作る
       -> CNI ADD
  -> RunInSandbox
       -> container process を sandbox namespace に参加させる
  -> StopPodSandbox
       -> container process を停止
       -> CNI DEL
       -> sandbox を停止
~~~

同じ状態遷移は Pod の削除や process の終了にも適用する。controller と kubelet が差分を再検出し、desired state に戻す。

## Resource の最小モデル

spec は desired state、status は component が観測した current state である。

~~~text
Node: metadata.name, spec.podCIDR, status.phase
Pod: metadata.name, metadata.labels, spec.nodeName,
     spec.containers[].name, spec.containers[].image,
     status.phase, status.podIP
Deployment: metadata.name, spec.replicas, spec.selector, spec.template
ReplicaSet: metadata.name, metadata.ownerReferences,
            spec.replicas, spec.selector, spec.template
Service: metadata.name, spec.selector, spec.clusterIP,
         spec.port, spec.targetPort
~~~

## API と watch

本家 Kubernetes の core API は API version を表す /api/v1 を使う。これは複数 version や API group を共存させるための仕組みである。一方、このプロジェクトは Kubernetes API 互換や version migration を扱わないため、不要な version prefix は持たせない。

API server は resource ごとに次の操作を提供する。

~~~text
GET    /<resources>
POST   /<resources>
GET    /<resources>/<name>
PUT    /<resources>/<name>
DELETE /<resources>/<name>
GET    /watch/<resources>
~~~

watch event は ADDED、MODIFIED、DELETED のいずれかと resource 本体を含む。controller、scheduler、kubelet、kube-proxy は typed client 経由で API server を利用する。

toyctl apply は YAML を読み込み、対象 kind に対応する API endpoint へ create または update を行う。kubectl 互換の discovery、OpenAPI、server-side apply、RBAC などは対象外とする。

## worker network の構成

worker には設定からそれぞれ固有の Pod CIDR を割り当てる。CIDR、Service の address range、bridge 名、port、socket path などの値はコードや script に散在させず、const.go 等の設定箇所から参照する。

~~~text
worker-1: worker index から生成した Pod CIDR
  cni0:   Pod CIDR の gateway

worker-2: worker index から生成した Pod CIDR
  cni0:   Pod CIDR の gateway
~~~

worker 間の underlay network を通じて、相手 worker の Pod CIDR への route を設定する。Pod network は worker network namespace 内の bridge に接続する。worker 数を増減しても、CIDR と route は worker index から生成する。

Service は専用の仮想 process を作らず、kube-proxy が worker namespace の forwarding rule として実現する。

## 仮想 node の作成と Kubernetes の起動

起動処理は、仮想 node の作成と Kubernetes 本体の起動に分ける。

### 仮想 node の作成

1. control plane と指定された数の worker 用 network namespace を作る
2. underlay network、worker bridge、worker 間 route を作る
3. 各 node の設定、static Pod manifest directory、CRI socket、ログ directory を用意する
4. 各 node namespace の init process として、小さい systemd 相当の node supervisor を起動する

node supervisor は、node namespace 内で常駐 process の起動、終了監視、終了時の cleanup を担当する。これは systemd の全機能を再現するものではなく、unit の依存関係、restart、signal forwarding に必要な最小機能だけを持つ。

起動処理の責務は、ホスト側の launcher、node namespace 内の supervisor、supervisor が管理する unit に分ける。

~~~text
task run
  └─ node/scripts/run.sh                 ホスト側で仮想 node 全体を起動・停止する
       ├─ control-plane namespace
       │    └─ node-supervisor             node 内の unit を管理する
       │         ├─ CRI runtime             container lifecycle を実行する
       │         └─ kubelet                 Pod を CRI runtime に反映する
       ├─ worker-1 namespace
       │    └─ node-supervisor
       │         ├─ CRI runtime
       │         └─ kubelet
       └─ worker-2 namespace
            └─ node-supervisor
                 ├─ CRI runtime
                 └─ kubelet
~~~

`node/scripts/run.sh` は namespace、node directory、supervisor の起動と、終了時の namespace cleanup を担当する。`cmd/node-supervisor/main.go` は supervisor executable の入口として、node 内で起動する unit を組み立てる。`node/supervisor.go` は unit の process group、ログ、終了監視、signal forwarding を管理する。CRI runtime と kubelet は同じ supervisor が管理する。

`node/scripts/run.sh` の Pod network prefix、network mask、gateway host、bridge 名、各 executable の path は toy cluster の既定値として script 冒頭にまとめて定義する。worker 数は起動時に変更でき、node root は `TOY_NODE_ROOT` で実行環境に合わせて変更できる。デフォルト値は 1 control plane と 2 worker の動作確認用である。

### Kubernetes 本体の起動

実際の kubeadm 構成に寄せ、control plane の bootstrap は次の順序にする。

1. node supervisor が CRI runtime と kubelet を起動する
2. control plane の kubelet が local static Pod manifest を読み込む
3. kubelet が CRI runtime に etcd、kube-apiserver、kube-scheduler、kube-controller-manager の static Pod を起動させる
4. API server の readiness を待つ
5. worker の kubelet と CRI runtime を起動し、Node object を登録する
6. API server 起動後に kube-proxy を worker 上の通常 workload として起動する
7. kubelet が API state と CRI runtime state を reconcile する

実 Kubernetes でも、kubelet と container runtime は host service manager から起動し、kubeadm の control plane component は kubelet が static Pod として起動する。この toy implementation では、その host service manager と static Pod の境界を、仮想 node と node supervisor で見える形にする。

停止時は、Pod、CNI resource、worker の通常 workload、static Pod、kubelet/CRI runtime、node supervisor、network namespace、underlay、control plane process、etcd の順に、所有している resource を cleanup する。起動 script は Ctrl-C と異常終了の両方で cleanup を実行する。

## 意図的に単純化するもの

- Kubernetes API の完全互換、kubectl 互換、認証認可、admission、OpenAPI は扱わない。
- control plane の複数 node 化、control-plane HA、etcd の複数 node 化は扱わない。
- image registry、OCI image format、複数 image、container runtime の完全な CRI は扱わない。
- scheduler の複雑な filter/score、resource request、taint/toleration は扱わない。
- CNI はこの toy cluster の bridge、veth、IP、route に必要な処理へ限定する。
- kube-proxy は Service と endpoint の forwarding を理解するための最小実装とする。

これらは省略ではなく、manifest → shared state → reconcile → node effect → network effect という学習対象を見失わないための境界である。
