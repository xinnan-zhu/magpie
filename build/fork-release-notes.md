Personal SSH build of magpie (xinnan-zhu/magpie).

- Servers forcing `GatewayPorts yes` can use the local gateway through a private SSH Unix socket and a loopback-only relay. The fallback needs Python 3 on the remote Linux host; no SSH server configuration is changed.
- A confirmed tunnel validation refusal no longer appears as an unconfirmed write. Confirmed failed attempts release their ports/keys, and changing a pending tunnel's port requires reconnecting.
- Builds and release uploads now run in GitHub Actions after Test CI passes at the tagged commit. This build updates only from this repository.
- The Codex MCP command-server relay registers its receiving stream before sending a call, preserving immediate server questions and progress notifications.

macOS: the Apple silicon app is ad-hoc signed, not notarized. Windows apps are unsigned. As in the previous personal release, the assets include macOS arm64, Windows amd64/arm64 apps and Windows/Linux amd64/arm64 terminal binaries.

The Unix transport was checked on a real managed Linux host forcing `GatewayPorts yes`, using an authenticated fixture model list. No real model request or agent configuration change was used for that check.

<!-- lang:zh -->

magpie 个人 SSH 版本（xinnan-zhu/magpie）。

- 兼容平台强制 `GatewayPorts yes` 的服务器：通过私有 SSH Unix socket 转发，再提供仅监听回环地址的入口。远端 Linux 需要 Python 3，无需修改服务器 SSH 配置。
- 明确的隧道验证失败不再误报为写入结果未知；确认失败时释放端口和密钥，待确认的隧道更换端口时会要求重新连接。
- 构建和发布已迁到 GitHub Actions，只有标签对应提交的 Test CI 通过后才发布。应用继续从此仓库更新。
- 修复 Codex MCP 命令服务转发的竞态：先注册接收流再发送调用，避免漏掉服务端立即发出的提问和进度通知。

macOS Apple Silicon 应用为临时签名，未公证；Windows 应用未做代码签名。文件范围与上一个人版本相同：macOS arm64、Windows amd64/arm64 桌面版，以及 Windows/Linux amd64/arm64 终端版。

Unix 转发已在强制 `GatewayPorts yes` 的真实托管 Linux 主机上使用需要认证的测试模型列表验证；该检查没有发送真实模型请求或修改 Agent 配置。
