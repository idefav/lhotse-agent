

# idefav mesh 数据面

## 介绍
从零开始垒砌 mesh 数据面网络代理代码, 目前支持http协议代理和tcp代理

## 功能
1. 支持原地升级, 升级新版本时, http流量无损
2. 数据面分为管理端和代理, 管理端具有较高权限, 可支持API接口实时开启流量拦截和实时下线流量拦截
3. http协议代理支持, 支持KeepAlive
4. tcp长链接支持
5. 支持按出入站方向配置域名/IP/CIDR 访问策略，策略可在启动时动态拉取并周期刷新

## Domain Policy

`lhotse-agent proxy` 可通过 `--domain-policy-url` 开启动态访问策略。开启后启动时会请求配置地址，并追加 `app` 和 `ip` query 参数：

```text
GET <domain-policy-url>?app=<app-name>&ip=<instance-ip>
```

相关参数：

- `--app-name`: 应用名称，配置动态策略 URL 时必填。
- `--instance-ip`: 实例 IP；不配置时尝试自动读取本机非 loopback IP。
- `--domain-policy-cache-file`: 策略缓存文件，默认 `/tmp/lhotse-domain-policy-cache.json`。
- `--domain-policy-refresh-interval`: 周期刷新间隔，默认 `5m`，设为 `0` 禁用周期刷新。
- `--domain-policy-fetch-timeout`: 拉取超时，默认 `5s`。
- `--domain-policy-scope`: 启用方向，取值 `outbound`、`inbound` 或 `both`，默认 `outbound`。

响应 JSON 示例：

```json
{
  "rules": [
    {
      "direction": "outbound",
      "mode": "default_allow",
      "allowList": ["api.example.com", "*.trusted.example.com", "203.0.113.10", "10.0.0.0/8"],
      "blockList": ["*.blocked.example.com", "198.51.100.0/24"]
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

`mode=default_allow` 表示默认放行、命中 `blockList` 拒绝；`mode=default_deny` 表示默认拒绝、命中 `allowList` 放行。列表项支持精确域名、`*.example.com` 子域通配、精确 IP 和 CIDR。远程拉取失败时优先使用本地缓存；没有缓存时默认放行。

## 未来功能列表
1. 服务健康检查
2. 基础信息上报
3. 配置协议规范, 支持启动自动初始化配置, 配置本地存储, 持久化, 异步刷新配置
4. 增量配置更新
5. 关键监控指标

## GitHub Release
仓库新增了 GitHub Actions 自动发布流程，工作流文件位于 `.github/workflows/release.yml`。

- 推送形如 `v*` 的 tag 时，会自动执行测试，构建 `linux/amd64`、`linux/arm64` 的 `lhotse-agent`、`lhotse-iptables` 和 `lhotse-clean-iptables`，打包为 release 资产并附带 SHA256 校验文件。
- 同时会构建并推送多平台镜像到 GitHub Container Registry：`ghcr.io/<owner>/<repo>:<tag>`，支持 `linux/amd64` 和 `linux/arm64`；非预发布 tag 还会更新 `latest`。
- 也可以通过 `workflow_dispatch` 手动触发，但需要填写一个已经存在的 tag。
- 主二进制的版本号通过 `-ldflags` 注入到 `cmd/mgr.Version`，因此 release 产物和镜像里的 `lhotse-agent version` 会返回对应 tag。

示例：

```bash
git tag v0.0.3
git push origin v0.0.3
```
