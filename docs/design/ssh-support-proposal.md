# SSH 远程主机管理提案

状态：首版已有实现，本文其余内容保留为设计讨论，不代表全部落地。当前契约见 [SSH management](../subsystems/ssh-management.md)，验证与剩余限制见 [验证记录](../ssh-validation.md)。未连接真实 SSH 主机、未读取私钥正文、未使用真实订阅。

已落地：内置插件页面、系统 SSH、早期 stdio 控制入口、用户 daemon 与实际服务上下文探测、Linux 校验部署、远端 Web、原 agent 适配器、认证隧道与 Remote Magpie provider、选择性新增合并、可选的单次直连会话。未落地：统一全仓库写入 owner、通用跨文件事务、持续同步、自动迁移现有服务、任意服务发现。

## 1. 推荐结论

按用户补充的架构偏好：**在远端安装完整、无 GUI 的 Magpie，由本地桌面经 SSH 控制；默认经远端 Magpie 网关；也允许单次会话直接连接本机 SSH 隧道，两种方式共存。** 网关是核心，CLI/helper 是同一个产品的控制入口，不另造一个只有配置编辑能力的远端产品，也不另建一套与远端 Magpie 冲突的 providers/stash。

本地提供「远程主机」入口，选主机后沿用 Magpie 的 agent 列表、模型选择器和状态表达。日常连接、切模型保持现有点选即执行的交互；配置复制、批量覆盖和有冲突的恢复先预览再应用。提供三种明确策略：

1. **使用本机网关：** 远端 Magpie 把本机 Magpie 作为 `Remote Magpie` 上游，经 SSH -R 到达本机专属入口。远端 agent 仍连远端固定 loopback 地址，订阅与上游凭证留在本机。
2. **复制本机配置，远端独立运行：** 选择 providers、groups、模型选择等可移植配置，可显式包含 API 密钥；在远端预览合并后应用，远端自行向供应商请求，本机退出后仍可运行。
3. **保留并管理远端已有配置：** 不复制、不替换供应来源，只把现有远端实例接入本地控制台。

这三项是配置向导/默认供应来源，不是互斥的全局开关：远端可以同时保留本机 Remote Magpie 上游和自己的 providers/groups，按 agent 选模型。切换向导不清空既有供应商，备用线路只由用户明确配置的路由组表达。

首版复用 Magpie 已有 nogui CLI、daemon、serve/web、Remote Magpie provider 和 backup 数据结构。新增范围是 SSH 生命周期、目标绑定、选择性复制及必要的恢复。先验证现有接口能否覆盖操作，再为真实缺口补窄接口；下文 stdio、owner 和新增 listener 是实现候选，不是必须全部建设的前提。

cc-switch-ssh 的“选主机、读状态、切模型、本机反向网关”体验值得保留；其本地直接读写远端 agent 文件的实现不作为 Magpie 的主架构。

### 1.1 遵循 Magpie 本身的设计

以下约束来自仓库当前的产品说明和子系统边界；具体 SSH 落点是本提案的推导，不宣称仓库已有 SSH 规范。

| 现有原则与依据 | SSH 设计必须保持的行为 |
| --- | --- |
| [README](../../README.zh-CN.md)：所有 Agent 的模型一处选择，共用本地网关。 | 每台机器运行自己的完整 Magpie；远端工具连远端网关，本机供应只是一个 Remote Magpie provider。模型、路由组、账号选择保持原有概念。 |
| [Agent wiring](../subsystems/agent-wiring.md)：修改必要字段，记录 applied/stash，断开后恢复原值。 | 远端使用同一套适配器和模型拼写，不复制配置编辑逻辑；保留注释、用户字段、漂移判断与断开恢复。 |
| [CLI set](../../internal/cli/main.go) 与 [GUI API](../../internal/gui/api.go)：选择模型直接执行既有 agent 操作。 | 普通点选不增加强制 diff/确认步骤。后台可做校验和记录；批量导入、替换及冲突才展示计划。 |
| [GUI shell](../subsystems/gui-shell.md)：桌面与 Web 共用嵌入页面、JSON API、原生 JS 和现有菜单。 | 复用远端 Web 及现有组件，不新增前端框架、构建链或第二套管理界面；主机归属始终可见。 |
| [Plugin runtime](../subsystems/plugin-runtime.md)：非人人需要的功能按可关闭的内置插件组织。 | SSH 集成优先作为内置插件 `plugins/ssh`，页面遵循 kernel/slots/kit；本机操作系统能力留在受限 Go 服务，不能把任意 SSH 执行权交给网页脚本。 |
| [Sync/backup](../subsystems/sync-backup.md)：迁移可移植 setup，机器设置留在本机。 | 复制扩展既有分项导入语义，端口、路径、网络授权与登录态按目标机处理；不生拷 agent 目录。 |
| [Code standards](../code-standards.md)：改动只覆盖目标所需，默认值不覆盖用户设置，读取失败表示未知。 | SSH 不成为全仓库配置层重构的理由；不清空远端已有 providers，不暗改 fallback、同步或共享设置。 |

实现优先级为：复用现有操作 → 为目标和传输补适配 → 仅对已验证的缺口扩展共享能力。不能为了 SSH 同时重做 CLI、Web、配置存储和所有 agent 的写入框架。

## 2. 已核对的源码事实

### 2.1 cc-switch-ssh 实际做了什么

以下链接指向相邻的参考仓库，仅用于本设计核对，不是 Magpie 的依赖：

- `cc-switch-ssh/src-tauri/src/services/provider/remote.rs`：读取 `~/.ssh/config` 并处理 Include，列出具体 Host；也允许手动 host/user/port/password。实际连接调用系统 `ssh`。`configure_ssh_command` 清除额外转发、设置超时；无密码时 BatchMode，密码时 ASKPASS，密码不序列化入配置。Unix 使用 ControlMaster/ControlPersist，路径不适合时关闭复用，Windows 不使用它。
- 同文件的 `read_remote_snapshot` 一次 SSH 读多个固定路径，字节长度帧避免配置内容混淆。`build_remote_writes` 在本机使用各 agent 的格式投影生成配置，再执行远端 `sh -s`，每文件临时写后 mv。**这是逐文件替换，不是多文件事务**；本地 `InFlightGuard` 也不能阻止另一个桌面实例或远端编辑器同时修改。读脚本把不存在及读取失败都编码为缺失，Magpie 不应沿用这一语义。
- `cc-switch-ssh/src/components/remote/RemoteProviderPage.tsx`：当前远端支持 Claude、Codex、Gemini、Grok Build；有 SSH Host/手动连接、远端配置预览、写入本地供应商、导入远端供应商、本地网关和远端进程操作。请求含 app、target、connectionVersion，切换应用会清除旧连接。订阅类本地登录不作为普通配置推送。
- `cc-switch-ssh/src-tauri/src/services/provider/remote_gateway.rs`：按 host 记录 token 和远端端口，按 host+app 记录路由；`ssh -R 127.0.0.1:远端端口:127.0.0.1:本地端口`，独立子进程、keepalive、2–60 秒退避。网关模式拒绝密码目标；进程存活三秒后标记 Connected，尚不是模型 API 端到端健康证明。
- `cc-switch-ssh/src-tauri/src/proxy/remote_gateway.rs`：专用 listener 校验 host token，按远端 app 路由，并给用量/会话标记远端来源。其 `cc-switch-ssh/src-tauri/src/services/provider/remote_cli.rs` 还能探测远端 CLI 安装来源并更新，`cc-switch-ssh/src-tauri/src/services/provider/remote_usage.rs` 读取远端用量；这些不是核心切换功能的前置条件。

借鉴其交互和控制流分层；不照搬密码处理、读取失败视作缺失、定时标绿、以别名作为完整身份等边界。

### 2.2 Magpie 可以复用什么，不能假定什么

| 当前事实 | 对设计的影响 |
| --- | --- |
| [agent-wiring](../subsystems/agent-wiring.md)、[agent.go](../../internal/agent/agent.go)、[applied.go](../../internal/agent/applied.go)、[stash.go](../../internal/agent/stash.go) 已定义发现、连接、模型选择、漂移和恢复。 | 复用 agent 语义和格式编辑器；不能只写 base URL 而绕过 stash/applied。 |
| [wsl.go](../../internal/agent/wsl.go) 的 `place` 提供 home、id、路径拼写和网关地址，读写仍使用本地 `os.Stat`、`os.ReadFile` 等。 | SSH 不能只增加 `place`，也不采用把远端挂载成本地目录的假设。 |
| [appdir.go](../../internal/appdir/appdir.go) 的配置根、portable 判定和多处 provider/catalog 缓存是进程级状态；stash/applied 位于 provider 配置旁。 | 每个远端用户/配置上下文独立进程，禁止在本机 GUI 进程切换 HOME 来“访问远端”。 |
| [preview.go](../../internal/agent/preview.go) 的 DisconnectPreview 在临时副本执行实际断开，部分 agent、portable、外部路径不支持。`NativeConnection` 的 [DisconnectPlan](../../internal/agent/native.go) 有 revision 和文件前置检查。 | 有预览范式和局部计划结构，但**没有通用的连接/切模型可提交计划**。批量复制需要的计划是新增能力，不因此要求普通切模型全部改为两步操作。 |
| [edit/file.go](../../internal/edit/file.go) 保留格式相关约定、权限和符号链接目标；普通文件临时写后 rename，硬链接可能原地写。stash/applied 并非统一事务持久化。 | 不把 WriteAtomic 宣称为跨文件事务。新增的可恢复批量提交首版拒绝硬链接，相关状态写入纳入同次操作并传播错误；不暗改所有现有本地编辑的文件政策。 |
| [filelock.go](../../internal/filelock/filelock.go) 的 `Lock` 可能返回 held=false，`Mutex` 会继续。 | 远端提交必须显式检查 held；锁不可用时拒写，不能直接套现有 Mutex 当严格锁。 |
| [cli/main.go](../../internal/cli/main.go) 普通命令分发前执行 startUp，包含迁移、插件初始化、provider 读取；gateway serving 会挂上同步等循环。已有 DryRunArg、claude-mcp-helper 提前分发。 | 新 helper 必须走独立早期入口，不能 SSH 执行普通 `magpie <agent> ...` 后声称只改请求的配置。 |
| [gui-shell](../subsystems/gui-shell.md)、[api.go](../../internal/gui/api.go)、[web.go](../../internal/gui/web.go) 是本机 GUI/网页与 JSON API；网页认证和网关 key 是不同边界。 | 桌面经本机 API 控 SSH，不把远端 `/api/*` 暴露为公网管理接口。 |
| [Remote Magpie](../subsystems/providers-accounts.md#remote-magpie)、[provider/remote_magpie.go](../../internal/provider/remote_magpie.go) 是把另一网关作为供应商，列模型、转发请求和汇总用量。 | 它不是远端文件管理；SSH 主机不自动成为一个 provider。 |
| [lan.go](../../internal/gateway/lan.go) 对普通 loopback 请求有本机信任；[oneport.go](../../internal/gateway/oneport.go) 已使 Web 端口的客户端路由始终按远端请求鉴权；[remote_magpie.go](../../internal/gateway/remote_magpie.go) 的 X-Magpie-Via 只是标签。 | 优先复用既有远端请求标记、ClientPath、Front 和 access key 规则；不能直接隧道转普通网关借用本机信任。已有 Web 端口路径仍要求共享授权，不能偷偷开启 LAN。 |
| [sync-backup](../subsystems/sync-backup.md) 只同步可移植 setup，机器相关设置留在本机；[providers-accounts](../subsystems/providers-accounts.md) 含本地/插件账号和远端网关语义。 | SSH 连接资料、运行状态和恢复日志不混入 setup 自动同步，不复制订阅令牌来“同步到远端”。 |

## 3. 方案取舍与进程边界

| 方式 | 结论 |
| --- | --- |
| 本地直接 SSH 编辑所有 agent 文件 | 不作为主方案。绕过远端完整 Magpie 的状态/网关，复制格式和恢复语义，远端环境难以正确解释。只用固定 bootstrap 脚本完成探测和安装。 |
| 完整远端 Magpie + 现有管理能力的 SSH 适配 | **推荐。** 优先复用远端 Web/CLI 操作和实际 setup；若 bootstrap、身份握手或机器输出确有缺口，再补同 binary 的窄 stdio 入口。不能由此复制整套业务 API。 |
| SSH -L 转发已有远端 Web 管理页 | 作为完整功能入口复用：用户可“打开远端管理界面”。不把完整网页 API 无差别透明转成桌面的 current-host API；嵌入工作区所需的窄控制协议仍新增。 |

```text
本机桌面「远程主机」 → 本机 /api/ssh/* → OpenSSH
                                         │ 已有接口 / 必要的窄适配
                                         ▼
                              远端完整 magpie 的控制入口
                                         │ 远端本地 IPC/受限管理接口
                                         ▼
                              远端 Magpie 服务与真实 setup
                                         │
                              远端 agent 配置 / 网关 / 插件

模式 A：远端 agent → 远端 loopback gateway → Remote Magpie provider
        → 远端 SSH -R 端口 → 本机 SSH ingress → 本机 gateway → 上游
模式 B/C：远端 agent → 同一个远端 loopback gateway → 远端上游

完整管理页：本机浏览器 → SSH -L → 远端 loopback web（独立认证）
```

远端服务可以在无人登录、无桌面环境运行。如果新增 stdio helper，它只是同 binary 的 `magpie ssh-helper` 早期入口：连接与只读不启动 ordinary CLI 初始化；实际“初始化/启动完整 Magpie”是明确的管理动作，会显示其将使用的版本、数据目录及服务方式。现有 Web/API 能满足的业务操作直接复用，不能以 helper 名义维护另一套实现。

**写入协调只针对本次操作实际触及的状态；统一 owner 是候选方案，并非当前 CLI/Web 天然具备或首版必须全量引入的框架。** 服务运行时优先复用同 uid、同 setup 的现有服务操作，不在旁边启动第二个带独立缓存的写入者。批量导入若需要协调服务缓存、同步和文件提交，再在共同业务层补最小协调点。未提供安全协调能力的旧服务可继续通过原有 Web 管理，但新增批量写入只读/要求升级。

复制/批量修改若与现有 CLI/Web/同步触及相同状态，需要共享该范围的提交锁及版本检查，或明确阻断冲突操作。普通切模型沿用共同业务函数，不为了暴露 SSH 再实现一遍。未知旧版 CLI 或外部编辑器无法靠我们的锁完全约束，仍通过前置检查、回读和冲突恢复限定保证。

## 4. 身份、状态和凭证隔离

### 4.1 主机不是一段 Host 字符串

本机保存 `hostId`（随机不可变 ID）、显示名、alias 或显式 host/user/port、配置来源路径、连接偏好。不得把显示名当数据库主键。握手绑定并展示：SSH 验证过的服务器身份、远端 OS/arch、实际 uid/user、真实 HOME、解析后的 agent 配置根及 Magpie 数据根。

一个连接上下文 `targetId` 对应 `(hostId, 已验证服务器身份, uid, canonical config roots)`；别名改名可以保留 hostId，别名改向另一服务器、同主机换用户或配置根则重新建立上下文。不能静默沿用旧快照、隧道凭证或恢复记录。相同远端文件集的多个别名必须在远端汇合到同一个锁及所有权记录。

会话使用随机 `connectionId` 和递增 generation；GUI 缓存键为 targetId+connectionId+agentId。响应绑定创建请求时的上下文，晚到响应丢弃。远端 agent 的 `codex` ID 可以保持原状，跨主机列表和用量键必须包含 targetId，不能污染本机 `codex` 的模型选择。

### 4.2 完整远端 setup 是唯一状态源

使用实际远端用户的 `appdir.Config()`，或用户明确选择的现有 portable/setup 根。providers、groups、settings、applied、stash、plugins 和 usage 都属于这个远端实例；helper 不创建平行的配置域。新增 SSH 控制元数据、操作日志和备份放在该实例的私有子目录，保持 0700/0600 权限并排除自动同步。

远端服务必须报告它实际使用的 binary、版本、uid、config/cache 根、agent 环境根和端口。本地发现命令的环境与服务环境不同时，以服务的实际上下文为准并展示差异。没有服务时明确选择上下文；一个 SSH 用户有多个 setup 时分别登记，不猜“默认就是正确的”。

确需显式选择 Magpie 数据根时，增加启动时固定、只影响 Magpie state/cache 的参数；不临时改 `HOME` 或 `XDG_CONFIG_HOME`，以免连带改变 agent 的路径。`CODEX_HOME`、`CLAUDE_CONFIG_DIR`、`OPENCODE_CONFIG_DIR` 等按远端真实环境/明确配置解析。本机路径不继承到远端；不执行任意 shell rc 来推测 IDE 环境。

本机仅保存主机连接信息和脱敏状态缓存。模式 A 中本机模型列表通过远端 `remote-magpie` provider 获取，远端已有 provider/catalog 仍是模型真相，不把本机 providers.json 挂进远端进程。模式 B 通过显式可移植配置导入进入同一远端 setup。

### 4.3 凭证各归其主

| 数据 | 存放/执行位置 |
| --- | --- |
| SSH 私钥、agent、硬件密钥 | 系统 SSH/ssh-agent；Magpie 只保存引用，不读私钥正文、不复制到远端，不启用 ForwardAgent。 |
| SSH 密码、私钥口令、键盘交互答案 | 经本机受限 ASKPASS/交互通道一次性送 OpenSSH；不进入 URL、命令参数、持久化或诊断日志。 |
| 本机供应商 key、订阅登录、插件账号 | 模式 A 留在本机；模式 B 仅显式选择的可迁移 API 密钥复制，订阅/插件登录不自动复制。 |
| 远端已有账号 | 留在远端；只按用户请求读取必要配置，RPC 默认只返回脱敏状态，不返回原始 auth.json。 |
| 模式 A 的访问令牌 | 本机专用入口签发，按远端实例限定；仅远端 Remote Magpie provider 持有。远端 agent 使用远端网关自己的鉴权。 |
| 原配置备份中的 secret | 远端 0700 目录、0600 文件；不自动下载进本机普通日志或同步包。 |

首版支持显式选择 API 密钥的配置复制，但不复制订阅登录。现有 backup 本就不包含 subscription sign-ins；目标机未登录的订阅列为“需要远端登录”，不能导入后谎报可用。

## 5. SSH 连接与认证

使用系统 OpenSSH，兼容其 Host/Include/Match、IdentityFile、ssh-agent、证书、ProxyJump 和 ProxyCommand；不再写一套不完整 SSH 协议或配置求值器。

- 主机列表仅解析显式 Host 别名和 Include 作为候选；通配符不当成具体主机。最终配置由 OpenSSH 解释。`ssh -G` 只在用户选定目标后执行；它可能涉及 Match exec，不能声称这是完全无执行的安全解析。
- CLI 参数用独立 argv；别名、主机、端口和用户名严格验证，`--` 结束参数。远端 bootstrap 是固定脚本、参数有严格编码，不把模型名/用户字符串拼入 shell 命令。
- 保持 OpenSSH host key 验证。已知 key 变化立即阻断，不能自动删除 known_hosts。首次未知 key 展示由 SSH 验证流程提供的指纹及地址，确认后再信任；不以 ssh-keyscan 的结果自行认定身份，也不默认 StrictHostKeyChecking=no/accept-new。
- 首版正常路径是已建立信任的 ssh config + key/agent/ProxyJump。需要交互时状态为 `needs_auth`，可在对应系统终端验证 host key、解锁或加入 ssh-agent。一次独立终端密码登录不会让后续独立 SSH 自动免密；纯密码或每次 keyboard-interactive 的目标在未实现连接复用/ASKPASS 通道的平台上明确不支持后台模式，不假装认证已可继承。GUI 原生 ASKPASS 作为第二批，答案只绑定当前请求；跳板机的认证可能先于目标机，UI 必须显示实际 prompt 来源和顺序。
- 若采用 helper RPC，用一条 `ssh -T` 长连接承载；复用远端 Web 时由 SSH -L 承载已有 HTTP API。数据隧道是独立 `ssh -N -T -R` 连接。Unix 可以复用专属 ControlMaster，但功能不能依赖 Windows 没有的 ControlMaster。socket 放短路径的私有 runtime 目录，以连接身份区分，关闭时只清理自己创建的连接。
- 管理连接清除用户配置中的额外 forwards、关闭 X11/agent forwarding。数据连接只设置所需转发，不继承额外转发。不能把 `ClearAllForwardings=yes` 原样放到数据连接而取消自己要求的 -R，需分别构建和验证最终参数。
- `ExitOnForwardFailure=yes`、连接超时和 ServerAlive 不足以证明数据可用。远端必须通过刚建立的端口完成认证探测，核对实际 gateway instance、目标归属和令牌权限后才标绿。优先使用现有模型列表/身份接口，仅补缺少的校验信息。
- 用户断开、取消安装或超时需结束所属子进程和管道；后台重连带抖动退避，只重建连接/隧道，不能重新提交配置。host key 变化、需要交互认证等不可重试状态停止自动尝试。

## 6. 完整远端 Magpie 的部署、服务与升级

复用 [Makefile](../../Makefile) 已有 `CGO_ENABLED=0 -tags nogui` CLI 构建路线，远端初始支持 Linux amd64/arm64，macOS 后续。本机各平台调用系统 OpenSSH，Windows 不依赖 ControlMaster。远端无需 GUI、sudo 或常驻桌面。

首次连接只做有限的 OS/arch、用户、已有 binary/service/setup 探测。已有实例优先复用，不静默覆盖 PATH 中的另一版本；缺失时“安装 Magpie”展示发行版本、目录、下载大小和初始化范围。本机取得官方对应 artifact，验证可信发布清单/校验和；若现有发布没有可验证清单，实施时补建，不能自行宣称现有签名可用。通过 SSH stdin/受控 SFTP 上传、远端核对、chmod 后原子安装到用户版本目录，再配置受控的稳定启动路径。

不执行 curl|sh，不自动安装所有 agent/Node/Bun。完整 Magpie 能力中某插件或订阅需要额外 runtime 时，在远端诊断页明确缺失依赖，按该功能现有安装流程处理；“完整 binary”不代表所有外部依赖已经存在。支持本机预缓存 artifact 供远端无外网时安装。

复用 [daemon.go](../../internal/daemon/daemon.go) 的 `Install(Options{Web,Key})`、`Web()` 和 `magpie serve --web=…`。本地安装流程默认让远端 gateway/web 只监听 loopback，web key 与 gateway key 分开且在目标机新建；不照搬本机 LAN/端口。Linux user service、持久化/linger 权限和无 systemd 主机分别检测；服务安装成功与开机自动启动是两项状态。不能悄悄 sudo 或假装 nohup 等于守护服务。

若需要新增控制入口，候选为 `magpie ssh-helper --stdio --protocol=1`，在普通 startUp、catalog hooks 前分发，并审计 Main 的环境/退出保存、包 init。不论采用何种传输，只读探测都不能额外触发账户发现、登录刷新、插件迁移、自动 sync 或 telemetry；已运行的完整服务自身原有后台行为不因 SSH 检查而增加或重启。明确的“初始化并启动”才走完整实例流程及原有迁移，执行前展示影响。

握手校验 control protocol、setup schema、writer capabilities 和版本。首版写入要求匹配版本/能力；旧版实例只读或打开其已有 Web 页面，升级后再提供新控制动作。升级先安装并列版本，检查未完成操作及活跃请求，按现有 gateway handover 能力决定安全重启；不假设远端 headless service 天然拥有 GUI handover 的全部保证。保留旧 binary 与升级前 setup 备份；涉及不可逆 schema 迁移时不能只换回旧 binary 就称回滚成功。

已有 Web 时，“打开远端管理界面”创建本机 loopback 的 SSH -L，仅转发远端 loopback Web 端口，继续使用远端 Web 自己的认证；不把 key 写到普通日志/持久浏览历史。首版可在临时浏览会话完成登录。完整管理页用于现有 Providers、Plugins、Library 等高级功能，本地不复制整套 UI。

## 7. 模型请求的两种拓扑

### 7.1 模式 A：远端网关代理本机网关

默认由远端 Magpie 把 agent 连接到远端稳定 loopback gateway，模型选择、stash/applied 都在远端。开启模式 A 时，在远端新增受管理的 `remote-magpie` provider，其地址为 SSH -R 的远端端口。同时允许使用 `magpie ssh-direct` 为单次 Claude Code/Codex 会话直接连接本机隧道，通过子进程环境和参数指定模型，不修改持久 agent 配置。两种连接方式可共存。

1. 本机签发远端实例专属命名 key，限制可用模型/账户、预算和并发；通过 SSH 只传给远端该 provider，不复制本机供应商凭证。首版身份粒度为远端实例，不宣称仅靠 forwarded User-Agent 能证明具体远端 agent 身份。
2. -R 指向始终按远端请求鉴权的入口，不直接指向普通 gateway port。先验证现有 Web 单端口的 ClientPath/FromPagePort/Front 路径能否满足：它已解决 TCP 转发被误认成本机请求的问题，但需要共享授权。用户已有合适共享设置时可复用；否则仅为 SSH 增加最小 loopback 适配，复用同一鉴权链，不自动开启或扩大 LAN 监听。独立 listener 只有在验证确有需要时增加。入口验证专属 key，拒绝空 key、magpie 默认 token、撤销 key及非该连接授权的 key。
3. 仅允许 Remote Magpie 需要的模型列表、模型请求、必要额度读取和经过审核的公共 MCP 路径。管理 `/api/*`、内部 `/_magpie/*`、debug/heap/callback 一律不暴露；不能直接无差别允许全部 `/v1/*`。路径与方法白名单由现有 Remote Magpie 客户端实际调用确定。
4. 认证产生服务端 target/instance context；客户端 X-Magpie-Via 只能作为展示信息，不能授予权限。SSH 适配复用已有远端请求鉴权规则，只对隧道实际缺少的边界作扩展，不自动开启 LAN sharing，也不把所有 loopback 当已认证。
5. 远端 helper/服务通过 -R 发带 key 的 probe，核对本机 gateway instance/target，再由远端 `remote-magpie` provider 获取模型、验证选择。成功后远端使用自己的 agent 适配器生成连接/切模型计划。
6. 固定保存远端转发端口，端口冲突时拒绝并给出改端口计划，只改受管理 provider。remote gateway 本身地址保持不变。流式请求断线不自动重放；恢复隧道不重复修改配置。

远端 gateway 在模式 A 仍参与接入、选择、记账和请求转发；复用 Remote Magpie 的 native_endpoints、模型标识、价格/额度和同 API 传递，避免两边重复转换。保留其正常的带 provider 前缀映射，不把字符串盲目再加一层前缀。远端记录自身使用、本机记录上游成本；跨机器展示必须标明来源，不能简单相加当成两次真实供应商消耗。

模式 A 默认仅让所选 agents/groups 使用这个 managed provider，保留原有供应来源。切到模式 B/C 前预览模型引用迁移；不要删除仍被引用的 provider。检查配置是否形成“本机又把这个远端当上游”的环，直接环拒绝；多跳链需要受保护的 hop 限制/instance 链才能宣称完整防环，属于新增路由契约。

远端 -R 参数绑定 127.0.0.1，但 sshd GatewayPorts 可能改变实际监听。探测实际地址，首版无法验证或为 wildcard 时拒绝启用并说明原因。loopback 上仍需 token，同机其他用户可能访问端口。断开管理连接可以不影响 -R；本机睡眠/退出会使这个上游不可用，是否回退到远端原供应商只遵循用户已有明确 fallback，不因 SSH 自动新增收费回退。

会话复用、routing affinity、订阅 bridge 和 MCP session 必须审计并纳入认证 target/instance 边界；同 session ID 不能跨主机复用。普通 SSH/API 转发不会把远端目录当成本机目录。仅本机 Claude 订阅 bridge 原来会把请求中的 cwd 提示用于启动本机进程，因此可信 SSH 请求不采用该提示；远端工具仍在远端 agent 自己的工作目录执行。

### 7.2 模式 B/C：远端独立服务与配置复制

模式 C 仅管理远端原有 setup。模式 B 将本机选定配置复制到同一个远端 setup 后独立服务：远端请求上游、账号刷新、插件运行、MCP 和计费都发生在远端，本机断开后继续运行。可以只复制选中的 API providers，订阅仍在远端重新登录；本机可另将远端作为 Remote Magpie provider 使用，但不是管理功能的隐含副作用。

**复制是一项有选择、有预览的导入，不是生拷配置目录，也不是直接 Restore 整份 backup。** 复用 [backup.go](../../internal/backup/backup.go) 的 Bundle/Parts、provider/group/profile/agent-field/library schema 和分项 keys policy；新增 `TransferSelection` 与目标导入策略，不能把当前 Restore 的所有行为当成已经适用于跨机器复制。

| 层次 | 默认复制语义 |
| --- | --- |
| providers、groups、model picks、profiles | 可选的可移植配置；先导入依赖，再重写引用。相同 ID 内容冲突展示保留/重命名/替换，不按名字猜同一账号。 |
| API keys、自定义鉴权 headers | 单独显式选项，默认不带；预览显示“将复制 N 项凭证”及目标用户，值脱敏。未带 key 的 provider 保持禁用/待填写，不能误用同 ID 另一服务的旧 key。 |
| agent 配置意图 | 传 agent kind/profile、field/model 等语义；远端按实际 OS、版本、路径、网关和现有文件生成计划。绝不生拷 ~/.codex、~/.claude 或其 auth.json。 |
| 通用设置 | allowlist + Settings.KeepOwn 保留目标机端口/LAN/proxy等；未知新字段默认不复制。 |
| 订阅/插件登录、系统 keychain | 不复制。导入可迁移的订阅选择设置时标记需要远端登录，不携带 session token/refresh token。 |
| 插件清单、Library/MCP/skills | 后续选项。清单不等于二进制/账号可迁移；固定版本和兼容性先校验。stdio MCP 的本机路径、环境和依赖需用户映射，不把本地绝对路径写进远端。 |
| gateway caller keys、web key、SSH token/信任、设备身份、sync配置、运行日志 | 默认排除，不因勾选 Settings 或 API keys 而携带。目标新建自己的身份和网络授权。 |

`backup.Collect(keys=true)` 会包含 GatewayKeys，某些采集路径可能做 legacy key migration；因此传输不能直接在只读预览时无条件 Collect/Restore 整包。先构造只含选中字段、明确 keys policy 的无副作用投影，复用已验证的 schema/清洗逻辑，并测试未选中部分完全不变。

默认 **merge**：只增加/更新明确选择的对象，未选 provider、group、agent 和远端 settings 不变。**replace** 仅针对用户明确选择的集合/分区，预览列出删除项及所有被引用处；不得把“替换这些 providers”解释为清空整个 setup。依赖缺失或模型在远端不可用时阻止对应 agent 的应用，允许只导入禁用 provider；不能生成悬空 group 引用。

本机代理、localhost URL、证书/文件路径、MCP command、workspace path、插件工作目录视作待映射字段。localhost 在复制后指远端，不能默默认为还是本机；模式 A 的 managed tunnel provider 也默认不复制回其他主机。PATH、HOME 和 appdir 从目标机解析。

远端生成整份目标差异、依赖映射和凭证复制摘要；提交前备份所选对象及受影响 agent 文件。失败恢复限该操作的范围，后来的编辑保留；已安装的插件、已发生的供应商登录等外部副作用不可能靠还原 JSON 自动撤销，所以首版配置复制不夹带这些动作。复制是一次性操作，不自动开启双向 sync；如果以后开启已有 WebDAV/S3/GitHub 同步，要另行选择拓扑，避免与显式复制争抢状态。

### 7.3 Claude 订阅与 MCP 在哪执行

当前 [Claude subscription bridge](../subsystems/claude-subscription-bridge.md)、[claudebridge/mcp.go](../../internal/claudebridge/mcp.go) 表明，订阅的 Claude Code 子进程及 `claude-mcp-helper` 在**提供该订阅的 gateway 所在机**执行。模型工具调用经 gateway 转成客户端协议，由客户端 agent 在自己的工作目录执行。

- 模式 A：本机登录、本机 Claude Code bridge、本机回调；远端 gateway 传递工具调用，远端 agent 执行 Bash/文件工具。桥接内部 `/_magpie/claude-mcp/...` 不需要经 SSH 对外开放。
- 模式 B/C：远端所选订阅的登录、bridge 和内部 MCP callback 都在远端 gateway；本机桌面只负责管理。
- agent 自己配置的 stdio MCP 运行在该 agent 所在的远端，所需命令/依赖也必须存在远端；不能把本机绝对路径原样写过去。Magpie 插件提供的工具则在服务它的 gateway/plugin host 上运行；涉及文件系统的工具不能自动宣称看到远端工作区。
- 后续远端登录若需要浏览器，单独建短时、单用途 callback forward，核对 state 和精确回调地址，结束即关闭；它不复用管理 API key，也不借机导出远端登录凭证。

## 8. 只读预览、提交、断线恢复

本节的显式计划、备份日志和分阶段提交针对配置复制、批量覆盖及恢复。普通 agent 操作复用 Magpie 现有路径，只补远端目标校验、重复请求处理及必要的共享协调，不以 SSH 为由将全仓库 I/O 改成事务框架。

### 8.1 计划必须来自远端真实适配器

远端实例新增文件型 agent 的 `PlanConnection`/`PlanPick`/`PlanDisconnect`，配置复制新增 `PlanImport`；由同一远端实例的实际 setup 生成。复用同一个格式转换和 stash 规则，输出受控文件集的 before/after、存在性、目标路径和 revision；不是前端根据模型名拼一个看起来合理的 diff。为首版两个 agent 抽必要的读/写边界即可，不先建设通用虚拟文件系统。

计划可用受限 worker 在临时副本执行现有适配逻辑，但必须满足：显式映射所有读写路径；不连接真实 runtime、不执行登录/重启/自动 Sync；不通过 symlink 从 staging 写回真实目录；不复制整个 HOME；清理含 secret 的临时文件。无法证明无副作用的 agent 只读。现有 DisconnectPreview 是参考，不能未经审计直接套用到 connect/pick。

实时 agent 文件、stash、applied、provider 配置在 inspect/plan 时不变。响应只含脱敏 diff；原始内容留在远端内存或私有临时区。`planId` 有过期时间，绑定 connection、实际远端用户、配置根、agent/选中导入范围、catalog digest 和远端实例 ID；客户端不能自行上传任意文件路径/after 内容来请求执行。批量导入/覆盖必须先展示计划；普通切模型可在一次请求中完成目标校验与执行，保持现有交互，不要求用户手动执行 plan/apply 两步。其内部校验与实际写入仍来自同一业务实现。

读取失败、权限错误、无效 JSON/TOML 是 unknown/error，不是不存在。parse 失败可以只读显示原因与脱敏片段，不能自动覆盖空配置。首版只管理用户级配置；项目级、managed settings 或环境覆盖须检测并提示“该操作可能不改变当前运行实例”，不谎报已生效。

### 8.2 可恢复提交，不宣传跨文件原子性

拟议提交顺序：

1. 按规范化的实际文件集/用户取得严格跨进程锁，`filelock.Lock` held=false 即拒写。锁路径独立于本机 hostId，覆盖别名及两个控制端。
2. 重新读取原始字节、存在性、符号链接目标、权限和 ownership；与 plan 前置条件比较。不符返回 `stale_plan` 并重新预览，不能拿旧 plan 强行覆盖。
3. 先写 0600 原始备份和 transaction manifest，包含操作 ID、所有目标、before/after digest、阶段和恢复依据；fsync 后再改 live 文件。首版拒绝硬链接、特殊文件、不受支持的跨根符号链接和锁不可靠的文件系统；合法符号链接保留其本体并核对目标未变。
4. 同目录 staging、fsync、逐文件 rename；包括远端实例自己的 stash/applied 及所选 provider/group/setup 状态。记录每一步、核对落盘结果；写入错误必须向上传播，不能沿用吞掉 stash/applied 错误的路径。
5. 全部完成后标记 committed，通知远端实例刷新 provider/catalog 缓存，并按计划触发必要的 agent 同步，返回新的 revision 与实际状态。不能提交期间由自动 Sync 再改计划外的 agent；若后续同步失败，分别报告配置已提交和生效失败。多文件提交过程中第三方仍可能观察到中间态；必要时提示重启 agent，首版不强杀进程来模拟事务。

这是一份**有日志、可恢复的批量变更**。严格锁只约束协作的 Magpie 进程；外部 agent/编辑器不协作，hash 复核到 rename 之间仍存在竞争窗口，不能承诺强一致事务。相关 Magpie GUI/CLI/自动 Sync 若要共同管理这些文件，必须加入同一锁和 ownership 契约；旧版远端 Magpie 不满足时禁止双重管理。

失败时仅对仍等于本操作已写 after 内容的文件做自动回滚。发现后来编辑则保留备份并返回 `recovery_required`，让用户预览恢复；不得用旧 before 覆盖后来的用户修改。磁盘耗尽/权限变化造成回滚也失败时列出每文件结果，下一次连接先报告未完成事务。

### 8.3 断线后的“结果未知”

每个 apply 有客户端生成的 `operationId`，远端持久保存它与 plan 摘要。重复 ID+相同摘要返回已有结果；同 ID+不同内容拒绝。收到执行请求后若 SSH 断开，UI 显示“结果待确认”，重连后查询 operation 状态，不能直接重复 connect/pick。

远端执行者在提交前可以取消；一旦进入持久化提交段，尽力完成或回滚并记录结果，进程被杀/主机断电则由下次显式恢复操作处理。读接口只报告残留，不能一读状态就修文件。撤销是基于已提交记录生成的新计划；只恢复仍由 Magpie 控制的字段，展示用户后来改动的保留项。

隧道断开与配置恢复是两件事：“断开 SSH”停止管理连接；“停用本机网关”须展示受影响 agent 和恢复计划；“删除主机记录”不应悄悄遗弃恢复依据，离线时只可撤销本机 key/停隧道并标记远端待清理。应用退出时可提示活跃远端依赖，不能未经说明修改远端配置。

## 9. UI、API 和 CLI 草案

### 9.1 UI

优先由可关闭的 SSH 内置插件贡献「远程主机」入口，主机选择只作用于其明确标注的范围。复用现有 agent 行、模型选择器、通知和远端 Web，不重建一套 Providers、Usage、Library、Settings 页面。具体入口落在 Agents 的扩展或独立插件页，实施时按现有 slot 能力选择，不先锁死新导航结构；不能用一个全局 selectedHost 悄悄切换所有 API 的目的地。

首次流程：添加主机 → 选 SSH 别名/手动地址 → 完成信任与认证 → 探测/按需安装完整 Magpie → 明确初始化/启动远端服务 → 展示“主机 · 用户 · 配置目录” → 选“使用本机网关”“复制配置”或保留现状 → 展示新增上游或批量导入的范围 → 应用。日常流程：选主机 → 在既有样式的 agent 行点选模型 → 返回实际状态和必要的重启提示；只有遇到冲突、批量替换或恢复时才进入差异确认。

列表同时显示管理连接状态、网关状态和配置状态，不能一个绿色点代替三者。状态示例：未连接、连接中、需要认证、Magpie 待安装、不兼容、就绪、隧道不可达、应用中、结果待确认、需要恢复。空状态区分“无已知主机”“未找到支持的 agent”“权限不足”“当前 agent 配置未知”。

前端按内置插件惯例放在 `assets/plugins/ssh/`，使用 kernel/slots/kit、现有菜单和插件国际化，不继续增大 app.js，不添加框架/打包器。插件只拥有连接资料和 SSH 生命周期；provider、agent、backup 的业务仍由原模块拥有。关闭插件清理自己创建的连接/进程，不静默断开 agent 或重写远端配置，并明确提示依赖隧道的模型将不可用。每个动作固定 target 快照；换主机时中止旧读取，待完成 mutation 仍归属于原主机，并在其状态中显示结果。

### 9.2 本机 API

以下均为拟议名称，不是现有接口：

| API | 作用 |
| --- | --- |
| `GET /api/ssh/hosts`、`POST /api/ssh/hosts` | 候选/已保存主机；保存无密码的连接描述。 |
| `POST /api/ssh/connections` | 建立固定 target 的连接，返回 connectionId、generation、capabilities。 |
| `GET /api/ssh/connections/{id}/agents` | 指定连接的 agent 状态；禁止隐式 current host。 |
| `POST /api/ssh/connections/{id}/pick` | 普通点选的单步入口，复用远端既有选择逻辑及目标校验；无需额外确认。 |
| `POST /api/ssh/connections/{id}/plans` | action=`connect|pick|disconnect|undo`，返回 planId、revision、脱敏 diff。 |
| `POST /api/ssh/connections/{id}/apply` | 只接受 planId、revision、operationId；不接受任意文件正文。 |
| `GET /api/ssh/connections/{id}/operations/{op}` | 查执行结果和恢复状态。 |
| `POST /api/ssh/hosts/{id}/tunnel` | enable/disable/status 动作，明确远端实例及 managed provider。 |
| `POST /api/ssh/connections/{id}/transfer-plan` | 选中配置、keys policy、merge/replace 范围；返回目标生成的 plan。 |
| `POST /api/ssh/connections/{id}/service` | 明确初始化、启动、停止或升级远端完整服务，不附带任意 shell。 |
| `DELETE /api/ssh/connections/{id}` | 关闭管理连接；已开启数据隧道生命周期单独处理。 |

API 沿用桌面及 `magpie web` 现有认证，但首版只在本机桌面启用 SSH 控制。浏览器访问远端 magpie web 时，“本机”其实是服务所在主机；未设计清楚该授权与 SSH 凭证访问前不开放此功能。任何 UI 脚本不能读原始 key、密码或随意请求 remote exec。

RPC 使用带 request ID、协议版本及大小限制的 JSON framing（例如 Content-Length 帧）；stdout 只放协议，stderr 放限长脱敏日志。单连接可多请求，写请求按文件集串行。示意：

```json
{"id":"r7","method":"agent.plan","params":{"agent":"codex","action":"pick","field":"model","value":"provider/model","catalogDigest":"…","instanceId":"…"}}
{"id":"r7","result":{"planId":"p9","revision":"sha256:…","expiresAt":"…","changes":[{"path":"~/.codex/config.toml","redacted":true,"diff":"…"}]}}
{"id":"r8","method":"plan.apply","params":{"planId":"p9","revision":"sha256:…","operationId":"…"}}
```

若采用 RPC，对应操作候选为 hello/status、inspect、agent.pick、agent.plan、transfer.plan、plan.apply、operation.get、tunnel.probe、service.control；复用既有远端 Web 完整功能，已有接口能提供的能力不再重复增加 RPC 方法，协议不扩为全套 provider/library CRUD。所有新增方法按 capabilities 白名单暴露。错误码包含 `needs_auth`、`host_key_changed`、`unsupported_platform`、`incompatible_helper`、`path_unknown`、`unreadable_config`、`stale_plan`、`busy`、`operation_unknown`、`recovery_required`，不能统一成“SSH 失败”。

CLI 与 GUI 调用同一控制层。普通远端选择复用 `magpie <agent> <model>` 的操作语义，目标参数的拼写在实施时按现有 CLI 惯例确定；`magpie ssh hosts`、`magpie ssh inspect <host>`、`magpie ssh operation <host> <id>`、`magpie ssh tunnel <host> status` 是管理命令候选。显式 plan/apply 留给批量导入和需要审阅的动作；plan 句柄绑定会话且会过期，无后台本机服务时在同一交互 CLI 会话完成，不假定跨进程内存句柄永久有效。脚本可输出脱敏 JSON，非交互认证失败返回明确退出码。

## 10. 具体代码边界

| 拟议位置 | 职责及复用边界 |
| --- | --- |
| `internal/sshremote/` | 本机 host store、OpenSSH argv、会话/进程生命周期、版本部署、RPC client、-R/-L supervisor；进程走 internal/proc。 |
| `internal/sshcontrol/`，仅在现有接口不足时新增 | 同 binary 的窄 stdio dispatcher、target identity、能力握手，适配实际远端服务；不维护 agent/provider 的另一套状态和业务。 |
| `internal/agent` 的必要增量 | 普通操作复用 Pick/Connect/Disconnect；批量导入所需的预览复用实际编辑逻辑、格式、模型命名与 stash；不另建 GatewayBinding 到 SSH 端口。 |
| `internal/backup/transfer.go` | 复用 Bundle/Parts schema，新增无副作用选择性投影、目标依赖映射、merge/replace计划、机器设置/凭证过滤；不直接 Restore 全包。 |
| 原有 agent/backup 业务层的最小提交协调点 | 批量导入所需的锁、revision、备份与恢复；共享给实际触及相同状态的调用方。不预设必须新增统一 owner 或改写整个仓库 I/O 框架。 |
| `internal/daemon`、`internal/cli` | 复用完整服务安装/启动；新增控制能力握手及实例定位、升级/停机策略，早期 helper 入口避开普通初始化。 |
| `internal/gateway/oneport.go`、`internal/access`，必要时窄增量适配 | 复用远端鉴权、ClientPath 和记账；按实际缺口补实例归属/路径限制。新增专属 listener 需先证明已有入口无法满足。 |
| `internal/provider/remote_magpie.go` | 复用既有 Remote Magpie provider；managed 元数据、隧道状态及循环检测若需要则窄增量实现。 |
| `plugins/ssh/`、`internal/gui/assets/plugins/ssh/`，必要的 Go 服务适配 | 可关闭的内置功能、现有 slot/组件/插件国际化、目标明确的操作入口、复制向导和远端 Web。核心不导入插件包，插件通过 services 使用核心能力。 |
| `internal/appdir` | 仅必要时增加明确 setup 根选择，保持实际 HOME/agent 环境语义；由服务报告事实。 |
| `internal/backup`、`internal/davsync` | SSH key及运行文件的机器归属/排除；复制和同步的互斥协调，不让导入启用别人的网络身份。 |

公共 RPC 类型放无服务依赖的小包，避免 control→agent→gateway→control 循环。目标是远端复用本来就能运行的 Magpie，而不是把每个本机 package 改成任意 host 的多租户接口。

## 11. 备份、同步及会话范围

默认只备份远端本次修改的必要文件和恢复元数据，按事务/用户归属保存。日常 Magpie 加密 setup backup 不能当作 agent 原文件恢复的替代品。保留时间、数量和大小上限需实现；未完成事务及仍被 stash 引用的原始备份不自动清理。

本机自动同步不携带 SSH 私钥引用路径、known_hosts 信任、连接状态、隧道 token、远端 stash 或事务 journal。以后允许显式导出主机列表时，默认仅显示名/alias等非 secret 元数据；另一台机器导入后重新验证身份、定位远端实例、签发凭证，不直接启用隧道。新增 SSH key 的机器归属也必须进入 access key 的 export/restore 过滤，不能因既有 gateway key store 会随 backup 流动而意外复制。

本地工作区首版仅展示模型请求的远端来源和必要用量，不遍历/下载远端会话原文；完整远端 Web 中已有的 Sessions/Usage/Library 可继续使用。本地原生整合、终端 resume 分开设计。若后续提供会话浏览，以 target+agent+native session ID 为键、只读分页，明确内容留存和导出位置；打开会话须在远端项目执行，不能调用本机 resume CLI。

## 12. 分期与验收

### P0：完整实例连接与只读闭环

打通系统 SSH、完整 nogui Magpie 探测/部署、服务身份和 setup 绑定、独立只读入口及“打开远端管理页”。验证未启动实例的 inspect/plan 不创建 providers/logins/plugins/sync、不触发外网或账号进程；明确启动才初始化完整 gateway。已运行实例的读取不额外刷新账号/迁移，使用缓存状态并注明时间。

### P1：可用 MVP，两条供应路径

交付 Linux 远端完整 Magpie 服务；本地工作区完成 Claude Code/Codex 的状态、连接、切模型、预览和恢复，以及两条路径：

- 本机供应：-R、复用网关鉴权的隧道入口、实例 key、健康探测、远端 managed Remote Magpie provider 和准确模型映射。
- 独立供应：选定 providers/groups/model picks/profiles 的 merge 导入，API key 显式选择，远端模型和 agent 意图预览；目标独立运行，本机退出仍能请求。

默认不改远端已有配置；replace 仅对首版明确支持的选中范围开放，不支持的范围保持只读/禁用。两种来源可共存，不扩为全局“模式切换器”。远端其他既有功能通过 -L 的完整管理页使用。

先选 Claude Code/Codex 是为验证多层设置、TOML、原账号共存及 bridge 工具链；不能为接入删除 auth.json 或隐式切账号。OpenCode 第二批加入，复用 openCodeLike 但须覆盖 JSONC 合并、独立 auth 路径、模型列表 Sync；Gemini/Grok 按真实适配成本继续，不从 agent.All() 自动宣布全支持。

### P2：深入整合

GUI ASKPASS/keyboard-interactive、更多远端平台/agent、Library/MCP 的可迁移选择、远端登录 UI、CLI 更新、远端会话和原生 Providers 页面。完整远端服务从 P0/P1 就存在，不延后到这一期。进程结束/终端恢复是独立动作，不因切模型自动杀用户的 agent。

### 必须通过的验收矩阵

| 场景 | 预期 |
| --- | --- |
| 两主机同 user/agent/session ID；同主机两个用户；两个别名同一文件集 | 配置、secret、缓存、会话及用量不串；同文件提交串行。 |
| Host 改向、host key 变化、ProxyJump、Include/Match、加密 key、取消 MFA | 正确解析/阻断/提示；不保存交互答案，不无限重试。 |
| 密码/口令/模型名含空格、引号、shell 字符 | 不注入命令；secret 不出现在日志、argv、前端缓存。 |
| 首次部署、无外网、架构错误、二进制版本不兼容、升级中断 | 原版本可用，配置不变；无兼容能力时拒写。 |
| 未安装 gateway、已有远端 Magpie、不同 config roots | 只读不隐式启动；明确初始化后建立完整服务；核对实际 owner/版本，不重复安装或写错 setup。 |
| 预览后外部改配置、并发桌面 apply、锁超时/NFS不支持锁 | stale/busy/只读，绝不无锁继续提交。 |
| 不可读/无效 JSON/TOML、符号链接变更、硬链接、磁盘满 | 不把错误当缺失；首版不支持的文件拒写，有可解释恢复状态。 |
| 每个 rename 前后断线/杀 helper/模拟断电 | 重连查询同一 operation，不重复操作；安全恢复，后来的编辑不被回滚覆盖。 |
| -R 端口冲突、禁止转发、GatewayPorts 扩大监听、TCP通但key错 | 不标“可用”；应用前端到端验证，不改旧配置。 |
| 普通 token/其他主机 token/撤销 token，访问 /api 或内部 callback | 全部拒绝；不能因 SSH 在 loopback 落地而获得本机权限。 |
| SSH 断线、睡眠、桌面退出、恢复连接 | 管理与模型状态分开；不重放模型请求、不自动恢复旧供应商。 |
| 模式 A 使用 Claude 订阅/工具，多主机同 session/cwd | bridge 在本机，客户端工具在远端；回调留本机，隔离正确。 |
| 复制的 ID 冲突/依赖缺失、未选字段、API keys、路径映射 | merge/replace仅改变选中范围，不复制订阅登录/caller keys，不写本机路径；不误用目标原 key。 |
| 两跳模型转发、同 API/协议转换、两端互指 | 模型名称正确、转换不重复、已识别循环拒绝，跨机用量不重复合计。 |
| 独立供应与本机供应共存，本机睡眠/退出 | 独立供应继续，隧道供应标不可用；不会自动改收费 fallback。 |
| 撤销前用户编辑，备份恢复到另一台电脑 | 保留用户改动；不复制启用隧道，不带出机器专属 key。 |
| GUI 快速换主机、重复点击、窄屏、多语言、断线晚到响应 | 不错目标、不重复提交；文案明确机器归属，旧响应无效。 |
| 普通切模型、开关 SSH 插件、未启用 SSH 的用户 | 切模型保持直接点选；关闭清理所属进程并提示隧道依赖；未启用时不探测 SSH、不新增常驻任务，本机行为不变。 |

单元测试使用 temp HOME 和固定 fixture、fake ssh/本地测试 sshd，禁止访问用户真实账号或互联网；故障通过注入阶段阻塞/失败实现，不依赖 sleep 碰运气。进程和 goroutine 必须清理，新增并发跑 race。按 [代码标准](../code-standards.md) 运行相关包、全量 Go、完整 GUI 双引擎和国际化检查，记录每项未执行内容。发布前用隔离测试主机与真实 agent CLI 验证两条数据拓扑以及恢复，明确“未试”的平台；本文设计阶段没有运行这些实现验收测试。

## 13. 实施前仍需确定的产品选择

建议采用以下默认值，工程验证遇到真实约束时再调整，而不是把选择全部留给用户：

1. **首版平台：** Linux amd64/arm64 远端，系统 OpenSSH；本机 macOS 优先验证，Windows 不依赖连接复用。macOS/Windows 远端后续。
2. **首版认证：** 已验证 key/agent/config/ProxyJump；需要交互时系统终端，GUI ASKPASS 后续。不能宣称第一版已原生支持所有 keyboard-interactive UI。
3. **默认供应选择：** 首次向导默认使用本机网关，也允许直接复制配置独立运行；两者可在远端同一完整实例共存，agent入口不变。
4. **管理入口：** 先复用远端 Web/CLI 与 SSH -L，同一业务层执行操作；窄 stdio 只补已核实缺口。远端真实 setup 为唯一源，提交协调只覆盖所需范围，旧实例缺少新增能力时仅禁用对应新动作，不阻断其原有 Web 管理。
5. **部署信任：** 需确定发行产物校验清单的来源/签名及缓存分发机制；缺少可验证产物时不能悄悄远端编译或执行在线安装脚本。

设计被接受后，首先以 P0 的完整实例识别/副作用隔离，以及 P1 的两跳 Remote Magpie 和选择性导入做技术验证，同时记录哪些现有接口可以直接复用、哪些确有缺口；它们决定工作量，SSH 建连按钮本身不是主要风险。实现 PR 需同步更新 agent-wiring、gui-shell、plugin-runtime、gateway-routing、providers-accounts、sync-backup 及 Claude bridge 的实际契约；本文始终保留为提案，不冒充当前已支持功能。
