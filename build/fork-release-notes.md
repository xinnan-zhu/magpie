Personal SSH build of magpie (xinnan-zhu/magpie).

- Merges upstream main through `4cb7159f1` (2026-11-05), including the upstream changes through v0.1.3025.
- Retains SSH host selection/manual entry, remote inspection and one-click Linux installation, remote Web management, Claude Code/Codex model selection, authenticated local-gateway tunnels and selective configuration copies.
- Adapts SSH authentication to upstream's short-lived gateway keys. Each tunnel key stays restricted to its own SSH ingress and cannot issue credentials for other listeners; ordinary gateway keys keep upstream's issuance and expiry behavior.
- Keeps this fork's update feed and GitHub Actions release process. No local release packages or Docker images are built.

macOS: Apple silicon app, ad-hoc signed and not notarized. Windows apps are unsigned. Assets include macOS arm64, Windows amd64/arm64 apps and Windows/Linux amd64/arm64 terminal binaries.

This integration uses isolated automated fixtures. Real SSH hosts, installed agents and subscription inference were not rechecked for this merge; earlier transport-only checks do not establish that compatibility.

<!-- lang:zh -->

magpie 个人 SSH 版本（xinnan-zhu/magpie）。

- 合入截至 2026-11-05 的上游 main（`4cb7159f1`），包含上游 v0.1.3025 及此前的更新。
- 保留 SSH 配置选择和手动填写、远端探测与 Linux 一键安装、远端 Web 管理、Claude Code/Codex 模型选择、需要认证的本机网关隧道，以及选择性配置复制。
- 适配上游新增的短期网关密钥：隧道密钥仍只用于对应 SSH 入口，不能签发可在其他入口使用的密钥；普通网关密钥保留上游的签发和过期行为。
- 继续使用本 fork 的更新源及 GitHub Actions 发布流程，本机不构建发布包或 Docker 镜像。

macOS Apple Silicon 应用为临时签名，未公证；Windows 应用未做代码签名。提供 macOS arm64、Windows amd64/arm64 桌面版，以及 Windows/Linux amd64/arm64 终端版。

本次合并使用隔离的自动化测试环境，未重新连接真实 SSH 主机、修改已安装 Agent 的配置或测试订阅推理。此前仅针对传输的实机检查不代表这些场景已验证兼容。
