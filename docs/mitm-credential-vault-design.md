# Lhotse Agent - MITM Credential Vault 功能实现方案

## 1. 背景与目标 (Objective)
在当前 AI Agent 生产环境中，如果将访问外部服务（如 GitHub、Jira）的 Token 或 API Key 通过环境变量（K8s Secret）直接注入到容器中，会存在极大的凭证窃取风险（如通过 Prompt Injection 诱导大模型读取环境变量）。
本方案旨在基于 `cloud-hermes-agent/docs/stateless-architecture.md` 及 `docs/openclaw-hermes-production-review.md` 中的技术设计，在 `lhotse-agent` 中实现 **MITM（中间人）透明代理与凭证动态注入 (Credential Vault)** 功能，彻底实现“凭证物理隔离”。

**核心目标：**
- 容器内（包括 MCP Server / LLM Runtime）**绝不持有真实凭证**。
- `lhotse-agent` 作为底层 Sidecar，通过 `iptables` 透明拦截容器发出的出站请求。
- 代理层解析流量（支持 HTTP/HTTPS），向统一的控制面（Vault Service）动态拉取凭证。
- 将拉取到的凭证注入到 HTTP Header（如 `Authorization: Bearer <token>`）中并加密转发给真实上游。

## 2. 架构设计 (Architecture)

### 2.1 流量劫持与协议探测 (Traffic Interception & Peek)
利用现有的 `lhotse-iptables` 将容器的出站 TCP 流量 REDIRECT 至 `lhotse-agent` 的 `:15001` 端口。
- 通过 `SO_ORIGINAL_DST` 获取原始目标 IP:Port。
- 窥探 (Peek) 连接的前 5 个字节：
  - 如果是 TLS (HTTPS) 流量，进入 **MITM TLS 劫持分支**。
  - 如果是明文 HTTP 流量，进入 **HTTP 直接注入分支**。

### 2.2 动态证书管理与双 CA 轮换 (CertStore & CA Rotation)
为了能解密 HTTPS 流量，`lhotse-agent` 必须作为中间人（MITM）与业务容器完成 TLS 握手。
- **CA 来源**：不再依赖挂载的 K8s Secret，而是启动时向配置中心 API (`/internal/ca/keypair`) 获取（后续可用 `--vault-uri` 传入配置中心地址）。
- **双 CA 零重启模型**：
  - 维护一个内存的 `CertStore`，存储 Primary (旧签发 CA) 和 Secondary (新轮换 CA)。
  - `lhotse-agent` 的 `:15030` 管理端口暴露 `/ca.crt` API，返回所有 Active CA 的 Bundle 供业务容器下载信任。
- **动态叶子证书签发**：通过 `tls.Config.GetCertificate` 回调，提取 ClientHello 中的 SNI（如 `api.github.com`），使用内存中的 CA 动态签发 ECDSA P-256 证书。

### 2.3 凭证匹配与注入 (Credential Resolver)
- 实现一个 `CredentialResolver` 组件，带有本地缓存（TTL 30s 左右）。
- 当获取到明文 HTTP 请求后，根据 `AgentID`、`TargetHost` 和 `UserID` 请求远端 Vault Service (`/internal/credentials/resolve`)。
- Vault Service 按优先级 (User Vault > Agent Vault > Platform Vault) 返回匹配的 Token。
- `lhotse-agent` 将凭证拼装成 `Authorization` 等 Header 注入请求，然后 `tls.Dial` 连接真正的上游并转发。

## 3. 详细实施步骤 (Implementation Steps)

### Step 1: 核心结构与配置扩展
1. 修改 `lhotse-agent/cmd/proxy/config/config.go` 和 `constants/constants.go`：
   - 新增参数 `--mitm-enabled` (默认 false)。
   - 新增参数 `--vault-uri` (Vault 配置中心地址)。
   - 新增参数 `--agent-id` (用于关联当前容器的 Agent 身份)。
   - 新增参数 `--ca-poll-interval` (CA 轮询刷新间隔)。

### Step 2: 实现 CA 与证书中心 (`pkg/tls/mitm`)
1. **`CertStore` 管理器**：
   - 支持加载和存储 CA 证书 (`ca.go`)。
   - 实现 `GetCertificate(hello *tls.ClientHelloInfo)` 方法：解析 SNI，判断缓存，无缓存则使用 CA 动态签发并存入 `sync.Map`。
   - 实现 `Reload()` 方法支持无损双 CA 热更新。
2. **`bufferedConn` 实现**：
   - 为了让 `tls.Server` 能读取到完整的 ClientHello，需要将之前 Peek 过的 5 个字节重新拼接回 `net.Conn` 的读取流中。

### Step 3: 实现凭证解析器 (`pkg/credential`)
1. **`Fetcher` 客户端**：封装 HTTP 客户端，调用 Vault API (`/internal/credentials/resolve`) 获取凭证映射。
2. **`Resolver` 管理器**：维护带有并发安全控制和超时淘汰 (TTL) 机制的内存缓存。当拦截到网络请求时提供同步获取凭证的接口。

### Step 4: 改造代理拦截核心 (`cmd/proxy`)
1. **`outbound.go` 改造**：
   - 在 TLS 判断 `else` 分支中，如果 `o.Cfg.MITMEnabled` 为 `true`，则不再进行透明 TCP passthrough，而是调用新增的 `mitmTLSProc(conn, reader, dst_host)` 方法。如果 MITM 处理失败，作为 Fallback 再退回到透明转发模式。
2. **`mitmTLSProc` 逻辑**：
   - 提取 SNI -> 动态签发证书 -> 执行 `tls.Server` 握手 -> 读取明文 HTTP 请求。
   - 调用 `CredentialResolver` -> 注入 Headers -> `tls.Dial` 真实上游 -> 完成双向 Copy。
3. **`http.go` 改造**：
   - 在已有的 `HttpProc()` 中，当 `data.Match(request)` 解析完成后，通过 `CredentialResolver` 动态注入匹配的 HTTP Header，然后再调用原有的 `proxyHTTPRequest`。

### Step 5: 业务容器 Entrypoint 辅助脚本 (`lhotse-ca-init.sh`)
由于代理进行了 TLS MITM 劫持，业务容器必须信任 Lhotse 的动态 CA。
- 编写 `lhotse-ca-init.sh`（后续可作为 configmap 或直接打入容器镜像）。
- 该脚本在业务进程启动前，循环向 Lhotse `127.0.0.1:15030/ca.crt` 抓取根证书。
- 注入通用环境变量 `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`。
- 以后台循环任务定期获取最新证书，随后 `exec` 真实业务命令。

## 4. 阶段交付与验证 (Validation)

1. **第一阶段 (基础通信)**: 实现 `CertStore` 与 `bufferedConn`，开启 `--mitm-enabled` 后，使用 `curl` 访问 `https://example.com`，确认能成功返回响应且使用的证书 Issuer 为 Lhotse 自建 CA。
2. **第二阶段 (凭证注入)**: Mock 一个本地的 Vault API 服务。访问受保护外部资源时（如 GitHub API），验证 Lhotse 能否正确拉取并注入 `Authorization` 头（通过远端 API 或抓包验证）。
3. **第三阶段 (CA 热更)**: 更新 Mock API 的 CA 证书，观察 Lhotse Agent 日志是否能无缝热更新 CA，且新建立的连接验证通过。