

# idefav mesh 数据面

## 介绍
从零开始垒砌 mesh 数据面网络代理代码, 目前支持http协议代理和tcp代理

## 功能
1. 支持原地升级, 升级新版本时, http流量无损
2. 数据面分为管理端和代理, 管理端具有较高权限, 可支持API接口实时开启流量拦截和实时下线流量拦截
3. http协议代理支持, 支持KeepAlive
4. tcp长链接支持

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
