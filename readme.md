# Lhotse Agent

Lhotse Agent 是一款专为 AI Agent 生产环境设计的**透明网络代理与凭证保险箱 (Transparent Proxy & Credential Vault)**。
它以 Kubernetes Sidecar 形式运行，在最底层网络边界拦截容器的所有出入站流量，执行严格的访问控制策略（Domain Policy）和零信任凭证注入（Credential Injection）。

## 核心特性 (Core Features)

1. **透明流量劫持 (Transparent Interception)**
   - 配合 `lhotse-iptables` Init Container，自动在内核层使用 `iptables NAT REDIRECT` 将所有出站/入站 TCP 流量无感劫持至 Lhotse 代理进程。
   - 利用 `SO_ORIGINAL_DST` 完美还原应用程序试图访问的原始目的 IP 和端口，对业务代码完全透明（无需配置 `HTTP_PROXY`）。

2. **多协议检测与审计 (Protocol Detection & Audit)**
   - 自动窥探 (Peek) 连接的前 5 个字节，精准识别 HTTP、TLS (HTTPS) 和纯 TCP 流量。
   - 基于提取出的 HTTP Host 头或 TLS SNI 扩展字段，执行后续的访问策略。
   - 所有连接的源地址、目标地址、协议类型及放行/阻断决策均被详细审计记录。

3. **动态网络沙箱策略 (Dynamic Domain Policy)**
   - 支持基于 域名、通配符（如 `*.github.com`）、IP 和 CIDR 块的 AllowList / BlockList 控制。
   - 默认**阻断**高危内部端点访问（如云厂商元数据接口 `169.254.169.254` 防护 SSRF）。
   - 策略可在启动时从远端统一配置中心动态拉取，并支持周期性无损刷新。

4. **MITM 凭证动态注入 (Credential Vault) *(New)***
   - Agent 容器内无需存放任何明文 API Key (如 GitHub Token, Jira API Key)。
   - Lhotse Agent 对白名单目标（如 `api.github.com`）执行 MITM TLS 劫持，解密 HTTP 流量，向统一控制面查询当前域名的真实 Token，并将其动态注入到 HTTP Header (`Authorization: Bearer <token>`) 后再加密转发给目标服务器。
   - 真正做到“Agent 拿得到调用凭证引用，但永远传不出明文密钥”，彻底防范大模型 Prompt Injection 导致的密钥泄露。

5. **无损热升级 (Zero-Downtime Upgrade)**
   - 通过 `tableflip` 等机制支持进程平滑重启，升级 Lhotse Agent 版本时正在处理的 HTTP 长连接不会被强行中断。

---

## 启动与参数说明 (Usage & Flags)

启动透明代理：

```bash
lhotse-agent proxy [flags]
```

### 核心监听端口
- `-o, --outbound-port int32`: 出口流量透明代理监听端口，默认 `15001`
- `-i, --inbound-port int32`: 入口流量透明代理监听端口，默认 `15006`
- `-m, --mgr-port int32`: 内部管理与监控端口，默认 `15030`
- `--udp-port int32`: UDP 流量代理端口，默认 `15009`

### 动态策略配置 (Domain Policy)
如果您的组织有统一的控制面下发白名单，可以使用以下参数开启动态策略同步：

- `--domain-policy-url string`: 动态拉取域名/IP 访问控制策略的 HTTP API 地址。
- `--app-name string`: 提交给策略中心的当前应用名称（必填，当 url 存在时）。
- `--instance-ip string`: 当前实例的 IP（非必填，自动探测）。
- `--domain-policy-scope string`: 策略作用方向：`outbound` (默认)、`inbound` 或 `both`。
- `--domain-policy-refresh-interval duration`: 策略定期刷新间隔，默认 `5m` (5 分钟)；设为 `0` 代表仅启动时拉取一次。
- `--domain-policy-fetch-timeout duration`: 请求策略的超时时间，默认 `5s`。
- `--domain-policy-cache-file string`: 拉取成功后的本地持久化缓存文件，默认 `/tmp/lhotse-domain-policy-cache.json`。

> *当策略 URL 拉取失败时，Lhotse 会自动退级使用本地 `cache-file`，如果缓存也不存在，则默认回退到全放行模式以保障可用性，请在测试时留意。*

### MITM 与凭证保险箱参数 (Credential Vault)

当需要对特定 HTTPS 目标做 TLS MITM 和动态凭证注入时，启用以下参数：

- `--mitm-enabled`: 开启 MITM 模式。开启后会从 Vault 拉取 CA、运行时规则和凭证。
- `--vault-uri string`: Vault / 控制面基地址，Lhotse 会访问 `/internal/ca/keypair`、`/internal/credential-runtime-config`、`/internal/credentials/resolve`、`/internal/identity/resolve`。
- `--agent-id string`: 当前 Agent 标识，用于凭证匹配。
- `--upstream-ca-file string`: MITM 后 sidecar 重拨真实上游时额外追加的 PEM trust bundle。未设置时会尝试读取 `SSL_CERT_FILE`，再回退到系统根证书。
- `--ca-poll-interval duration`: CA 轮询刷新间隔，默认 `5m`。
- `--ca-init-timeout duration`: 启动阶段阻塞等待首个 CA 的超时时间，默认 `2m`。
- `--credential-runtime-cache-file string`: MITM 运行时配置缓存文件，默认 `/tmp/lhotse-credential-runtime-cache.json`。
- `--credential-runtime-refresh-interval duration`: 运行时配置刷新间隔，默认 `5m`。
- `--credential-runtime-fetch-timeout duration`: 运行时配置拉取超时，默认 `5s`。

管理端口 `:15030` 会额外暴露：

- `GET /ca.crt`: 当前系统根证书 + Lhotse Active CA bundle，供业务容器下载后建立信任。
- `GET /credential-runtime`: 当前 MITM 运行时状态。
- `POST /credential-runtime/reload`: 触发运行时配置立即刷新。

如果真实上游使用私有 CA，建议显式挂载 PEM 文件并通过 `--upstream-ca-file` 传入；`SSL_CERT_FILE` 只作为未设置该参数时的 fallback。

### 业务容器 CA 引导脚本

仓库提供 `scripts/lhotse-ca-init.sh`，用于在业务进程启动前拉取并持续刷新 `:15030/ca.crt`。

默认行为：

- 首次启动最多等待 `120s`，按 `2s` 间隔重试拉取 `http://127.0.0.1:15030/ca.crt`
- 成功后写入 `/tmp/lhotse-ca-bundle.pem`
- 自动导出 `SSL_CERT_FILE`、`REQUESTS_CA_BUNDLE`、`NODE_EXTRA_CA_CERTS`
- 后台每 `300s` 刷新一次 CA bundle

可选环境变量：

- `LHOTSE_CA_URL`
- `LHOTSE_CA_FILE`
- `LHOTSE_CA_REFRESH_INTERVAL`
- `LHOTSE_CA_INIT_TIMEOUT`

示例：

```bash
/workspace/scripts/lhotse-ca-init.sh python app.py
```

---

## 策略响应示例 (Domain Policy Format)

当配置了 `--domain-policy-url`，Lhotse 启动时会发起以下 GET 请求：
`GET <domain-policy-url>?app=<app-name>&ip=<instance-ip>`

配置中心应返回如下结构的 JSON：

```json
{
  "rules": [
    {
      "direction": "outbound",
      "mode": "default_allow",
      "allowList": [
        "api.example.com", 
        "*.trusted.company.com", 
        "203.0.113.10", 
        "10.0.0.0/8"
      ],
      "blockList": [
        "*.blocked.example.com", 
        "169.254.169.254",
        "198.51.100.0/24"
      ]
    },
    {
      "direction": "inbound",
      "mode": "default_deny",
      "allowList": ["admin.example.com", "192.0.2.10"],
      "blockList": []
    }
  ]
}
```
* **`default_allow`**: 默认放行所有流量，只有命中了 `blockList` 中的域名/IP才会被阻断。
* **`default_deny`**: 默认拒绝所有流量，只有命中了 `allowList` 的目标才会被放行 (严苛模式，推荐高价值 Agent 采用)。

---

## Kubernetes 部署示例

Lhotse 总是以 Init Container (写入 iptables) + Sidecar (代理进程) 的模式组合部署。

### 1. Init 容器拦截流量
Init 容器需要使用特权 (`NET_ADMIN` 和 `NET_RAW`) 来写入路由表，将进出站流量全部打给 15001 和 15006：

```yaml
initContainers:
  - name: lhotse-iptables
    image: ghcr.io/idefav/cloud-claw-lhotse-sidecar:latest
    command: ["/usr/local/bin/lhotse-init-iptables.sh"]
    securityContext:
      runAsUser: 0
      capabilities:
        add: ["NET_ADMIN", "NET_RAW"]
```

### 2. Lhotse Agent Sidecar
运行网络代理进程，使用专属用户 `UID 1337`。`iptables` 规则会通过匹配 UID 来放行 1337 发出的流量，防止代理自身的流量陷入死循环：

```yaml
containers:
  - name: lhotse-agent
    image: ghcr.io/idefav/cloud-claw-lhotse-sidecar:latest
    args:
      - proxy
      - --app-name=my-agent
      - --domain-policy-url=http://control-plane.local/api/v1/policy
      - --mitm-enabled=true
      - --vault-uri=http://control-plane.local
      - --agent-id=my-agent
    securityContext:
      runAsUser: 1337
      runAsGroup: 1337
      capabilities:
        add: ["NET_ADMIN", "NET_BIND_SERVICE", "NET_RAW"]
```

如果业务容器需要信任 Lhotse MITM CA，可在业务容器启动命令前包一层：

```yaml
containers:
  - name: app
    image: ghcr.io/example/app:latest
    command: ["/bin/sh", "-lc"]
    args:
      - /workspace/scripts/lhotse-ca-init.sh python app.py
```

---

## 自动构建与发布 (CI/CD)

本仓库已配置 GitHub Actions 自动发布流水线 (`.github/workflows/release.yml`)。

- **自动打标编译**：推送形如 `v*` (如 `v1.0.0`) 的 Tag 时，将自动构建针对 `linux/amd64` 和 `linux/arm64` 架构的 `lhotse-agent`、`lhotse-iptables` 二进制文件，并创建 GitHub Release。
- **自动构建镜像**：发布时，CI 将自动构建双架构的容器镜像并推送至 GitHub Container Registry (`ghcr.io/<owner>/<repo>:<tag>`)。
- **自动获取版本**：Go 编译时的 `-ldflags` 会将 Github 的 tag 号动态注入到代码中，执行 `lhotse-agent version` 即可查看当前部署的版本和修订信息。
