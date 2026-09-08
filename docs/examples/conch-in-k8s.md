---
title: 在 Kubernetes 中部署 Conch
sidebar_position: 1
---

# 在 Kubernetes 中部署 Conch

本示例将 `conchd` 部署为 Kubernetes 节点上的 Pod，并由 Pod 内的 Conch 启动 StratoVirt microVM Sandbox。`conch-engine` 容器镜像和 `openeuler` 沙箱模板均存放在集群内的镜像仓库中，节点和 Pod 都从该仓库拉取。

本示例涉及的 Dockerfile、DaemonSet 清单、device plugin 源码和验证脚本均位于本文同目录下，可直接查看。下面按章节内联关键片段，完整内容见末尾[示例源码](#示例源码)清单。

## 部署依赖

| 依赖项 | 要求 | 运维操作 |
| --- | --- | --- |
| 容器 capability | `NET_ADMIN` | 允许 Conch 配置 bridge、TAP、路由和 NAT 规则 |
| 容器 capability | `SYS_ADMIN` | 允许 Conch 执行网络命名空间、挂载和文件系统操作 |
| seccomp | `Unconfined` | 放行 StratoVirt、挂载和网络命名空间所需系统调用 |
| AppArmor | `Unconfined` | 放行 KVM、挂载和网络配置操作 |
| KVM 设备 | `/dev/kvm` | 通过 device plugin 注入容器，供 StratoVirt 使用 KVM 加速 |
| VSOCK 设备 | `/dev/vhost-vsock`、`/dev/vsock` | 通过 device plugin 注入容器，建立 conchd 与 conch-init 的 VSOCK 通信 |
| TUN 设备 | `/dev/net/tun` | 通过 device plugin 注入容器，创建 Sandbox TAP 网络设备 |
| Loop 设备 | `/dev/loop-control`、`/dev/loop0`～`/dev/loop7` | 通过 device plugin 注入容器，供 containerd EROFS snapshotter 挂载 rootfs 和 vm view 层 |
| `/proc/sys` | 容器内可写 | 在容器启动命令里 `mount -o remount,rw /proc/sys`，供 Conch 关闭 Sandbox 网络命名空间的 IPv6 并开启 IPv4 转发 |
| 内核特性 | `erofs` | 在节点内核启用 EROFS |

> Loop 设备数量决定并发沙箱上限：每个并发沙箱的 EROFS vm view snapshot 各需占用一个 loop 设备，同一模板的 rootfs layer 文件可共享。device plugin 默认注入 loop0～loop7，支持 8 个并发沙箱；如需更多并发，在 device plugin `paths` 列表和节点 `/dev/loopN` 中补充。
>
> Loop 设备必须通过 device plugin 注入，不能用 hostPath volume。device plugin 会正确设置 cgroup 设备权限；hostPath 注入的块设备在容器内会报 `operation not permitted`。

## 镜像构建

### 准备镜像仓库（可选）

如果你没有可用的镜像仓库，可以在节点上用 `registry:2` 起一个本地 HTTP 仓库。本示例使用域名 `hub.conch.com` 指向该仓库。

在节点上启动 registry（监听 5000，数据持久化到 `/var/lib/hub`）：

```bash
docker run -d --restart=always --name conch-registry \
  -p 5000:5000 -v /var/lib/hub:/var/lib/registry registry:2
```

把域名 `hub.conch.com` 指向节点本机。节点上编辑 `/etc/hosts`：

```bash
echo "127.0.0.1 hub.conch.com" >> /etc/hosts
```

容器运行时需要把该仓库识别为 HTTP（非 TLS）。containerd 通过 certs d 目录配置：

```toml
# /etc/containerd/certs.d/hub.conch.com:5000/hosts.toml
server = "http://hub.conch.com:5000"

[host."http://hub.conch.com:5000"]
  capabilities = ["pull", "resolve"]
```

并在 containerd 主配置中启用 certs d：

```toml
# /etc/containerd/config.toml —— containerd 1.x
[plugins."io.containerd.grpc.v1.cri".registry]
  config_path = "/etc/containerd/certs.d"
```

containerd 2.x 的插件节名不同（`version = 3` 配置）：

```toml
# /etc/containerd/config.toml —— containerd 2.x
[plugins.'io.containerd.cri.v1.images'.registry]
  config_path = '/etc/containerd/certs.d'
```

> `config_path` 只接受**单个目录**。写成 `'/etc/containerd/certs.d:/etc/docker/certs.d'` 这类冒号分隔的多路径时，
> containerd 不会报错，但整个 certs d 配置静默失效，拉取时报
> `http: server gave HTTP response to HTTPS client`。

若用 Docker 构建/推送镜像，在 `/etc/docker/daemon.json` 中添加：

```json
{ "insecure-registries": ["hub.conch.com:5000"] }
```

重启容器运行时使配置生效：

```bash
systemctl restart containerd
systemctl restart docker
```

> 如果你已有自己的镜像仓库，跳过本节，把后文的 `hub.conch.com:5000` 替换为你的仓库地址即可；TLS 仓库无需 `--plain-http`。

### 获取构建产物

Conch 以 RPM 形式发布在 openEuler 24.03-LTS 的 EPOL update 仓库（SP3、SP4 均有），包里已带编译好的二进制、guest kernel 和 initramfs，无需自行构建。**取包版本要与基础镜像一致**，本示例基础镜像是 `24.03-lts-sp4`，因此用 SP4 的包：

```bash
# 节点已配置 EPOL update 源时（仓库配置见 Dockerfile.online）
dnf install -y conch erofs-utils

# 或从包目录下载后直接解包，无需安装到节点
# https://dl-cdn.openeuler.openatom.cn/openEuler-24.03-LTS-SP4/EPOL/update/main/x86_64/Packages/
rpm2cpio conch-<版本>.oe2403sp4.x86_64.rpm | cpio -idm
```

包目录里取最新版本即可；aarch64 把路径中的 `x86_64` 换成 `aarch64`。注意 openEuler 自带的 `[EPOL]` 仓库指向 `EPOL/main/`，而 conch 与 erofs-utils 都在 `EPOL/update/main/`，`dnf` 路径需要另加仓库。`conch` 的依赖里含 `stratovirt`、`containernetworking-plugins`、`virtiofsd`、`iptables`，会一并装上。

构建上下文中 `bin/` 的内容取自 RPM：`/usr/bin/conch`、`conchd`、`conch-init` 直接复制，`/var/lib/conch/kernel` 对应 `bin/vmlinux.bin`，`/var/lib/conch/conch.initrd` 对应 `bin/conch-init.cpio.gz`，Python SDK 为 `/usr/share/conch/wheels/conch-0.2.0-py3-none-any.whl`。

RPM 未包含的两项：device plugin 由本文同目录的 [`main.go`](pathname:///examples/conch-in-k8s/main.go) 编译（`go build -o bin/conch-kvm-device-plugin ./main.go`）；`erofs-utils` 可从同一 EPOL 仓库安装，无法访问该仓库时从 [源码](https://git.kernel.org/pub/scm/linux/kernel/git/xiang/erofs-utils.git) 编译，下方示例 Dockerfile 用的就是后者。

### 构建 conch-engine 镜像（离线）

二进制、内核和 initramfs 都从构建上下文复制进镜像，构建过程只需访问基础镜像。
使用 `hub.oepkgs.net/openeuler/openeuler:24.03-lts-sp4` 作为基础镜像，在镜像中编译 `mkfs.erofs`，并复制 Conch、StratoVirt、guest kernel、initramfs、device plugin、CNI 二进制和 SDK。[`Dockerfile`](pathname:///examples/conch-in-k8s/Dockerfile) 关键部分：

```dockerfile
FROM hub.oepkgs.net/openeuler/openeuler:24.03-lts-sp4
RUN dnf install -y iproute iptables util-linux containernetworking-plugins python3 python3-pip \
    pixman \
    gcc make autoconf automake libtool pkgconf-pkg-config \
    libuuid-devel zlib-devel lz4-devel xz-devel zstd-devel \
    libcurl-devel openssl-devel libxml2-devel json-c-devel && dnf clean all
COPY erofs-utils-src /tmp/erofs-utils
RUN cd /tmp/erofs-utils && ./autogen.sh && ./configure --prefix=/usr \
    && make -j"$(getconf _NPROCESSORS_ONLN)" && make install && rm -rf /tmp/erofs-utils
COPY bin/conch bin/conchd bin/conch-init /usr/local/bin/
COPY bin/conch-kvm-device-plugin /usr/local/bin/conch-kvm-device-plugin
COPY bin/stratovirt /usr/bin/stratovirt
COPY bin/vmlinux.bin bin/conch-init.cpio.gz /opt/conch/
COPY bin/cni/ /usr/libexec/cni/
ENTRYPOINT ["/usr/local/bin/conchd"]
CMD ["--config", "/etc/conch/config.yaml"]
```

构建并推送到 `hub.conch.com:5000`（[`build-image.sh`](pathname:///examples/conch-in-k8s/build-image.sh)）：

```bash
docker build -f Dockerfile -t hub.conch.com:5000/conch/conch-engine:v0.1-x86_64 .
docker push hub.conch.com:5000/conch/conch-engine:v0.1-x86_64
```

> `openeuler` 沙箱模板由 `conch template create` 生成，需要已运行的 `conchd`，因此在 [沙箱管理](#沙箱管理) 中创建并推送到仓库。

### 构建 conch-engine 镜像（在线）

[`Dockerfile.online`](pathname:///examples/conch-in-k8s/Dockerfile.online) 直接从 EPOL 仓库安装 conch、erofs-utils、StratoVirt 和 CNI 插件，
构建上下文里只需要 device plugin 二进制和两个脚本。两种方式产出的镜像等价，按构建环境能否访问外网二选一：

```bash
docker build -f Dockerfile.online -t hub.conch.com:5000/conch/conch-engine:online-x86_64 .
```

要求构建环境能访问 `repo.openeuler.org` 与 PyPI 镜像。RPM 把 kernel 和 initramfs 装在 `/var/lib/conch`，
而 DaemonSet 会把 hostPath 挂到该目录并盖住它们，所以 Dockerfile 中将其复制到 `/opt/conch`。

> 本文的示例脚本需要 conch [`0.1.0-7`](https://atomgit.com/src-openeuler/Conch/pull/20) 及以上：`0.1.0-6` 的
> `conch template create` 不支持 `--name`（模板名自动生成），SDK 的 `Sandbox.create()` 也只接受 `template_id`。


注意：
1. **StratoVirt 特性**：必须包含 `virtio_pmem`、`vhost_vsock`、`vhostuser_fs`，否则会报
   `Unsupported device: "virtio-pmem-pci"`。EPOL 仓库的 `stratovirt-2.4.0-14`（conch RPM 的依赖）已带这些特性。
   如果从 [StratoVirt 源码](https://gitee.com/openeuler/stratovirt) 编译，必须开启这些特性：
   ```bash
   cargo build --release --bin stratovirt --features virtio_pmem,vhost_vsock,vhostuser_fs
   ```

2. **Guest kernel**：ARM64 使用 `Image` 格式（非 x86 的 `bzImage`），通过 Conch 内核配置 `config/oe-kernel/aarch/.config` 编译。

3. **镜像 tag**：ARM64 镜像建议使用 `v0.1-aarch64` tag 以区分 x86 镜像。



## 集群适配

- Conch Pod 使用 `hostNetwork: false` 与 `dnsPolicy: ClusterFirst`，保持 Pod 网络隔离；通过 `hostAliases` 把 `hub.conch.com` 注入 Pod 的 `/etc/hosts` 指向运行 registry 的节点 IP，使 Pod 内能解析并访问本地 registry。运行时目录和状态目录通过 hostPath 挂载到容器内的 `/var/run/conch` 和 `/var/lib/conch`，与节点共用同一目录。
- 节点 `iptables` 的 `FORWARD` 链默认策略需为 `ACCEPT`（或放行 Pod CIDR 到外网的转发），否则 Conch 在 Pod 网络命名空间内创建的 bridge、TAP 和 NAT 规则无法把 Sandbox 流量转发到外网。Docker 安装后常把 `FORWARD` 默认设为 `DROP`，需要恢复：
  ```bash
  iptables -P FORWARD ACCEPT
  ```
- 关闭 firewalld（会阻止 Pod 网络与节点通信）：
  ```bash
  systemctl stop firewalld
  systemctl disable firewalld
  ```
- 单节点集群（control-plane 同时跑工作负载）需给 DaemonSet 加 toleration：
  ```yaml
  tolerations:
    - key: node-role.kubernetes.io/control-plane
      operator: Exists
      effect: NoSchedule
  ```
- 给节点打标签 `conch.io/kvm=true`，使 device plugin 和 Conch DaemonSet 调度到该节点。

`hostAliases` 片段（[`conch-daemonset.yaml`](pathname:///examples/conch-in-k8s/conch-daemonset.yaml)，把 `10.0.0.10` 替换为运行 registry 的节点 IP）：

```yaml
spec:
  template:
    spec:
      hostNetwork: false
      dnsPolicy: ClusterFirst
      hostAliases:
        - ip: 10.0.0.10
          hostnames: [hub.conch.com]
```

> 容器内 PID 1 是 `conchd` 自身，重启时 pid 文件残留会导致 `Failed to acquire pid file` 错误。同时容器的 `/proc/sys` 默认只读，
> 而 Conch 预热网络池时要写 `/proc/sys/net/ipv6/conf/*` 关闭 IPv6，否则 conchd 启动即失败：
> ```text
> Failed to initialize server error=start network pool during startup: ... configure IPv4-only network namespace:
> set IPv4-only sysctl /proc/sys/net/ipv6/conf/all/accept_ra: read-only file system
> ```
> DaemonSet 通过 `command` 在启动前重挂 `/proc/sys` 并清理 pid 文件（有 `SYS_ADMIN` 即可，无需 privileged）：
> ```yaml
> command: ["sh", "-c", "mount -o remount,rw /proc/sys && rm -f /var/run/conch/conchd.pid /var/run/conch/conchd.sock && exec conchd --config /etc/conch/config.yaml"]
> ```

验证节点已注册 `conch.io/kvm` 资源（device plugin 部署后才会出现）：

```console
$ kubectl get node localhost -o jsonpath='{.status.allocatable.conch\.io/kvm}{"\n"}'
1
```

## 部署引擎

先部署 KVM device plugin，由其注册 `conch.io/kvm` 并注入设备；Conch DaemonSet 请求该资源：

```bash
kubectl create namespace conch-system
kubectl apply -f kvm-device-plugin-daemonset.yaml
kubectl apply -f conch-daemonset.yaml
kubectl -n conch-system rollout status daemonset/conch --timeout=180s
```

两个 DaemonSet 均使用 `hub.conch.com:5000/conch/conch-engine:v0.1-x86_64`，并设置 `imagePullPolicy: Always`——镜像内容变化但 tag 不变时，默认策略不会重新拉取，`rollout restart` 仍会沿用节点上的旧镜像。

Conch 容器通过 device plugin 请求 KVM 资源，并添加必要 capability（[`conch-daemonset.yaml`](pathname:///examples/conch-in-k8s/conch-daemonset.yaml)）：

```yaml
securityContext:
  runAsUser: 0
  runAsGroup: 0
  allowPrivilegeEscalation: false
  capabilities:
    drop: [ALL]
    add: [NET_ADMIN, SYS_ADMIN]
  seccompProfile: {type: Unconfined}
  appArmorProfile: {type: Unconfined}   # Kubernetes 1.30+
resources:
  limits:
    conch.io/kvm: "1"
```

> `appArmorProfile` 字段要求 Kubernetes 1.30+。1.29 及更早版本 apply 时会报
> `strict decoding error: unknown field "spec.template.spec.containers[0].securityContext.appArmorProfile"`，
> 改用 Pod 注解：
> ```yaml
> metadata:
>   annotations:
>     container.apparmor.security.beta.kubernetes.io/conchd: unconfined
> ```

device plugin 在 `Allocate` 响应中注入设备和 `rwm` 权限。完整源码见 [`main.go`](pathname:///examples/conch-in-k8s/main.go)，核心部分：

```go
paths := []string{
    "/dev/kvm", "/dev/vhost-vsock", "/dev/vsock",
    "/dev/net/tun",
    "/dev/loop-control", "/dev/loop0", "/dev/loop1", "/dev/loop2",
    "/dev/loop3", "/dev/loop4", "/dev/loop5", "/dev/loop6", "/dev/loop7",
}
for _, path := range paths {
    devices = append(devices, &dp.DeviceSpec{
        HostPath: path, ContainerPath: path, Permissions: "rwm",
    })
}
```

device plugin 注册后节点应显示 `conch.io/kvm: 1`，Conch Pod 为 `Running`：

```console
$ kubectl -n conch-system get pods -l app=conch
NAME          READY   STATUS    NODE
conch-ffspc   1/1     Running   localhost
```

## 沙箱管理

沙箱管理包括沙箱启动与沙箱执行命令两部分。以下操作均在 Conch Pod 内执行，先进入 Pod：

```bash
POD=$(kubectl -n conch-system get pods -l app=conch -o jsonpath='{.items[0].metadata.name}')
kubectl -n conch-system exec -it "$POD" -- bash
```

### 沙箱启动

沙箱启动前先用 `conch template create` 从基础镜像生成 `openeuler` 模板，并 `conch template push` 推送到仓库。[`build-cold-template.sh`](pathname:///examples/conch-in-k8s/build-cold-template.sh) 完成这两步：

```bash
kubectl -n conch-system exec "$POD" -- env CONCH_API_TIMEOUT=30m \
  /usr/local/bin/build-cold-template.sh
```

脚本等价于：

```bash
conch template create --config /etc/conch/config.yaml \
  --name hub.conch.com:5000/conch/openeuler-bzimage:24.03-lts-sp4 \
  --source hub.oepkgs.net/openeuler/openeuler:24.03-lts-sp4 \
  --kernel /opt/conch/vmlinux.bin --initrd /opt/conch/conch-init.cpio.gz
conch template push --plain-http --config /etc/conch/config.yaml \
  hub.conch.com:5000/conch/openeuler-bzimage:24.03-lts-sp4 \
  hub.conch.com:5000/conch/openeuler-bzimage:24.03-lts-sp4
```

模板创建后从仓库拉取，冷启动一个 Sandbox，创建快照模板并推送到仓库，再从快照启动两个 Sandbox。[`start-sandbox.py`](pathname:///examples/conch-in-k8s/start-sandbox.py) 完成完整流程：

```bash
kubectl -n conch-system exec "$POD" -- env CONCH_API_TIMEOUT=180s \
  python3 /usr/local/bin/start-sandbox.py
```

核心 SDK 调用：

```python
from conch import Sandbox

SOURCE = "hub.conch.com:5000/conch/openeuler-bzimage:24.03-lts-sp4"
SNAPSHOT = "hub.conch.com:5000/conch/openeuler-snapshot:latest"

# 从仓库拉取模板（脚本内部调用 conch template pull --plain-http）
source = Sandbox.create(template_name=SOURCE, sandbox_id="k8s-template-start")
snapshot = source.checkpoint(SNAPSHOT)
# 脚本对快照执行 template push/pull --plain-http 后再启动
for sandbox_id in ("k8s-snapshot-start-1", "k8s-snapshot-start-2"):
    Sandbox.create(template_name=snapshot.template_name, sandbox_id=sandbox_id)
```

脚本会打印每次启动耗时和 IP，以及命令执行结果。以下为 x86_64 和 aarch64 单节点集群实测输出：

**x86_64 实测**：

```console
k8s-template-start: 0.473s, ip=10.13.0.72
k8s-template-start ensurepip exit=0
k8s-template-start pip install math packages exit=0
snapshot template: hub.conch.com:5000/conch/openeuler-snapshot:latest (sha256:0d4e9a2eb182ec4a08bad0eb3654be3a62fc0ec7f48ee238791ffe3a5bd56405)
k8s-snapshot-start-1: 1.386s, ip=10.13.0.73
k8s-snapshot-start-1 ensurepip exit=0
k8s-snapshot-start-1 pip install math packages exit=0
k8s-snapshot-start-2: 0.062s, ip=10.13.0.74
k8s-snapshot-start-2 ensurepip exit=0
k8s-snapshot-start-2 pip install math packages exit=0
```

**aarch64 实测**：

```console
k8s-template-start: 2.148s, ip=10.13.0.52
k8s-template-start ensurepip exit=0
k8s-template-start pip install math packages exit=0
snapshot template: hub.conch.com:5000/conch/openeuler-snapshot:latest (sha256:a56ede27b67b959b8874f5c4803aa37d1e434b0b127178c17232780d1aa2fea4)
k8s-snapshot-start-1: 1.868s, ip=10.13.0.53
k8s-snapshot-start-1 ensurepip exit=0
k8s-snapshot-start-1 pip install math packages exit=0
k8s-snapshot-start-2: 1.009s, ip=10.13.0.54
k8s-snapshot-start-2 ensurepip exit=0
k8s-snapshot-start-2 pip install math packages exit=0
```

x86_64 冷启动约 0.47s，aarch64 约 2.15s；从快照模板首次启动（含 view 拉取）x86 约 1.39s、ARM 约 1.87s；再次启动（view 已缓存）x86 约 0.06s、ARM 约 1.01s。三次启动均获得 IP 并成功执行命令。

### 沙箱执行命令

通过 SDK 的 `sandbox.commands.run` 在 Sandbox 内执行命令。[`start-sandbox.py`](pathname:///examples/conch-in-k8s/start-sandbox.py) 在每次启动后依次执行 `ensurepip` 与 pip 安装：

```python
result = sandbox.commands.run(
    cmd="python3",
    args=["-m", "ensurepip", "--upgrade"],
)
print(f"{sandbox_id} ensurepip exit={result.exit_code}")
result = sandbox.commands.run(
    cmd="python3",
    args=[
        "-m", "pip", "install",
        "-i", "https://mirrors.aliyun.com/pypi/simple/",
        "--trusted-host", "mirrors.aliyun.com",
        "numpy", "sympy", "mpmath",
    ],
)
print(f"{sandbox_id} pip install math packages exit={result.exit_code}")
```

执行失败时 SDK 抛出 `CommandExitException`，脚本捕获后打印失败信息而不中断后续 Sandbox。

## 环境清理

```bash
kubectl -n conch-system delete daemonset conch conch-kvm-device-plugin
kubectl delete namespace conch-system
```

如需清理 registry 容器（`/var/lib/hub`、`/var/run/conch`、`/var/lib/conch` 保留持久化）：

```bash
docker rm -f conch-registry
```

## 示例源码

本示例所有源码保持原始文件格式，点击可直接查看或下载完整内容：

| 文件 | 说明 |
| --- | --- |
| [`Dockerfile`](pathname:///examples/conch-in-k8s/Dockerfile) | `conch-engine` 镜像构建文件（离线，二进制来自构建上下文） |
| [`Dockerfile.online`](pathname:///examples/conch-in-k8s/Dockerfile.online) | `conch-engine` 镜像构建文件（在线，从 EPOL 仓库安装） |
| [`build-image.sh`](pathname:///examples/conch-in-k8s/build-image.sh) | 构建并推送 `conch-engine` 镜像 |
| [`main.go`](pathname:///examples/conch-in-k8s/main.go) | KVM device plugin 源码 |
| [`kvm-device-plugin-daemonset.yaml`](pathname:///examples/conch-in-k8s/kvm-device-plugin-daemonset.yaml) | device plugin DaemonSet 清单 |
| [`conch-daemonset.yaml`](pathname:///examples/conch-in-k8s/conch-daemonset.yaml) | Conch DaemonSet 清单 |
| [`build-cold-template.sh`](pathname:///examples/conch-in-k8s/build-cold-template.sh) | 创建并推送 `openeuler` 冷启动模板 |
| [`start-sandbox.py`](pathname:///examples/conch-in-k8s/start-sandbox.py) | 沙箱启动与命令执行验证脚本 |
