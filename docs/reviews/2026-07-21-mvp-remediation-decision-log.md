# TunnelBoard MVP 问题决策与修复方案记录

## 1. 文档定位

本文是 `2026-07-19-mvp-adversarial-review-and-remediation-plan.md` 的持续决策账本，用于逐项确认问题是否成立、最终修复方案、实现范围与验收标准。

原始审查报告保留发现时的证据和初始建议；本文记录讨论后正式确认的决策。两者冲突时，以本文中状态为“已确认”的最新条目为准。

维护规则：

1. 每次只讨论一个问题。
2. 问题经确认后立即更新本文；全部问题讨论完成后统一校验并一次性提交本文。
3. 未经确认的问题只登记为“待讨论”，不把初始建议视为最终方案。
4. 后续问题若影响已确认方案，先重新确认受影响决策，再同步修改原条目，并记录变更原因。
5. “已确认”只代表方案确定；代码完成后再分别标记“已实现”和“已验证”。
6. 每项实现保持单一职责，Git 提交遵循 Conventional Commits。

## 2. 状态定义

| 状态 | 含义 |
| --- | --- |
| 待讨论 | 尚未逐项核对问题和方案 |
| 讨论中 | 当前正在澄清约束或比较方案 |
| 已确认 | 问题结论和修复方案已经确认，但尚未代表代码完成 |
| 已实现 | 代码已经按确认方案完成，尚待完整验收 |
| 已验证 | 实现及规定的验收项全部通过 |
| 已关闭 | 复核后确认不成立、不再适用或被其他方案完整吸收 |

## 3. 问题清单

原始审查表实际包含 28 项问题，而不是旧总结中提到的 27 项。本文以以下 28 项为完整清单；截至 2026-07-21，全部问题均已逐项讨论并确认方案。

| ID | 级别 | 问题摘要 | 当前状态 |
| --- | --- | --- | --- |
| SEC-01 | P0 | SYSTEM 服务直接注册普通用户可写的 Helper | 已确认 |
| SEC-02 | P1 | 同 SID 任意进程可让 Helper 安装任意自签根 CA | 已确认 |
| SEC-03 | P1 | 固定无认证 Caddy Admin API 可被其他本机账户控制或冒充 | 已确认 |
| RUN-01 | P1 | 旧 watcher 可删除同 ID 的新一代 Forward | 已确认 |
| ROUTE-01 | P1 | 已运行的自有 Caddy 被误判为 443 端口冲突 | 已确认，现有改动部分实现 |
| SSH-01 | P1 | 池级 keepalive 没有超时 | 已确认 |
| SSH-02 | P1 | Forward Stop 不关闭全部活跃桥接连接且无等待上限 | 已确认 |
| SEC-04 | P1 | Unix 提权命令存在字符串插值风险 | 已确认 |
| REL-01 | P1 | CI 未使用完整平台打包入口 | 已确认 |
| SSH-03 | P1 | 连接池只按 Host ID 复用旧连接 | 已确认 |
| DATA-01 | P2 | 完全还原在校验前停止运行时且未原子清理 Route 副作用 | 已确认 |
| SSH-04 | P2 | 多跳链只探活首跳 | 已确认 |
| ROUTE-02 | P2 | Route 系统副作用缺少串行事务协调 | 已确认 |
| SEC-05 | P2 | 备份和导入缺少资源预算 | 已确认 |
| SEC-06 | P2 | SSH 密码或口令可能进入 WebView | 已确认 |
| PERF-01 | P2 | Caddy 日志不轮转且日志 tail 无界 | 已确认 |
| UI-01 | P2 | Vault 加载失败被伪装成空状态 | 已确认 |
| UI-02 | P2 | Route 开关失败不回滚 | 已确认 |
| UI-03 | P2 | Route 未知或失败状态被显示为停止 | 已确认 |
| UI-04 | P2 | 批量移动可能部分成功且界面不刷新 | 已确认 |
| UI-05 | P2 | 文件夹选择缺少键盘和读屏语义 | 已确认 |
| UI-06 | P2 | Modal 缺少统一焦点和对话框语义 | 已确认 |
| UI-07 | P2 | Forward 端口异步预检存在迟到响应竞态 | 已确认 |
| UI-08 | P2 | 更新设置读取失败时隐私策略 fail-open | 已确认 |
| UI-09 | P2 | 侧栏更新入口的可发现性和键盘操作不足 | 已确认 |
| PERF-02 | P2 | 前端单 chunk 过大且页面未按需加载 | 已确认 |
| ARCH-01 | P3 | Wails 绑定面未收敛到应用 Module | 已确认 |
| ARCH-02 | P3 | Host 表单和 Modal 状态机重复 | 已确认 |

## 4. 已确认方案

### 4.1 SEC-01：将常驻 SYSTEM 服务改为应用会话级临时提权 Helper

#### 状态与结论

- 状态：已实现并通过代码、单元测试和 Windows 构建验证；正式签名产物的真机 Helper smoke 待 GitHub Actions 执行
- 确认日期：2026-07-21
- 问题结论：成立，且当前测试机仍注册了从普通用户可写工作区启动的 `LocalSystem` 自动服务。
- 产品决策：不需要跨应用重启持续复用授权。每次打开 TunnelBoard 后，首次特权操作允许触发一次 UAC；在该次应用生命周期内复用，应用关闭或崩溃后立即失效。

因此，旧方案中“把 Helper 安装到 Program Files 并继续作为 Windows 服务长期运行”不再符合需求。最终方案是不注册 Windows 服务，改用与当前 TunnelBoard 进程一一绑定的临时高完整性 Helper。

#### 目标生命周期

```text
普通权限 TunnelBoard 启动
        ↓
首次执行 hosts 等特权操作
        ↓
创建本次进程专属 IPC，并触发一次 UAC
        ↓
启动高完整性 Helper 进程
        ↓
本次应用运行期间复用同一连接
        ↓
TunnelBoard 正常退出或崩溃
        ↓
IPC 断开或父进程句柄触发，Helper 自动退出
```

约束：

- 未使用特权功能时不得弹出 UAC。
- 同一次应用生命周期内，Helper 正常存活时不得重复弹出 UAC。
- 应用重新启动后，不继承上一次授权；再次需要特权能力时重新请求 UAC。
- Helper 不注册 SCM、不设为开机启动、不以 `LocalSystem` 常驻。
- Helper 只使用本次 UAC 获得的高完整性管理员令牌。

#### Module 与 Interface

在业务逻辑和 Windows 进程机制之间保留一个小而深的 Interface：

```go
type PrivilegedSession interface {
    Ensure(ctx context.Context) error
    Call(ctx context.Context, request Request) (Response, error)
    Close(ctx context.Context) error
}
```

Interface 语义：

- `Ensure`：保证本次应用进程拥有一个通过身份校验且协议匹配的临时 Helper；已有健康会话时幂等返回。
- `Call`：只发送结构化白名单请求；按 SEC-02 的确认结果，常规能力只包含受托管 hosts 区块的应用和移除，另保留一次性旧服务/旧机器级 CA 迁移；禁止任意命令、任意目标路径、任意证书和未建模的系统修改。
- `Close`：请求 Helper 退出并有界等待。即使调用方未执行 `Close`，Helper 也必须在父进程结束后自行退出。

Windows Adapter 在 Interface 后隐藏 UAC、进程句柄、命名管道、身份绑定、协议握手和退出监控。Router 不得了解或编排这些 Windows 细节。

#### 启动与 IPC 设计

1. 普通权限主程序生成高熵随机管道名，并在提权前创建双向命名管道服务端。
2. 管道使用 `FILE_FLAG_FIRST_PIPE_INSTANCE`、单实例和显式 DACL；不得使用固定全局管道名或默认安全描述符。
3. 主程序通过 `ShellExecuteEx` 的 `runas` 启动 `tunnelboard-helper.exe --session-helper`，并保留返回的 Helper 进程句柄和 PID。
4. Helper 作为管道客户端连接已由主程序创建的唯一管道。连接建立后，主程序读取管道客户端 PID，并要求它与刚刚启动的 Helper PID 完全一致。
5. Helper 核对管道服务端 PID 与传入的父进程 PID一致，并验证父进程属于正式签名的 TunnelBoard 主程序。
6. 双方完成精确协议版本握手后才接受业务请求。版本不匹配必须关闭会话，不得降级调用未知协议。
7. 正式发布的主程序与 Helper 都必须使用可信 Authenticode 发布者签名。主程序启动 Helper 前验证其签名；UAC 提示展示的发布者必须与正式发布身份一致。

参考 Windows 机制：

- `ShellExecuteEx`/`runas`：<https://learn.microsoft.com/en-us/windows/win32/api/shellapi/ns-shellapi-shellexecuteinfoa>
- 命名管道 ACL：<https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights>
- 获取管道客户端 PID：<https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-getnamedpipeclientprocessid>
- 等待父进程终止：<https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-waitforsingleobject>

#### 退出与故障语义

Helper 必须同时监听：

- 主程序发出的显式 `shutdown`；
- 命名管道断开或 EOF；
- 以 `SYNCHRONIZE` 权限打开的父进程句柄进入终止状态。

任一条件发生都应停止接收新请求、结束当前有界操作、关闭句柄并退出。不能只依赖正常关闭回调，因为主程序可能崩溃或被任务管理器终止。

错误处理：

- 用户取消 UAC：返回明确的“用户取消授权”，本次业务操作零副作用。
- Helper 启动或握手超时：终止本次连接并返回可重试错误，不缓存半初始化会话。
- PID、签名或协议验证失败：立即断开并记录安全事件，不尝试兼容调用。
- Helper 在应用运行期间崩溃：当前调用失败；下一次特权操作可以重新执行 `Ensure`，因此可能再次弹出 UAC。
- 应用退出：先请求正常关闭并有界等待；超时不能阻塞应用无限退出，Helper 仍由父进程监控兜底结束。

#### 旧服务迁移

新实现不得再通过旧服务的 `Ping` 判断授权可复用。

首次发现 `TunnelBoardHelper` 服务时，由新启动的会话级 Helper 在本次 UAC 授权内执行一次迁移：

1. 查询并记录旧服务状态与 `ImagePath`。
2. 停止旧服务并有界等待。
3. 删除 SCM 注册。
4. 再次查询并确认服务不存在或已标记删除。
5. 继续使用当前会话级 Helper 完成本次请求。

不要求删除工作区或旧安装目录中的普通权限 Helper 文件；安全不变量是 SCM 和其他高权限持久化入口不再引用这些文件。

如果旧服务清理失败，本次特权业务操作必须失败并提示用户修复，不能同时保留旧常驻服务和新会话 Helper。

#### 预期代码范围

- 替换 `internal/helper/install_windows.go` 中的 `EnsureInstalled` 安装逻辑。
- 移除或收缩 `internal/helper/service_windows.go` 的 SCM 安装、常驻运行代码，仅保留一次性旧服务清理能力。
- 将 `cmd/helper/main.go` 的 `-install/-uninstall/-serve` 模式替换为 `--session-helper`。
- 将固定 `PipePath` 改为每个应用进程独立生成的随机管道。
- 为 Windows Adapter 增加进程启动、PID 校验、父进程监控和协议握手。
- 从 Helper 协议中删除 CA 信任和撤销能力；CA 改由普通权限的当前用户证书存储 Adapter 管理。
- 一次性迁移模式只允许删除已登记的旧机器级 TunnelBoard CA，不接受调用方提供任意证书身份。
- 调整 `scripts/smoke-windows.py`，不再安装自动启动服务，而是验证单次 UAC 会话闭环及退出清理。
- 正式打包加入主程序与 Helper 的签名和签名校验门禁。

#### 验收标准

- 干净机器启动应用但不使用 Route 时，无 UAC、无 Helper、无 SCM 注册。
- 首次特权操作只弹一次 UAC；本次应用内连续修改多条 Route 不再弹 UAC。
- 应用正常退出、崩溃和被强制结束后，Helper 都在规定时限内退出。
- 应用重新启动后的首次特权操作重新弹出 UAC。
- `sc.exe query TunnelBoardHelper` 不存在长期注册的服务。
- 其他同 SID 进程无法连接、抢占或复用当前会话管道。
- 伪造管道服务端、错误 PID、错误发布者签名和错误协议版本全部被拒绝。
- UAC 取消、Helper 启动失败和握手失败均保持零系统副作用。
- 从旧版本升级时，旧的 `AUTO_START LocalSystem` 服务被停止并删除，后续重启系统不会恢复。
- Helper 协议中的 CA 安装和删除操作已不存在，构造旧操作码必须被拒绝且不产生副作用。

#### 后续关联

- `SEC-02` 已确认 CA 只写入当前用户证书存储，Helper 不再拥有常规 CA 信任能力；本节的 Interface、迁移和验收标准已同步收紧。
- `SEC-04` 已确认 macOS/Linux 暂不采用 Windows 的会话级 Helper，而使用无动态命令拼接的系统原生逐次授权 Adapter；这不改变 Windows 不注册常驻服务的决策。
- `REL-01` 必须把 Authenticode 签名、Helper 完整性和“不产生持久服务”加入正式产物门禁。

### 4.2 SEC-02：将 Caddy CA 限定为当前 Windows 用户信任

#### 状态与结论

- 状态：已实现并通过代码、单元测试和 Windows 构建验证；正式签名产物的 CurrentUser CA 真机 smoke 待 GitHub Actions 执行
- 确认日期：2026-07-21
- 问题结论：成立。当前 Helper 接受调用方传入的任意自签 CA，并将其写入机器级 Root；“证书自签、是 CA、声明哈希一致”不能证明它属于 TunnelBoard 当前 Caddy。
- 产品决策：TunnelBoard 只需要让当前登录 Windows 用户信任本地 Caddy CA，不需要让本机其他账户或 SYSTEM 信任。
- 多用户规则：允许把程序安装到 `Program Files` 供多个用户分别使用，但不把多个 Windows 用户同时运行 Web Route 作为支持场景，也不为此引入机器级协调进程。

最终方案是：从提权 Helper 中完全删除常规 CA 安装和删除能力，由普通权限主程序通过当前用户证书存储管理当前用户自己的 Caddy CA。

#### 共享安装与每用户状态

`Program Files` 只存放所有用户可读、普通用户不可修改的签名程序文件：

```text
%ProgramFiles%\TunnelBoard\
  tunnelboard.exe
  tunnelboard-helper.exe
  caddy.exe
```

每个 Windows 用户分别拥有：

```text
%LocalAppData%\TunnelBoard\
  caddy.json
  caddy\pki\...
  logs\...
  runtime\...

CurrentUser\Root
  该用户明确确认过的 TunnelBoard Caddy CA
```

不变量：

- 用户 A 的 Caddy 私钥、根证书、信任记录和运行状态不得被用户 B复用。
- Caddy PKI、日志和运行时数据固定存放在 `%LocalAppData%`，不得写入 `Program Files`、`ProgramData`、源码目录或当前工作目录。
- 现有 `config.root` 即使继续允许重定向 Vault，也不得带着 Caddy PKI 一起重定向到共享盘、同步盘或便携目录。
- 机器级 `LocalMachine\Root` 中不得存在由新版本安装的 TunnelBoard CA。
- 多用户同时运行发生端口或 hosts 冲突时，只返回真实冲突，不实现跨会话抢占、终止或协调。

当前代码使用 `os.UserConfigDir()`，Windows 上对应 `%AppData%`，且 `app.go` 把 Caddy `DataDir` 直接设为可重定向的 `store.Dir()`。实现时需要拆分“Vault 数据目录”和“本机当前用户运行目录”，避免机器相关的 CA 私钥漫游或被共享。

#### 当前用户交互模型

当前用户第一次启用需要本地 HTTPS 的 Web Route 时：

1. 编译目标 Route，并确认 Caddy 能生成或读取当前用户自己的本地 CA。
2. 查询当前用户 Root 存储中是否存在与当前 Caddy CA 完全匹配的证书。
3. 若不存在，展示一次明确确认，至少说明：
   - 将信任一个本地根 CA；
   - 只影响当前 Windows 用户；
   - 用途仅为 TunnelBoard 本地 HTTPS；
   - 展示证书 SHA-256 指纹，提供取消选项。
4. 用户确认后写入 `CurrentUser\Root`。该操作不请求管理员 UAC；若企业策略禁止修改，则返回明确错误并保持未信任状态。
5. Route 还需要修改系统 hosts 时，再按 SEC-01 启动本次应用会话的临时高完整性 Helper；CA 操作本身不得触发 Helper。
6. 同一 CA 已真实存在于当前用户存储时不重复确认；仅有 Vault 记录但实际证书不存在时视为未信任。

程序安装在 `Program Files` 时，其他用户第一次运行会进入相同流程：使用自己的 `%LocalAppData%` 生成自己的 Caddy CA，并只写入自己的 `CurrentUser\Root`。不继承最初安装用户的信任状态。

#### CA 身份、变更与撤销

新增一个小而深的 Interface，调用者不传入 DER、路径或待删除指纹：

```go
type LocalCATrust interface {
    EnsureCurrentCaddyCATrusted(ctx context.Context) (CAIdentity, error)
    RemoveCurrentCaddyCA(ctx context.Context) error
    Status(ctx context.Context) (CATrustStatus, error)
}
```

Interface 后的 Windows Adapter 负责：

- 从固定的当前用户 Caddy 数据目录读取当前 authority；
- 校验证书可解析、是 CA、自签名且编码与当前 authority 一致；
- 通过 Windows 证书存储机制写入或删除当前用户 Root；
- 通过精确证书编码和受控记录定位目标，不按 CN 模糊删除；
- 返回实际存储状态，而不是把 Vault 字段当作事实来源。

信任记录至少保存当前 CA 的 SHA-256 指纹、确认时间和 schema 版本，但它只表示用户曾对该 CA 明确确认。实际信任状态必须实时查询证书存储。

以下情况必须重新确认，不能沿用旧同意：

- Caddy 数据被清空并生成新 CA；
- 当前证书 DER 或公钥发生变化；
- 用户删除证书后再次启用；
- 信任记录 schema发生不兼容变化。

以下情况执行精确撤销：

- 当前用户禁用或删除最后一条需要 Caddy HTTPS 的 Route；
- 用户在设置中显式选择“移除 TunnelBoard 本地 CA”；
- 完全还原或清理流程确认不再保留当前 Route 状态。

应用退出但 Vault 中仍保留启用的 HTTPS Route 时，不因“提权会话结束”而删除 CA。CA 信任状态和 SEC-01 的临时管理员令牌是两件不同的事：前者是当前用户明确保存的配置，后者必须随应用进程结束。

#### 旧机器级 CA 迁移

从旧版本升级时必须同时迁移 SEC-01 的旧服务和本问题的机器级 CA：

1. 从受保护的旧记录和实际机器级 Root 中定位旧 TunnelBoard CA。
2. 会话级 Helper 在本次 UAC 内只允许删除这张已登记的旧机器级 CA，不接受调用方提供任意 DER 或任意目标指纹。
3. 删除后查询 `LocalMachine\Root`，确认旧 CA 不再存在。
4. 如果当前用户仍有启用的 HTTPS Route，普通权限 CA Module 展示当前用户确认后，把当前 Caddy CA 写入 `CurrentUser\Root`。
5. 迁移失败时不得把 Vault 标记成已完成，也不得继续留下无法诊断的双重信任状态。

#### 卸载与其他用户

- 卸载前为当前执行卸载的用户提供“移除当前用户 TunnelBoard CA”操作。
- 不通过提升后的卸载器加载和修改其他离线用户的注册表 hive；这是超出产品需要的跨用户系统修改。
- 如果其他用户曾运行过 TunnelBoard，其证书只存在于各自 CurrentUser 存储；文档应提供该用户登录后手动移除的明确方法。
- 由于多用户同时运行不是支持场景，不增加机器级 CA Broker、共享 Caddy 或跨会话所有权协议。

#### 预期代码范围

- 从 `internal/helper/protocol.go` 删除 `OpTrustLocalCA`、`OpUntrustLocalCA`、`CertDER` 和 `CertSHA256`。
- 删除 Helper 的常规 CA 执行入口；旧机器级 CA 清理使用无任意参数的一次性迁移命令。
- 新增当前用户 `LocalCATrust` Windows Adapter，优先直接使用 Windows 证书存储机制，避免依赖带临时文件的机器级 `certutil` 流程。
- 把 Caddy `DataDir` 从 `store.Dir()` 解耦，Windows 固定到 `%LocalAppData%\TunnelBoard` 下的当前用户目录。
- 修改 `RouterBiz`：`needHelper` 只由 hosts 变更和旧迁移决定；CA 信任由独立 Module 完成。
- 将现有 `CATrustedSHA256` 从可移植 Vault 移到 ROUTE-02 的每用户本机 RouteAppliedState；它只记录本机上次成功登记的 CA，实际状态仍以当前用户证书存储查询结果为准。
- 调整备份、完全还原和诊断逻辑：不得把 CA 私钥、机器路径或其他用户的信任状态作为可移植配置。

#### 验收标准

- 启用 HTTPS Route 后，CA 只存在于当前用户 Root，`LocalMachine\Root` 中不存在对应证书。
- 单独执行 CA 信任和撤销不弹管理员 UAC；企业策略拒绝时返回明确错误且无虚假成功状态。
- 用户 A 和用户 B 依次运行同一个 `Program Files` 安装时，各自生成、确认和信任自己的 CA，互不读取对方数据。
- 不测试、不承诺两个 Windows 用户同时运行 Web Route；出现资源冲突时不得跨用户抢占。
- Helper 协议收到旧 CA 操作码、证书 DER 或指纹请求时拒绝且零副作用。
- 同 CN 的其他根证书、不同指纹的旧 CA 和系统现有证书均不会被误删。
- 当前 CA 指纹变化后必须重新确认；旧 Vault 标记不能静默授权新 CA。
- 证书被用户手动删除后，状态显示未信任，不得继续显示已应用。
- `config.root` 指向共享目录时，Caddy PKI 仍保留在当前用户 `%LocalAppData%`。
- 从旧版升级后，已登记的机器级 CA 被删除；需要继续使用的当前用户 CA 经确认后只写入 CurrentUser。

#### 后续关联

- `SEC-01` 已同步删除 Helper 的常规 CA 能力，并增加旧机器级 CA 的一次性迁移限制。
- `ROUTE-02` 必须把 hosts、Caddy 和当前用户 CA 的目标状态纳入同一次串行 reconcile，但不能因此重新把 CA 放回提权 Helper。
- `DATA-01` 的完全还原不能恢复“已信任”事实，只能恢复 Route 期望；恢复后由当前用户重新确认实际 CA。
- `REL-01` 必须验证安装包只共享只读二进制，并验证每用户 LocalAppData 与 CurrentUser Root 行为。

### 4.3 SEC-03：使用每应用 generation 的权限化 AF_UNIX Caddy Admin Socket

#### 状态与结论

- 状态：已实现并验证
- 确认日期：2026-07-21
- 问题结论：成立。固定 `127.0.0.1:2019` 对本机所有用户可达，且当前实现把 Admin API 可达性错误地当作自有 Caddy 进程存在的证据。
- 产品决策：保留 Caddy Admin API 的无中断热重载能力，但将其从固定 TCP loopback 改为当前用户运行目录中的权限化 AF_UNIX socket。
- 已接受的安全边界：阻止其他 Windows 用户、固定端口扫描、端口抢占和伪装；不承诺隔离同一 Windows 用户身份下已经不可信的任意进程。

不采用固定或随机 TCP Admin 端口，不使用自定义 Header 充当认证，也不把“socket 可连接”当成进程所有权。

#### Admin Socket 模型

每次 TunnelBoard 应用进程生成独立 generation 和运行目录：

```text
%LocalAppData%\TunnelBoard\runtime\<generation>\
  caddy-admin.sock
  caddy.pid
  caddy-state.json
```

其中 generation 使用高熵随机标识，不复用进程 PID。Caddy 配置中的 Admin 地址由 Caddy 进程管理 Module 注入，例如：

```text
unix/<绝对 socket 路径>|0600
```

不变量：

- socket 只能位于规范化后的当前用户 `%LocalAppData%\TunnelBoard\runtime` 子目录。
- generation 目录使用显式 NTFS DACL，只允许当前用户、SYSTEM 和 Administrators；不得继承会扩大访问面的权限。
- 每个应用 generation 最多拥有一个 Caddy Admin socket 和一个自有 Caddy 进程。
- 不使用 `127.0.0.1:2019`，也不回退到其他明文 loopback 管理端口。
- `config.root` 不得重定向 runtime 或 Admin socket。
- socket 路径、PID 文件和状态文件只能作为诊断信息，不能单独证明进程所有权。

#### Caddy 进程所有权 Module

把当前分散的 `Running`、`Reload`、`Start`、`Stop` 收敛到小而深的 Interface：

```go
type CaddySupervisor interface {
    Apply(ctx context.Context, config []byte) (ApplyResult, error)
    Stop(ctx context.Context) error
    Status(ctx context.Context) CaddyStatus
}
```

Windows Adapter 在 Interface 后持有：

- `exec.Cmd` 和 Windows 进程句柄；
- PID、启动时间和 generation；
- 本 generation 的 Admin socket 路径；
- readiness、最后成功配置摘要和最后错误；
- 带 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 的 Windows Job Object。

`Status` 以自有进程句柄和 generation 为事实来源。Admin socket 只用于 readiness 和发送控制请求；外部进程创建一个可响应 `/config/` 的端点，不会因此被识别为自有 Caddy。

#### 启动、热重载与停止

首次 `Apply`：

1. 校验打包 Caddy 的固定版本和完整性。
2. 创建本 generation 运行目录并收紧 DACL。
3. 将 Route 配置和本 generation Admin socket 组合成候选完整配置。
4. 以 `caddy validate --config <candidate>` 验证候选配置。
5. 启动 `caddy run --config <active>`，保留进程句柄并加入 Job Object，不再调用 `Process.Release()`。
6. 同时确认进程仍存活、Admin socket 已就绪且响应来自本 generation 后，才报告 started。

后续 `Apply`：

1. 在进程外生成并校验候选配置。
2. 通过本 generation AF_UNIX socket 向 `/load` 提交完整 JSON。
3. 只有 Caddy 返回成功后才更新最后成功配置记录。
4. `/load` 失败时保留旧活动配置，并返回结构化错误。

正常 `Stop`：

1. 只向本 generation socket 发送 `/stop`。
2. 有界等待持有的进程句柄结束。
3. Admin socket 不可用时，只允许对当前持有句柄对应的自有进程执行兜底终止，绝不根据裸 PID、端口或响应内容终止外部进程。
4. 进程退出后清理当前 generation 的 socket 和运行状态文件。

TunnelBoard 崩溃或被强制结束时，关闭 Job Object 必须回收自有 Caddy，避免孤儿进程继续占用 443 或遗留可调用的 Admin socket。

#### 配置职责调整

Route Compiler 只负责编译 HTTP、TLS、PKI 和反向代理规则，不再硬编码 Admin 地址。CaddySupervisor 负责注入：

- Admin socket；
- 当前用户 Caddy DataDir；
- 运行 generation；
- 进程级配置和持久化策略。

同一应用 generation 内由 Supervisor 在 `admin-a.sock` 与 `admin-b.sock` 两个权限相同的地址间换代；任一时刻只有一个地址属于当前 Caddy 配置。业务层不得直接拼接 `/load`、`/stop` 或 `/config/` 请求。

实现阶段使用正式钉版 Caddy `v2.11.4` 在 Windows 真机完成 POC 后，对原设计作出必要修正：Caddy 重载会重建 Admin listener，如果新旧配置复用同一 AF_UNIX 路径，旧 listener 的迟到清理会 unlink 新 listener，导致后续 `/stop` 无法连接。因此热重载固定采用“通过旧 socket 提交包含新 socket 的完整配置 → 以新 socket readiness 确认成功 → 切换 Supervisor ownership → 清理旧 socket”的双槽换代。候选 `caddy validate` 使用 `admin.disabled=true`，避免验证进程预占活动 socket。该变化不扩大信任边界，也不回退到 TCP。

#### 兼容性降级

实现前必须对正式钉版 Caddy 执行 Windows AF_UNIX POC，覆盖启动、`/load`、`/stop`、socket ACL、应用崩溃和残留 socket 清理。

如果正式钉版 Caddy 在支持范围内的 Windows 版本上无法可靠使用 AF_UNIX，则唯一允许的降级方案是完全关闭 Admin API，并采用“候选配置校验 → 停止自有旧进程 → 启动新配置 → 失败恢复旧配置”的受控重启。不得静默降级回 TCP Admin 端口。

#### 预期代码范围

- 从 `internal/route/caddy.go` 删除固定 `127.0.0.1:2019`，让 Route Compiler 不再决定 Admin transport。
- 重构 `internal/caddy/adapter.go`，以进程句柄和 generation 管理状态，并通过 AF_UNIX transport 调用 Admin API。
- 重写 `internal/caddy/process_windows.go`，保留进程句柄并加入 Job Object。
- 为非 Windows Adapter 使用受权限保护的 AF_UNIX socket 和受控进程组，保持同一 Interface 语义。
- 将 runtime 路径固定到 SEC-02 确认的当前用户 `%LocalAppData%`，与 Vault 重定向解耦。
- 删除 `AdminURL` TCP 测试接缝和“API 可达即 Running”的测试，改为进程所有权、socket ACL 与 generation 测试。

#### 验收标准

- Caddy 不监听 `127.0.0.1:2019` 或其他 TCP Admin 端口。
- 其他 Windows 用户无法打开当前用户的 Admin socket；失败不会影响现有配置。
- 外部进程预占 2019、伪造 HTTP `/config/` 或创建同名 PID 文件，均不会被识别为 TunnelBoard Caddy。
- Route 配置变化通过 AF_UNIX `/load` 无中断生效；非法配置保留旧服务并返回错误。
- 同一用户的两次应用 generation 不会互相连接或停止对方 Caddy。
- 正常退出时 Caddy 优雅停止；应用崩溃和强制结束时 Job Object 在规定时限内回收 Caddy。
- socket 残留、PID 复用和进程异常退出均不能导致删除或终止外部进程。
- 正式钉版 Caddy 的 Windows AF_UNIX POC 在最低支持 Windows 版本上通过；失败时只能进入“Admin off”明确降级路径。

#### 后续关联

- `ROUTE-01` 的运行判断必须使用 CaddySupervisor 的自有进程状态，不能重新以端口或 Admin 可达性判断。
- `ROUTE-02` 的串行 reconcile 通过单个 `Apply` Interface 提交完整配置，不直接操作 Admin API。
- `PERF-01` 的日志生命周期应由同一个 CaddySupervisor 管理。
- `REL-01` 必须把钉版 Caddy 的 AF_UNIX Windows POC 和无 TCP Admin 监听加入发布门禁。

### 4.4 RUN-01：以 generation 隔离 Forward 运行实例的全部异步写入

#### 状态与结论

- 状态：已实现并验证
- 确认日期：2026-07-21
- 问题结论：成立。当前 `watch(id, run)` 在旧实例的 `Done` 关闭后无条件执行 `delete(b.runs, id)` 并写终态；旧实例迟到的 `disconnected/reconnected` 事件也会无条件覆盖同 ID 新实例的状态。
- 产品决策：同一 Forward ID 的每次启动都分配单调递增的内部 generation；只有仍同时匹配当前 generation 和当前 `runHandle` 的异步回调，才允许修改运行表或对外状态。
- generation 只属于一次应用进程内的 RuntimeBiz，不写入 Vault、不暴露给前端，也不跨应用启动持久化。

仅在旧 watcher 的 `delete` 前检查 map 是否存在并不充分：map 中可能已经是新实例，而且旧事件、启动失败和 Shutdown 仍然可以覆盖新状态。因此代际校验必须成为 RuntimeBiz 内部统一不变量。

#### Runtime Entry 与状态机

`runs` 从裸 `runHandle` 收敛为带代际和内部阶段的 entry：

```go
type runPhase uint8

const (
    runStarting runPhase = iota
    runRunning
    runStopping
)

type runEntry struct {
    generation uint64
    phase      runPhase
    run        runHandle
}
```

RuntimeBiz 至少增加：

```go
runs          map[int]*runEntry
nextGeneration uint64
closing        bool
```

其中：

- `generation` 在 RuntimeBiz 锁内分配，进程生命周期内只递增、不复用。
- `phase` 是 Module 内部并发状态，不直接替代前端的 `running/reconnecting/stopped/error` 展示状态。
- `runHandle` 身份与 generation 同时校验，防止将来错误复用 generation 或 entry 时产生误写。
- `closing` 一旦进入 Shutdown 就拒绝新的 Start，直到本 RuntimeBiz 生命周期结束。

#### Start 语义

1. 在执行 SSH 拨号等昂贵操作前，先在锁内检查 `closing` 和现有 entry。
2. 已存在 `starting/running` entry 时幂等返回，不再创建第二个真实实例。
3. 不存在时分配 generation，立即登记 `starting` 占位，再到锁外加载配置、解析链并启动实例。
4. 启动成功后重新加锁；只有该 ID 仍指向相同 generation 且 phase 仍允许发布，才能写入 `run`、切换为 `running` 并启动 watcher。
5. 若占位已被 Stop、Shutdown 或更新代替，刚启动成功的实例必须立即在锁外停止，不能发布为当前实例。
6. 加载、解析或启动失败时，只有 generation 仍匹配才清除占位并写 error；旧代失败不得覆盖后来一代。

这样可以同时消除“并发 Start 先建立两条 SSH/监听，再事后丢弃一条”的副作用窗口。

#### Stop、watcher 与事件语义

- `watch` 和 `handleEvent` 都显式携带 `{id, generation, run}`。
- 每次写入前统一调用内部匹配判断，例如 `isCurrentLocked(id, generation, run)`；不匹配即静默丢弃旧事件，但可以记录 debug 日志。
- 当前代 `Done` 才能删除当前 entry 并落 `stopped/error`；旧代 `Done` 只能结束自己的 watcher。
- Stop 在锁内把目标 generation 标记为 `stopping`，在锁外调用该实例的 `Stop(ctx)`，避免持锁执行网络和 goroutine 等待；清理成功前不能从 runs 表摘除。
- 对 `starting` 占位的 Stop 必须留下取消意图；Start 完成发布检查时发现已取消，就停止刚创建的实例并保持 stopped，不能重新变成 running。
- Stop 返回的错误只属于被停止的 generation；它不得覆盖同 ID 已经启动的新 generation。
- Stop 清理成功后才允许 Start 新 generation；当前代仍为 `stopping` 或 Stop timeout 时，Start 返回明确的 `ErrForwardStopping`，避免旧连接仍存活时叠加新实例。
- Stop 成功后由匹配 generation 的完成路径摘除 entry 并落 stopped；若 watcher 迟于新一代启动才消费旧 Done，generation 校验仍会阻止它覆盖新代。
- Stop timeout 时保留当前 entry，状态显示 error/stopping 且明确“后台清理未完成”；后台最终完成后只有匹配 generation 才能落 stopped。

RuntimeBiz 应提供一个私有的状态写入路径，集中执行 generation 校验；不要让 watcher、Start 失败路径和 Stop 各自复制一套近似判断。这个小而深的内部 Interface 能把代际复杂性隐藏在 RuntimeBiz 内，调用方仍只需要 `Start/Stop/Status/Shutdown`。

#### Shutdown 语义

1. 在锁内设置 `closing=true`，使新的单条启动、批量启动和 AutoStart 全部失败并返回明确的关闭中错误。
2. 快照当时所有 entry 并标记 stopping，但在实际清理完成前不伪报 stopped。
3. 使用同一个全局 context 并行停止快照中的实例；单项成功后按 generation 摘除，单项超时保留错误事实。
4. 达到全局 deadline 后执行 `SSH-02` 定义的精确连接代兜底并允许应用继续退出，不能按 Forward 数量逐项累加等待。
5. 最后关闭 SSH 连接池。活跃 bridge、Stop deadline 和连接代兜底均复用 `SSH-02`，RUN-01 不另造一套超时策略。

#### 预期代码范围

- `internal/biz/runtime.go`：引入 `runEntry`、generation 分配、closing 状态和统一的当前代校验/状态写入路径。
- `internal/biz/runtime_test.go`：增加可控制 `Start`、`Done` 和 `Events` 时序的 fake runHandle，对故障交错做确定性测试。
- 不修改 Vault schema、Forward ID 语义或前端数据模型；generation 不穿透 RuntimeBiz 的外部 Interface。

#### 验收标准

- Stop A 清理成功后立即 Start B，再让 A 的 watcher 迟到消费 Done，B 仍在运行表中且状态保持 running。
- B 启动后，A 迟到发送 disconnected、reconnected 或 error，B 的状态、错误和延迟均不变化。
- 同一 ID 的两个并发 Start 最多创建并启动一个真实 runHandle，不产生短暂的重复监听或 SSH 连接。
- Start A 尚未完成时执行 Stop；A 后续成功也不会被发布为 running，并会被清理。
- Stop A timeout 时同 ID Start B 被拒绝；A 后台清理完成并摘除后才允许新一代启动。
- 旧代 Start 失败不会删除新代 entry，也不会把新代状态改为 error。
- Shutdown 开始后所有 Start 入口均被拒绝，迟到事件不能恢复 running。
- 上述交错在高次数循环及 `go test -race ./internal/biz` 下通过且无数据竞争。

#### 后续关联

- `SSH-02` 已把 runHandle 的停止 Interface 收敛为 `Stop(ctx)`，并决定成功、timeout 和应用退出的清理语义；RUN-01 负责把这些结果只写入匹配 generation。
- `SSH-03` 若因 Host 连接身份变化重启 Forward，必须复用 RuntimeBiz 的 generation 语义，不能另建一套重启标记。
- `Shutdown` 只用于应用永久退出并设置不可逆的 `closing`。`DATA-01` 的完全还原改用可恢复的 `SuspendAll(ctx)`，由应用 mutation lock 阻止还原期间的 Start、AutoStart 和其他变更；失败补偿或事务结束后仍可恢复使用同一个 RuntimeBiz。

### 4.5 ROUTE-01：由 CaddySupervisor 根据自有进程状态决定热重载或冷启动

#### 状态与结论

- 状态：已实现并验证
- 确认日期：2026-07-21
- 问题结论：原问题成立。旧实现每次应用 Route 都预检 443，Caddy 已运行时必然把其自身监听误判为外部冲突，使第二条及后续 Caddy Route 无法进入活动配置。
- 现有改动：提交 `6433b5a` 已通过 `if !prevRunning { DiagnosePort() }` 修复直接症状并增加多 Route 回归测试；提交 `4cfc90e` 又在配置字节未变化时跳过重复 Reload。
- 最终决策：保留现有回归行为，但不保留 `RouterBiz` 组合调用 `Running/DiagnosePort/Reload` 的浅 Interface。最终由 `SEC-03` 确认的 CaddySupervisor 根据自有 generation 和进程句柄，在一个 `Apply` 调用内部完成热重载、冷启动、端口冲突分类和配置摘要判断。

现有修复不能直接标记为“问题已验证关闭”，因为当前 `Running()` 仍以固定 TCP Admin API 可达性为事实来源。其他进程伪造 Admin 端点时，RouterBiz 仍可能错误跳过 443 检查并向非自有进程发送配置。该判断将在 SEC-03 重构中被进程所有权状态取代。

#### 最终 Interface

RouterBiz 不再了解 Caddy 的监听预检、Admin transport、进程句柄或磁盘配置文件，只提交完整目标配置并解释结构化结果：

```go
type CaddyApplyOutcome string

const (
    CaddyApplied      CaddyApplyOutcome = "applied"
    CaddyUnchanged    CaddyApplyOutcome = "unchanged"
    CaddyPortConflict CaddyApplyOutcome = "port_conflict"
)

type CaddyApplyResult struct {
    Outcome CaddyApplyOutcome
    Detail  string
}

type CaddySupervisor interface {
    Apply(ctx context.Context, config []byte) (CaddyApplyResult, error)
    Stop(ctx context.Context) error
    Status(ctx context.Context) CaddyStatus
}
```

`PortConflict` 是预期的产品降级结果，不与配置无效、二进制损坏、Admin 通信失败等执行错误混为一类。RouterBiz 收到它后保留 hosts-only，并把冲突状态如实交给前端。

#### Apply 决策规则

Supervisor 持有当前 generation 的自有 Caddy 进程时：

1. 以进程句柄和 generation 确认所有权与存活状态。
2. 不执行 443 bind 预检，因为 443 正由本 Module 自己管理的进程占用。
3. 新配置摘要等于“最后一次成功应用摘要”时返回 `unchanged`，不调用 `/load`。
4. 配置变化时通过本 generation 的 AF_UNIX Admin socket 热重载；成功后才更新摘要。
5. 热重载失败保留旧活动配置并返回错误，不退化为 `PortConflict`。

Supervisor 没有自有 Caddy 进程时：

1. 先验证候选配置。
2. 可以在 Supervisor 内部做一次 443 预检以改善提示，但预检不是成功依据。
3. 启动自有 Caddy，并以进程存活、真实 bind 结果和 readiness 作为最终依据。
4. 外部进程占用 443 时返回结构化 `port_conflict`，不杀外部进程、不写虚假的已应用状态。
5. 即使预检通过后端口才被抢占，也必须根据实际启动失败正确收敛为冲突或明确启动错误，不能报告成功。

#### 现有实现的迁移

- 保留 `TestApplyRouteCaddyAlreadyRunningReloads` 所证明的产品行为，并改写为针对 CaddySupervisor Adapter 的自有进程测试。
- `DiagnosePort()` 从 RouterBiz 的外部 seam 删除；如仍保留预检，它只能是 Supervisor 的内部实现细节。
- `Running()` 不再以 Admin API 可达性判断，改为 `Status()` 返回的自有 generation 状态。
- `Reload()` 收敛进 `Apply()`，RouterBiz 不再决定热重载还是冷启动。
- `4cfc90e` 的配置未变化优化迁入 Supervisor，比较“最后成功应用摘要”，不由 RouterBiz 读取 `caddy.json` 推断活动配置。
- RouterBiz 的回滚不再直接读取和重载旧磁盘配置；ROUTE-02 已确认由 RouteCoordinator 按 beforeApplied、transaction ID 和 journal 统一补偿，CaddySupervisor 只接受带 revision 的完整目标配置。

#### 验收标准

- 自有 Caddy 已运行时新增第二条 Route：不调用端口预检，沿同一 PID/generation 热重载，新旧 Route 都可访问。
- 自有 Caddy 已运行且配置未变化：不调用端口预检和 `/load`，返回 `unchanged`。
- 未运行且 443 被外部进程占用：hosts 正常应用，Caddy 返回 port conflict，不停止或重配外部进程。
- 端口预检通过后再模拟外部进程抢占 443：真实启动失败不得报告 CaddyApplied。
- 外部进程伪造旧 TCP Admin API、AF_UNIX socket、PID 文件或状态文件，均不能让 Supervisor 进入“自有进程正在运行”分支。
- 自有进程异常退出但 socket 残留时，下一次 Apply 走冷启动/明确冲突路径，不对残留 socket 热重载。
- 配置校验错误、AF_UNIX `/load` 错误和端口冲突分别返回可区分结果，前端不得都显示为“已停止”。

#### 后续关联

- 本方案是 `SEC-03` 的具体路由应用规则；实现时两项应共用一次 CaddySupervisor 重构，不应先后叠加两层 Adapter。
- `ROUTE-02` 负责把一次完整 Route reconcile 串行化，并决定 Caddy Apply 失败后的 revision 与补偿语义。
- `UI-02`、`UI-03` 必须区分 desired、applied、port conflict 与 unknown，不能把 hosts-only 降级显示为全部成功或全部停止。

### 4.6 SSH-01：为共享首跳提供有界且单飞的 keepalive 探活

#### 状态与结论

- 状态：已实现并通过 race 验证
- 确认日期：2026-07-21
- 问题结论：成立。当前池级 `keepAliveLoop` 同步调用 `ssh.Client.SendRequest`；网络黑洞时该调用可能永久不返回，pool entry 会一直显示 alive，所有共享该首跳的 Forward 也无法及时进入重连。
- 产品决策：共享首跳和独占连接复用同一套有界 SSH keepalive 实现。连接池负责判死并关闭当前连接代，Forward Runtime 继续负责退避重连；不让连接池建立第二套主动重拨循环。
- 配置决策：沿用现有内部 timeout 策略，单次请求至少 5 秒、最多 10 秒，并与用户配置的 keepalive interval 分离；不增加新的前端设置。

#### 有界探活 Module

将当前独占 Forward 中已有的超时逻辑抽取为 `internal/forward` 内部共享实现。其 Interface 接受 SSH client、timeout 和停止信号，返回延迟或分类错误；调用者不再直接裸调 `SendRequest`。

实现要求：

1. `SendRequest("keepalive@openssh.com", true, nil)` 在单独 goroutine 中执行，结果写入容量为 1 的 channel。
2. 调用方同时等待请求结果、timeout 和当前连接代的 stop 信号。
3. 收到响应即证明 transport 有响应；即使服务端返回“不支持该 global request”，也不能把“收到拒绝响应”误判为网络黑洞。
4. timeout 返回明确的 `ErrKeepAliveTimeout`，但不直接对未知或后来一代连接产生副作用。
5. 关闭真实 `ssh.Client` 必须使阻塞的请求退出；测试 fake 也必须遵守这一关闭契约，以验证探活 goroutine 不泄漏。

该实现是连接生命周期 Module 的内部 seam，不新增业务层可见方法。独占 Forward 和 SSHConnPool 都从同一处获得 timeout、错误分类和延迟测量语义，避免两套实现继续漂移。

#### Pool 连接代与单飞规则

每个 pool entry 为真实 SSH client 维护单调递增的 `connectionGeneration`。keepalive loop 启动时捕获：

```text
host identity + connection generation + client + stop + closeAll
```

规则如下：

- 同一连接代同一时刻最多一个 keepalive 请求。
- 使用“一次探活结束后再等待下一 interval”的 timer，不使用可能积压 tick 的并发调度。
- timeout 或真实请求错误后，先在 entry 锁内确认 client 与 generation 仍是当前代，再标记 dead。
- 放锁后只调用该连接代捕获的幂等 `closeAll`；不得通过 entry 当前字段关闭可能已经换代的新 client。
- watcher、keepalive、引用归零和 CloseAll 可以竞争，但最终只关闭一次当前捕获的 client。
- 旧连接代迟到返回成功或错误时，不得恢复、判死或关闭新连接代。

#### 故障传播与重连

共享首跳超时后的顺序固定为：

1. 当前 pool entry 标记 dead，并记录 host identity、generation 和 keepalive timeout 诊断信息。
2. `closeAll` 关闭 SSH transport，使阻塞的 `SendRequest`、`Wait()` 以及依赖该 transport 的下游链路退出。
3. 每条受影响 Forward 通过现有 RuntimeEvent 进入 reconnecting。
4. 各 Forward 按既定指数退避重新 Acquire；entry 锁继续保证并发租户只进行一次首跳拨号。
5. 认证失败、指纹变化和手动 Stop 仍是既有终止规则，不能因本修复变成无限重试。

连接池不主动定时重拨，因为没有租户时重拨没有价值，而且会形成与 Forward Runtime 竞争的第二套重连状态机。

#### Stop 与 Shutdown

- 引用归零或 `SSHConnPool.CloseAll` 关闭 stop 信号并关闭 client 后，正在执行的探活应退出，不记录为运行故障。
- RuntimeBiz 已进入 RUN-01 的 `closing` 状态时，连接关闭不能触发新的 Start 或 reconnect。
- 手动 Stop 引起的关闭保持 stopped，不得因 keepalive goroutine 的迟到错误改写为 error/reconnecting。
- keepalive loop 自身不持有阻止 pool entry 回收的额外业务引用。

#### 适用范围

- 本项只负责池化首跳 transport 的探活和超时。
- 单跳 Forward 的末端 client 就是共享首跳，因此可以直接受益。
- 多跳链中“首跳正常、末跳黑洞”的情况不由本项解决，继续由 `SSH-04` 定义末跳探活和尾链重建。
- 活跃 TCP bridge 的强制关闭与 Stop deadline 由 `SSH-02` 处理。

#### 预期代码范围

- `internal/forward/probe.go`：抽取可测试的有界 keepalive 实现和错误分类。
- `internal/forward/port_forward.go`：独占连接改用共享实现，删除重复 timeout/select 逻辑。
- `internal/forward/conn_pool.go`：增加连接代、单飞 timer 探活和 generation-safe 判死。
- `internal/forward/conn_pool_test.go`、`port_forward_test.go`：增加阻塞请求、关闭解阻塞、旧代迟到和多租户单飞测试。

#### 验收标准

- fake `SendRequest` 永不自行返回时，在 timeout 内 entry 变为 dead，且对应 client 被关闭。
- client 关闭后阻塞的探活 goroutine 退出；高次数换代后 goroutine 数回落，无线性泄漏。
- 多条 Forward 共享同一首跳超时后，只发生一次首跳重拨，全部租户最终复用新连接代。
- 旧代探活在新代建立后迟到返回，不改变新代 alive 状态，也不关闭新 client。
- 同一连接代不存在两个并发 `SendRequest`；慢响应不会累积 ticker 任务。
- CloseAll、最后一个 lease release 和手动 Stop 均能取消探活，且不会触发自动重连或伪造 keepalive error。
- 独占与共享连接使用同一 5～10 秒 timeout 规则，相关测试使用可注入短 timeout 而不依赖真实等待。
- `go test -race ./internal/forward` 在超时、换代、release、CloseAll 并发交错下通过。

#### 后续关联

- `SSH-03` 的连接 identity 换代必须复用这里的 `connectionGeneration`，旧 identity 的探活不能影响新 identity。
- `SSH-04` 已确认复用同一个有界探活 Module：单跳只由池探测首跳；多跳额外探测末跳，失败时由 ChainLease 优先保留健康首跳并重建该 Forward 的尾链。
- `RUN-01` 的 Forward generation 负责屏蔽旧连接故障事件；pool connection generation 负责屏蔽旧 SSH client 探活结果，两者职责不同且都必须保留。

### 4.7 SSH-02：以运行 context、活跃连接 registry 和全局 deadline 保证可停止

#### 状态与结论

- 状态：已实现并通过 race 验证
- 确认日期：2026-07-21
- 问题结论：成立。当前 Stop 只关闭 listener 和 SSH lease，然后无限等待 accept/bridge goroutine；本地连接、SSH channel 和远程转发连接没有登记。共享首跳仍有其他租户时，释放 lease 不会关闭 transport，空闲 bridge 可永久阻塞。
- 产品决策：每个 LocalForward 建立运行 context 和活跃连接 registry；Stop 主动关闭该 Forward 的全部资源并受 5 秒总 deadline 约束。
- 兜底决策：正常的每 Forward 资源关闭约 3 秒后仍不能退出时，允许关闭它所使用的精确 SSH connection generation，使同一首跳的其他 Forward 短暂进入重连，以保证停止和应用退出不会永久卡住。

只在 `wg.Wait()` 外包一层 timeout 不算修复：它会让调用返回，但后台连接仍传输数据，goroutine 和 SSH channel 仍被占用，界面却可能错误显示 stopped。

#### LocalForward 生命周期 Interface

runHandle 的停止方法改为显式接收 deadline：

```go
type runHandle interface {
    Start() error
    Stop(ctx context.Context) error
    Done() <-chan struct{}
    Events() <-chan forward.RuntimeEvent
    Err() error
}
```

LocalForward 在 Start 时创建：

- `runCtx/runCancel`：整个 Forward generation 的取消信号；
- `serveDone`：accept loop 确认结束；
- `monitorDone`：SSH 生命周期监控确认结束；
- `stopDone/stopErr`：所有并发 Stop 调用共享的最终清理结果；
- `connRegistry`：登记本 Forward generation 拥有的每一个 `net.Conn`。

不继续使用当前简单 `sync.Once` 包住完整 Stop。第一次 Stop 只负责原子启动一次清理流程；所有调用者用各自 context 等待同一个 `stopDone`，并得到一致的成功或后台仍未完成事实。

#### 活跃连接 registry

registry 是 LocalForward 内部的深 Module，提供很小的内部 Interface：登记、移除、封闭并关闭全部连接。

不变量：

- Accept 得到本地、远程或 SOCKS 连接后立即登记，再进入任何握手或 Dial。
- SSH Dial 或本地 Dial 成功得到第二端后立即登记，再开始 bridge。
- registry 一旦封闭，竞态中后来登记的连接必须当场关闭并返回失败。
- 每个连接移除和 Close 都是幂等的；关闭快照在 registry 锁外执行。
- registry 只管理当前 Forward 拥有的 channel/连接，不在正常 Stop 中关闭仍由其他 Forward 共用的首跳 transport。

这样可以覆盖普通 Local Forward、Dynamic SOCKS、Remote Forward，以及阻塞在 SOCKS 握手或双向复制中的连接。

#### context-aware Dial 与 bridge

- 本地 TCP 拨号改用 `net.Dialer.DialContext(runCtx, ...)`。
- listener Accept 通过关闭 listener 取消；读取 SOCKS 握手通过关闭已登记连接取消。
- `bridge(runCtx, c1, c2)` 同时启动两个 copy 方向；任一方向结束或 context 取消时关闭连接对，并等待两个 copy goroutine 都退出后才返回。
- handler goroutine 必须全部纳入生命周期等待。Stop 先等待 `serveDone`，确保不再发生新的 WaitGroup Add，再等待 handler，避免 Add 与 Wait 竞态。
- monitor goroutine也有明确的 `monitorDone`，不能只依赖事件 channel 最终可能关闭。

`ssh.Client.Dial` 没有 context Interface，且共享 transport 上的单个 channel-open 请求无法可靠单独取消，因此需要下述精确连接代兜底。

#### Stop 顺序与 5 秒预算

第一次 Stop 固定执行：

1. 原子切换为 stopping，取消 `runCtx`，阻止重连和新业务处理。
2. 关闭并摘除 listener，使 accept loop 结束。
3. 封闭 registry 并关闭当前 Forward 的全部已登记连接，打断 SOCKS 读取和 bridge。
4. 释放池化首跳 lease，或关闭当前 Forward 独占的 SSH 尾链。
5. 等待 serve、monitor、handler 和两个方向的 copy goroutine 退出。
6. 清理完成后关闭 `stopDone`，由 RuntimeBiz 对匹配 generation 落 stopped。

时间策略：

- 总预算默认为 5 秒，是内部产品常量，不增加前端设置。
- 正常资源关闭后约 3 秒仍未完成，则调用 lease 捕获的 `AbortGeneration`，只关闭该 Forward 实际使用的 SSH connection generation。
- Abort 可能让共享同一首跳的其他 Forward进入 reconnecting，但 SSHConnPool 仍只单飞重拨一次。
- 剩余预算内仍未结束则返回结构化 `ErrStopTimeout`；应用运行期间保留 error/stopping 事实，应用退出路径则记录诊断后继续退出。
- 后续代码不得假设“context deadline 到达”等于后台资源已经清理完成。

#### Chain Lease 调整

当前 `ChainDialer` 返回含义过宽的 `shared bool`。SSH-04 已确认将停止、探活和重建所需能力一起收敛到小而深的 ChainLease Interface：

```go
type ChainLease interface {
    Terminal() *ssh.Client
    WaitLoss(ctx context.Context) error
    Rebuild(ctx context.Context) (ChainLease, error)
    Release()
    AbortGeneration()
}
```

- `Terminal` 返回当前链末跳 client，供 Local/Remote/Dynamic Forward 发起业务连接。
- `WaitLoss` 合并首跳 generation 失效通知与多跳末端的有界探活；单跳不重复池级探测。
- `Rebuild` 根据 lease 捕获的所有权决定保留健康首跳并只重建尾链，或在首跳已失效时重新 Acquire 首跳再建尾链。
- `Release` 是正常 Stop 路径：关闭本 Forward 独占尾链并归还首跳引用。
- `AbortGeneration` 是超时兜底：先关闭本 Forward 拥有的尾链；若仍阻塞在共享 transport 的不可取消请求，再 generation-safe 地关闭捕获的首跳 transport，不能通过当前 Host ID 查找后误伤新连接代。
- `Release`、`AbortGeneration` 和失效通知都必须幂等；旧 lease 的任何迟到动作不得影响新 lease。

#### RuntimeBiz 与 Shutdown

- RUN-01 已同步修改：entry 在 Stop 成功前保持 stopping，Stop timeout 时不允许同 ID 新 generation 启动。
- 单条用户 Stop 使用 5 秒 context；失败返回明确错误，不能先把界面写成 stopped。
- Shutdown 为全部当前 generation 创建同一个全局 5 秒 context，并行发起 Stop；总耗时不按 Forward 数量累加。
- 全局 deadline 到达后，RuntimeBiz 关闭尚未退出实例的精确 SSH generation，记录未完成项，然后允许桌面进程继续退出。
- 手动 Stop、永久 Shutdown 和 DATA-01 的可恢复 `SuspendAll` 都复用同一 `Stop(ctx)` Interface 与 5 秒全局预算，不各自实现资源清理和等待策略。

#### 预期代码范围

- `internal/forward/port_forward.go`：运行 context、registry、结构化 stop completion、context-aware bridge 和 Dial。
- `internal/forward/conn_pool.go`：结构化 ChainLease 与 generation-safe Abort。
- `internal/biz/runtime.go`：`Stop(ctx)`、并行全局 Shutdown、stopping/timeout 的 generation-safe 状态。
- `app.go`：为用户停止、显式退出和 restore 编排传入相应 context。
- `internal/forward/*_test.go`、`internal/biz/runtime_test.go`：增加阻塞阶段和 deadline 故障注入。

#### 验收标准

- 两端都无流量的 `net.Pipe` bridge 在 Stop 后双方及时收到 EOF，handler 和两个 copy goroutine 都退出。
- 阻塞在 SOCKS5 握手、本地 Dial、SSH channel open、Local/Remote accept 和双向复制的路径都能停止。
- 正常停止一条 Forward 只关闭其 registry 中的连接和 lease，不中断其他 Forward 的共享首跳。
- 模拟不可取消的共享 SSH 请求时，约 3 秒触发精确 connection generation Abort；其他租户只进行一次首跳重拨，手动停止的 Forward 不重连。
- 旧 lease 的 Abort 在新 pool generation 建立后执行，不会关闭新 client。
- 50 条 Forward 的 Shutdown 共享同一个 5 秒 deadline，不出现 250 秒的串行最坏时间。
- 并发重复 Stop 不重复 close、不 panic，所有调用者得到一致完成事实；调用者自身 context 超时不重新启动第二次清理。
- Stop timeout 时 Runtime 状态不显示 stopped，且同 ID Start 被拒绝；后台清理完成后状态正确收敛。
- 高次数 Start/Stop/Shutdown 在 `go test -race ./internal/forward ./internal/biz` 下通过，goroutine 数不随轮次线性增长。

#### 后续关联

- `RUN-01` 已同步修改 Stop 成功、timeout 与新代启动规则。
- `SSH-01` 的 connection generation 与本项 `AbortGeneration` 必须是同一代际事实来源。
- `SSH-04` 已在 ChainLease 的实现中明确首跳 lease、首跳 connection generation、尾链 generation 与 tail ownership，不再增加新的布尔返回值。
- `DATA-01` 的 CommitRestore 通过 `SuspendAll(ctx)` 使用同一个全局停止 context；StageRestore、错密码或预检失败不得进入停止流程，也不得把 RuntimeBiz 设为永久 closing。

### 4.8 SEC-04：移除 Unix 动态命令源码并保留平台原生逐次授权

#### 状态与结论

- 状态：已实现并通过代码与单元测试验证；macOS/Linux 真机提权回归待对应 Runner 执行
- 确认日期：2026-07-21
- 问题结论：成立。Linux 把受 `TMPDIR` 影响的临时证书路径直接拼入 `pkexec sh -c`；macOS 虽做 shell 单引号转义，却又把结果嵌入 AppleScript 双引号源码，存在第二层解析逃逸。
- 产品决策：Windows 保持 SEC-01 的应用生命周期临时 Helper；Linux/macOS 暂不引入会话级常驻 root Helper，继续使用各平台原生的逐次授权流程。
- 安全决策：Linux 完全删除动态 shell；macOS 的 AppleScript 源码保持常量，所有路径和标识通过 argv 传入并在 AppleScript 内引用。
- 范围决策：本项只修复 Unix 特权执行链和参数约束，不把 Windows 的 CurrentUser Root 模型未经验证地强推到 Linux/macOS；这些平台仍按各自已确认/后续交付的信任范围实现。

#### 高层特权 Adapter

删除通用的 `elevatedCopy(src, dst)` 形态。它把任意源/目标文件操作暴露给调用者，Interface 与底层实现几乎等宽，容易在新增调用点时扩大 root 能力。

Unix Adapter 只暴露产品需要的高层操作，例如：

```go
type PlatformPrivilege interface {
    ApplyManagedHosts(ctx context.Context, content []byte) error
    RemoveManagedHosts(ctx context.Context) error
    TrustLocalCA(ctx context.Context, certDER []byte) error
    UntrustLocalCA(ctx context.Context, fingerprint string) error
}
```

目标路径、命令、最大输入、证书名称和回滚步骤都封装在 Adapter 内；业务调用方不能提供任意命令、可执行文件或目标路径。若某平台最终不需要系统级 CA 操作，则该平台 Adapter 应删除对应能力，而不是保留无调用的提权入口。

#### Linux 执行规则

- 完全禁止 `pkexec sh -c`、`bash -c` 或拼接复合命令字符串。
- 每一步使用固定绝对程序和独立 argv，概念上类似：

```text
/usr/bin/pkexec /bin/cp -- <source> <固定目标>
/usr/bin/pkexec /usr/sbin/update-ca-certificates
/usr/bin/pkexec /bin/rm -f -- <固定目标>
```

- 实际路径应按支持发行版建立小型 allowlist/resolver；解析后的文件必须是绝对路径、root 所有，且普通用户和组不可写。不能盲信应用继承的 `PATH`。
- `--` 必须放在可接受它的程序参数中，防止以 `-` 开头的路径被解析为选项。
- 复制、刷新信任库和失败补偿由 Go 顺序编排。复制成功但刷新失败时，尝试删除本次写入文件并再次刷新；原错误和补偿错误一起返回。
- 授权取消属于明确的可识别错误，不得重试弹窗循环或报告成功。

Linux 多步操作可能根据桌面 Polkit 策略多次要求授权；当前不承诺整个应用生命周期复用授权。若未来有明确 UX 需求，应单独设计发行版原生、root 所有的最小 Helper，不能从用户可写 AppImage/工作区直接 `pkexec` 执行。

#### macOS 执行规则

- `/usr/bin/osascript` 接收固定 AppleScript 程序；不得用 `fmt.Sprintf`、字符串拼接或模板把路径/指纹写入源码。
- 动态值通过 `osascript ... -- <arg1> <arg2>` 传入 `on run argv`。
- AppleScript 内对每个动态参数使用 `quoted form of item n of argv`，再与固定的绝对命令路径组合。
- shell 命令固定使用 `/bin/cp`、`/bin/rm`、`/usr/bin/security` 等受系统保护路径，不依赖 PATH。
- 指纹仍须先按 64 位小写十六进制校验；即便内容受限，也必须作为 argv 处理，不能直接插入 AppleScript。
- 授权取消、钥匙串被锁定或系统策略拒绝时返回结构化错误，临时文件和中间状态必须清理。

这里允许 macOS `do shell script ... with administrator privileges`，但 shell 接收到的只能是“固定绝对程序 + AppleScript `quoted form` 生成的参数”。安全关键点是动态数据不参与 AppleScript 源码解析，也不作为未引用的 shell 片段。

#### 临时文件与输入限制

- Adapter 自己创建权限最小的私有临时目录和文件，不接受调用方给出的任意临时路径。
- 提权前使用 Lstat/平台等价机制确认源是普通文件而非符号链接，大小不超过业务预算。
- 写完后 flush、关闭并计算摘要；提权步骤只处理本次 Adapter 创建的文件。
- 目标 hosts、CA 目录、证书文件名和钥匙串路径均由平台 Adapter 固定。
- 成功、失败和授权取消后清理临时文件；日志不输出证书内容、完整临时路径或未经清理的命令行。

使用标准 `cp` 的 MVP 路径仍存在本地同用户进程抢占临时文件的理论窗口，因此私有目录权限和文件身份复核必须同时存在。若后续威胁模型要求隔离同一用户下的不可信进程，则需升级为 root 所有的专用 Helper 直接接收有界内容，不能继续依赖路径传递。

#### 预期代码范围

- `internal/helper/local_linux.go`：删除 `fmt.Sprintf` 与 `pkexec sh -c`，实现固定程序/argv 和补偿编排。
- `internal/helper/local_darwin.go`：删除动态 AppleScript 源码和 `shellQuote`，改为常量脚本 + argv + `quoted form`。
- `internal/helper/local_unix.go`：把通用复制调用收敛为高层操作，统一临时文件和输入限制。
- 新增可注入 command runner 的内部测试 seam，用于精确断言 executable、argv 和调用顺序；生产仍只有一个平台 Adapter。

#### 验收标准

- `TMPDIR`、临时路径或测试输入包含空格、分号、单双引号、`$()`、反引号、反斜杠和换行时，command runner 捕获的 argv 与原始值逐字节一致。
- 仓库特权路径中不存在 `pkexec sh -c`、`bash -c`，也不存在动态值拼入 AppleScript 源码。
- 恶意路径测试不能产生标记文件、额外子进程或额外参数。
- 可执行文件解析到相对路径、用户可写文件或 allowlist 外路径时，在提权前拒绝。
- 复制成功、刷新失败时执行预期补偿；补偿失败返回包含两个阶段的复合错误。
- 用户取消授权不会循环重试，不残留临时文件，不修改成功状态。
- Linux/macOS 真机分别完成 hosts 应用/撤销、CA 信任/撤销和重复撤销回归；未完成真机验证的平台不得在 REL-01 中标记为已交付。

#### 后续关联

- `SEC-01` 继续只定义 Windows 会话级临时 Helper；Unix 不注册常驻 root 进程。
- `REL-01` 必须按平台运行 argv 注入负向测试和真机特权 smoke，不能以交叉编译成功代替。
- `ROUTE-02` 的事务协调调用高层 PlatformPrivilege Interface，不直接编排 `cp/security/update-ca-certificates`。

### 4.9 REL-01：以 GitHub Actions 作为唯一正式构建者并验证最终安装产物

#### 状态与结论

- 状态：已实现并完成本地 bundle/manifest/verifier 验证；正式签名与 GitHub Actions 运行待远端执行
- 确认日期：2026-07-21
- 问题结论：成立。当前 Windows Actions 只运行 `wails build` 并上传 `build/bin/*.exe`，不会包含 `caddy/caddy.exe`；macOS DMG 没有内置 Caddy；现有 Windows 打包和 smoke 又仍依赖旧 SCM Helper 与工作区 Caddy。
- 构建决策：正式 Release 产物只能由 GitHub Actions 生成。本地可以调用相同脚本复现，但本地产物不具备正式发布来源身份。
- 发布决策：Tag 通过自动门禁后创建 draft Release；下载该 draft 的真实 Windows 安装包完成一次交互式 UAC smoke 后，才转为正式 Release。
- 平台决策：Windows x64 是当前首要正式目标；macOS amd64/arm64 只有完整打包和真机特权 smoke 通过后才附加；Linux 按 handoff 明确后置，不作为当前 MVP Release 阻断项，也不得宣称已支持。

#### GitHub Actions 工作流

正式流水线拆为以下依赖图：

```text
quality-gate
  ├─ build-windows
  └─ build-macos
        ↓
verify-artifacts
        ↓
draft-release（仅 v* Tag）
        ↓
真实 Windows UAC smoke
        ↓
人工确认发布 draft
```

要求：

- `quality-gate` 运行 `go test ./...`、`go vet ./...` 和 `pnpm build`。
- Windows/macOS build job 分别在 GitHub 托管的原生 runner 上执行，不交叉编译特权 Adapter。
- release job 只下载本次 workflow 已验证的 artifacts，禁止重新 checkout 后再次构建。
- 非 Tag 分支只上传短期 CI artifacts；`v*` Tag 才允许创建 draft Release。
- build job 只授予 `contents: read`；只有 draft-release job 获得 `contents: write`。
- Actions 依赖固定到审核过的版本/提交，并启用依赖缓存时避免缓存最终可执行产物覆盖当前源码构建结果。

#### 唯一打包 Module

仓库提供一个跨平台编排入口，例如：

```text
uv run scripts/release.py build --target windows-amd64
uv run scripts/release.py build --target darwin-universal
uv run scripts/release.py verify --artifact <path>
```

它是 GitHub Actions 调用的打包 Module，不是要求用户本机手工发版。其职责包括：

- 校验干净的 staging 目录，禁止复用旧 `build/bin` 残留；
- 构建前端、主程序和平台 Helper；
- 按目标平台/架构下载并校验钉版 Caddy；
- 组装安装目录/App bundle；
- 调用平台签名和安装包工具；
- 生成 manifest/checksums；
- 解包最终 artifact 并运行无特权 self-check。

GitHub Actions YAML 只负责环境、权限、并行 job 和 artifact 传递，不复制这些平台打包规则。CI 与本地复现共用同一个 Interface，修复一次即可同时影响两处。

#### Caddy 供应链清单

把当前只支持 `windows_amd64` 的 `fetch-caddy.py` 和散落哈希替换为单一版本清单，按 `{version, os, arch}` 记录：

- 上游下载地址；
- 归档文件 SHA-256；
- 解包后二进制 SHA-256；
- 归档内预期文件名；
- 许可证/NOTICE 来源。

下载必须先限制响应大小，再验证归档摘要和目标成员，禁止路径穿越和模糊选择首个文件。运行时校验值从同一清单生成或注入，不能让 Python、Go 和 workflow 分别维护三个常量。

Caddy 保持上游钉版原始字节；Windows 主程序、会话 Helper 和安装器使用 TunnelBoard Authenticode 签名。manifest 同时记录上游 Caddy 摘要和最终发行文件摘要。

#### Windows 正式产物

正式 Windows 只发布签名安装器，安装布局至少为：

```text
%ProgramFiles%\TunnelBoard\
  TunnelBoard.exe
  tunnelboard-helper.exe
  caddy\caddy.exe
  manifest.json
  LICENSES\...
```

不变量：

- 安装目录继承 Program Files 的保护 ACL；普通 Users 无写入、删除、改名和替换权限。
- 主程序与 `--session-helper` 使用同一可信 Authenticode 发布者；UAC 前后都验证签名和协议版本。
- Helper 不包含 `-install/-serve` 正常模式，不注册 SCM、不设置开机启动。
- Caddy 固定从受保护安装目录定位；用户级 runtime/PKI/log 仍位于 `%LocalAppData%`，不写 Program Files。
- 正式渠道不发布可从 Downloads、解压目录等用户可写位置启动提权 Helper 的 portable 包。
- 如果未来提供 portable 包，其 hosts 特权能力必须禁用；不能把签名校验当成用户可写路径 TOCTOU 的完整替代。

签名顺序为：验证上游输入 → 构建 → 签主程序和 Helper → 验签 → 生成安装器 → 签安装器 → 再验签。缺少签名 secret、证书过期、时间戳失败或发布者不一致时，Tag job 失败且不得创建可发布 Release。

#### macOS 正式产物

- universal App 同时携带 darwin/amd64 和 darwin/arm64 的钉版 Caddy，运行时按当前架构选择，或在打包阶段生成经过验证的 universal Caddy。
- Caddy 位于 App bundle 的受控 Resources/Helper 位置，runtime 只从 bundle 和明确开发 override 定位。
- 先签嵌套可执行文件，再签整个 App，最后制作并验证 DMG。
- 按既有产品约束使用 ad-hoc 签名和首次手动放行模式，不宣称已 notarize 或拥有 Apple Developer 发布者身份。
- macOS hosts、CA、Caddy、托盘和退出清理未在真实 amd64/arm64 环境完成验收时，不把 DMG附加到正式 Release；不能为了不阻塞 Windows 而发布已知残缺包。

#### Artifact manifest

每个平台产物内包含机器可读 manifest，至少记录：

- 产品版本、Git commit、构建 workflow/run ID；
- 目标 OS、架构与最低支持系统；
- 主程序、Helper、Caddy、许可证文件的相对路径、角色、大小和 SHA-256；
- Caddy 上游版本及原始摘要；
- Go、Wails、Node、pnpm、uv 版本；
- 主程序/Helper/安装器的签名状态、发布者和 Helper 协议版本。

verify 命令只接受 manifest 中声明的文件集合。必需文件缺失、内容被替换、出现未声明可执行文件、签名不符或目标架构错误时必须失败。

#### 验证最终 artifact

所有检查针对 Actions 刚生成并重新下载/解包的 artifact，不能通过以下方式借用工作区状态：

- 不设置 `TUNNELBOARD_CADDY_PATH` 指向 `build/caddy`；
- 不从 Actions workspace 的 `build/bin` 补文件；
- 不依赖上一次缓存或 runner 预装副本；
- release job 不重新生成 manifest/checksums。

自动验证至少包括：

- manifest 文件集合、SHA、架构和版本完全匹配；
- Caddy 可从安装布局定位、校验并在非特权测试端口启动；
- 正式钉版 Caddy 的 AF_UNIX Admin socket 完成 `/load`、`/stop` POC，且不监听 TCP 2019；
- Helper `--self-check`/协议握手通过且运行后不产生 `TunnelBoardHelper` 服务；
- Windows 安装器静默安装到隔离测试路径后，ACL、卸载和旧服务迁移检查通过；
- macOS bundle 的嵌套二进制、架构和签名结构通过验证。

GitHub 托管 Windows runner 无法真实复现交互式桌面 UAC 点击，因此这部分不能伪装成自动验收。Tag 自动创建 draft 后，使用下载的签名安装器在真实 Windows 桌面执行：首次 hosts 操作弹一次 UAC、同应用生命周期复用、退出后 Helper 消失、重新启动后再次授权、CurrentUser CA 和 AF_UNIX Caddy 闭环。证据通过后再发布 draft。

#### 精确 artifact 输出

Actions 只上传精确命名的文件，例如：

```text
TunnelBoard-<version>-windows-x64-setup.exe
TunnelBoard-<version>-windows-x64.manifest.json
TunnelBoard-<version>-macos-universal.dmg
TunnelBoard-<version>-macos-universal.manifest.json
SHA256SUMS
```

删除 `build/bin/*.exe` 和 `artifacts/**/*` 这类宽泛上传规则，避免把旧 helper、selfcheck、调试二进制或其他残留文件带入 Release。

#### 预期代码范围

- 重写 `.github/workflows/build.yml` 为 quality/build/verify/draft-release 依赖图。
- 新增统一 `scripts/release.py` 或等价打包 Module，替换 CI 直接裸跑 Wails。
- 将 `scripts/fetch-caddy.py` 泛化为受清单驱动的目标下载器。
- 重写 `scripts/package-windows.py` 和 `scripts/smoke-windows.py`，删除 SCM 服务模型和工作区 Caddy override。
- 增加 Windows 安装器配置、签名/验签步骤、manifest schema 和 artifact verifier。
- 增加 macOS Caddy bundle、架构选择、签名顺序和 DMG verifier。

#### 验收标准

- 正式 Release 附件全部可追溯到同一次 GitHub Actions run，本地构建文件无法混入。
- 从干净 checkout 构建；预先放置错误 `build/bin` 文件不会被上传或改变产物。
- 删除 Helper/Caddy、替换任一字节、篡改 manifest、加入额外可执行文件或使用错误架构时 verify job 失败。
- Windows 安装后主程序、Helper、Caddy 位于受保护目录，标准用户无法替换；用户数据只写 CurrentUser 范围。
- Windows 不存在持久 `TunnelBoardHelper` 服务，Helper 仅在一次应用生命周期内存在。
- artifact 内 Caddy 的 AF_UNIX POC 和无 TCP 2019 检查通过。
- Tag 缺少有效签名或自动门禁失败时不创建可发布 Release；成功时只创建 draft。
- draft 下载物完成真实 UAC smoke 后才转正式；测试使用下载附件而非仓库工作区二进制。
- Linux 未交付状态在 README、Release notes 和支持矩阵中明确，不上传占位或仅编译通过的 Linux artifact。

#### 后续关联

- `SEC-01` 的签名、临时 Helper、旧服务迁移和退出清理由最终安装包验证。
- `SEC-02` 的 CurrentUser CA 与每用户 LocalAppData 必须在 Program Files 安装后仍成立。
- `SEC-03`/`ROUTE-01` 的 AF_UNIX Admin socket POC 是 Caddy 钉版升级的强制门禁。
- `SEC-04` 的 macOS/Linux argv 安全测试进入各自平台 job；Linux 真机门禁在正式启用 Linux target 时生效。
- `PERF-01` 的日志预算和轮转应包含在安装后长时间 smoke 中。

### 4.10 SSH-03：按连接身份换代，并在 Host 连接字段变化时重启受影响 Forward

#### 状态与结论

- 状态：已实现并通过换代与交错测试验证
- 确认日期：2026-07-21
- 问题结论：成立。SSHConnPool 当前只按 `SSHHost.ID` 查找 entry；Vault 更新地址、用户、认证或凭据后，新 Acquire 仍可能复用旧连接。与此同时，运行中的 LocalForward 保存的是 Start 时解析出的旧 Host 快照，重连也可能继续拨旧目标。
- 产品决策：连接池复用必须同时匹配 Host ID 和不含明文秘密的 ConnectionIdentity。
- 交互决策：展示字段可以直接保存；任何连接字段变化且存在受影响的运行中 Forward 时，必须预检新配置并明确确认重启全部受影响项。不提供“Vault 显示新配置，但运行中 Forward 静默继续旧连接”的保存模式。

#### ConnectionIdentity

ConnectionIdentity 由规范化后的连接事实组成，至少包含：

- Host ID；
- 规范化 Host、Port、User；
- AuthType、KeyPath、AgentSocketPath；
- HostKeyAlgorithms；
- TimeoutMs、KeepAliveIntervalMs；
- 内部 CredentialRevision。

Name、Notes 等纯展示字段不属于连接身份。Password/私钥口令不直接进入持久摘要、日志或 PoolStats；SEC-06 已确认 Wails 只接受单向 `SaveSSHHostCommand`，通过 `secretAction=keep|replace|clear` 管理秘密，replace/clear 时递增 CredentialRevision。

ConnectionIdentity 可以使用规范化结构的稳定摘要表示，但摘要输入不得含明文秘密。CredentialRevision 存在于加密 Vault 的内部模型中，不作为前端可编辑字段；旧 Vault 缺省值在首次相关保存时安全升级。

外部私钥文件或 SSH Agent 内容变化不会被文件监听自动发现。用户需要通过明确的“重新连接”操作强制新 connection generation；不能为追踪外部文件而把私钥内容摘要暴露到界面或日志。

#### Pool 换代模型

连接池以 `(hostID, connectionIdentity)` 选择 entry，并保留 SSH-01 已确认的 connectionGeneration：

- identity 完全相同才允许复用当前连接代。
- identity 变化时创建新的 entry/generation，不返回旧 client。
- 旧 lease 继续持有旧 entry 指针；引用归零后关闭并回收。
- 旧代 keepalive、watcher、release 和 SSH-02 的 AbortGeneration 只作用于捕获的旧 client/generation。
- PoolStats 可以按 Host ID 展示 current/retiring generation 与引用数，但不得暴露凭据版本、密码摘要或其他秘密。
- 临时 `Host.ID==0` 的连接测试永不进入共享池。

即使上层遗漏显式 Invalidate，新的完整 identity 也不能命中旧连接。Invalidate/Retire 只用于加速回收和显式“重新连接”，不能成为正确性的唯一保障。

#### 连接字段分类

以下变化要求新连接身份：

- Host、Port、User；
- AuthType；
- Password/私钥口令的 replace 或 clear；
- KeyPath、AgentSocketPath；
- HostKeyAlgorithms；
- TimeoutMs、KeepAliveIntervalMs。

以下变化不要求重启：

- Name；
- Notes；
- 未来明确标记为纯展示的字段。

字段分类由后端单一函数维护并测试，前端不能自行猜测是否需要重启。

#### 预览与确认 Interface

编辑请求先进入后端预览：

```go
type HostUpdatePreview struct {
    NormalizedHost      SSHHostView
    ConnectionChanged  bool
    AffectedForwardIDs []int
    RunningForwardIDs  []int
    RequiresRestart    bool
    Revision           string
}
```

受影响 Forward 必须扫描整个 `ChainHostIDs`，不只检查首跳；中间跳或末跳配置变化同样需要重建完整尾链。

纯展示变化或没有运行中引用时可直接提交。连接字段变化且存在运行中引用时，后端返回 `HostChangeRequiresRestart`；UI 展示受影响数量/名称和短暂中断说明，默认动作是“保存并重启”，取消则零副作用。

Preview 的 revision 绑定旧 Host 内容和受影响 Runtime generation。用户确认期间 Host 或运行集合变化时，Commit 必须拒绝过期 preview 并要求重新预览，不能按旧列表操作。

#### 保存并重启流程

在应用 mutation lock 内执行：

1. 重新验证 preview revision、当前 Host 和受影响 Forward generation。
2. 使用新配置建立不入池的临时 SSH 连接，完成地址、认证和主机指纹预检；新 `(Host, Port)` 使用自己的指纹记录。
3. 预检成功后，记录所有原本 running/reconnecting 的 Forward ID 与 generation。
4. 使用 SSH-02 的同一 5 秒全局 context 并行停止这些 generation。
5. 全部停止成功后，原子保存规范化 Host；秘密 replace/clear 时递增 CredentialRevision。
6. 将旧 pool identity 标记 retiring；正常情况下引用已归零，残留按精确 generation 处理。
7. 通过 RUN-01 的 Start 为原运行集合创建新 generation。
8. 返回逐项 `{stopped, saved, restarted, error}` 结果，并刷新 Vault、Runtime 和 PoolStats。

#### 失败与补偿

- 新配置预检、认证或指纹确认失败：不停止旧 Forward、不写 Vault、不改变 pool。
- 任一 Forward 停止失败：不保存 Host；尝试以旧配置恢复本次已经停止成功的 Forward，并返回原始错误与补偿结果。
- 全部停止后 Vault 保存失败：旧 Host 保持不变，按旧配置重启原运行集合。
- Host 保存成功但部分新代启动失败：保留新 Host，成功项继续运行，失败项显示 error 并提供批量重试；不能回滚 Host 后让已成功项继续新配置。
- 补偿本身失败时返回结构化复合错误，界面必须列出每条 Forward 的真实状态。
- 流程中任何旧 watcher、旧 pool 探活或迟到 Stop 都受 RUN-01/SSH-01 generation 校验，不能覆盖新实例。

#### 明确不采用

- 不采用“编辑后 `CloseAll()`”：它会无差别中断不相关 SSH Host 的 Forward。
- 不采用只按 Host ID 调用 `InvalidateHost` 而不改变 pool key：并发 Acquire 仍可能命中旧 entry。
- 不采用密码明文哈希作为可持久、可显示 identity。
- 不允许默认“仅影响未来连接”，因为 Vault/界面与真实流量目标会产生长期分裂。

#### 预期代码范围

- `internal/model/vault.go`：增加内部 CredentialRevision 或等价秘密版本字段，并提供迁移默认值。
- `internal/biz/catalog.go`：集中连接字段分类、规范化、secretAction 合并和 revision 递增。
- `internal/forward/conn_pool.go`：按 ConnectionIdentity + generation 管理 current/retiring entries。
- `internal/biz/runtime.go`：查找所有链路引用、预览 generation、批量停止/重启编排。
- `app.go`/后续应用 Module：暴露有类型 PreviewHostUpdate/CommitHostUpdate，而不是继续直接透传 SaveSSHHost。
- Hosts 页面：展示受影响 Forward、预检结果和重启逐项摘要。

#### 验收标准

- 旧连接指向 A，保存同 ID Host 为 B 后，任何新 Acquire 都拨 B，绝不返回 A client。
- 只修改 Name/Notes 时 identity 不变，不中断运行中的 Forward。
- 修改地址、端口、用户、认证类型、密码、口令、KeyPath、AgentSocket、算法、timeout 或 keepalive 时 identity 必变。
- Password replace/clear 递增 CredentialRevision；keep 不递增，且响应/日志中不存在秘密或其可离线猜测摘要。
- 旧 lease 未释放时新旧 pool entry 可并存；最后一个旧 lease 释放后旧 client 关闭，新 client 不受影响。
- 中间跳和末跳 Host 修改也列出全部受影响 Forward。
- 新配置预检失败保持旧 Vault、旧运行集合和旧 pool 完全不变。
- 停止或保存失败时旧运行集合按补偿规则恢复；新代部分启动失败时逐项状态真实可见。
- preview 后 Host/Runtime generation 变化，旧确认 token 被拒绝。
- 多条受影响 Forward 的停止共享 SSH-02 全局 deadline，重启使用 RUN-01 新 generation。
- 换代、keepalive、release、Abort、保存与重启交错在 `go test -race ./internal/forward ./internal/biz` 下通过。

#### 后续关联

- `SEC-06` 已确认提供 `secretAction=keep|replace|clear`、单向 SecretInput 和不含秘密的 SSHHostView，本项不再接受完整 SSHHost 往返 WebView。
- `SSH-01`、`SSH-02` 的 connectionGeneration 是 pool 换代和精确 Abort 的共同事实来源。
- `SSH-04` 的多跳尾链 lease 必须携带每一跳 identity，编辑中间/末跳时才能精确识别影响。
- `ARCH-01` 的应用 Module 需要把预览、预检、停止、保存、退役和重启隐藏在一个有类型命令后。

### 4.11 DATA-01：以 Stage/Commit 恢复事务和持久隔离态消除预检副作用

#### 状态与结论

- 状态：已实现并通过故障注入、崩溃窗口与门禁交错测试验证；真实断电测试保留为发布门禁
- 确认日期：2026-07-21
- 问题结论：成立。当前 `RestoreBackup` 在确认口令、解密、schema 和引用完整性之前就调用 `runtime.Shutdown()`；错误密码、损坏文件或用户未确认也会中断全部 Forward。成功替换 Vault 后，又没有原子清理旧 managed hosts、Caddy 和 CA 实际状态。
- 产品决策：完全还原拆成零副作用的 `StageRestore` 与受事务保护的 `CommitRestore`。恢复配置成功后进入持久的“恢复隔离态”，所有 Forward、managed hosts、Caddy 和当前用户 CA 均保持未激活，只有用户再次明确确认才应用恢复出的网络配置。
- 生命周期决策：完全还原不能调用永久 `Shutdown`。RuntimeBiz 提供可恢复的 `SuspendAll(ctx)` 和内部恢复计划；永久 `Shutdown` 只用于应用退出。

恢复的目标是恢复“期望配置”，不是把备份来源机器的运行事实复制到当前机器。尤其是 CA 已信任、Caddy 正在运行、Forward 正在连接等事实都与当前 Windows 用户和当前机器相关，不能因为备份字段存在就自动生效。

#### StageRestore Interface

应用层提供有类型的预检 Interface，例如：

```go
type RestoreStageRequest struct {
    BackupPath string
    Password   SecretInput
}

type RestorePreview struct {
    Token                   string
    ExpiresAt               time.Time
    EntityCounts            RestoreEntityCounts
    CurrentRunningForwards  []ForwardSummary
    CurrentRouteEffects     RouteEffectSummary
    RestoredDesiredSettings RestoredNetworkIntent
    Warnings                []RestoreWarning
}
```

`StageRestore` 在不修改外部状态的前提下完成：

1. 使用 SEC-05 后续确认的统一文件、KDF、实体数、字符串和私钥资源预算进行有界读取。
2. 校验备份头、版本、KDF 参数和完整性，再执行解密。
3. 完成 schema 迁移、字段合法性、ID 唯一性和 Host/Forward/Route 引用完整性校验。
4. 生成只含非敏感摘要的预览：实体数量、当前运行集合、当前 Route 副作用、恢复后期望的 AutoStart/hosts/Caddy 设置，以及缺失外部密钥文件等警告。
5. 将解密后的 staged 数据只保存在后端内存，返回高熵、单次使用、短时有效并绑定当前应用 generation 的 token。过期、应用重启或再次 Stage 后立即失效。
6. Password、私钥口令、完整 SSHHost 和解密后的 Vault 不得进入 WebView、日志、诊断包或 token；完成、过期或取消时尽力清理内存中的 staged secret。

Stage 阶段严格禁止调用 Runtime、Helper、CaddySupervisor、证书库或 Vault 写入 Adapter。用户取消、密码错误、备份损坏和预览失败都必须是零副作用。

#### CommitRestore Interface 与并发门禁

`CommitRestore(token)` 在应用级 mutation lock 内执行。这个 lock 是备份恢复、Host 保存并重启、Route reconcile 等跨 Module 变更的统一 seam；前端不能自行按顺序拼接多个细粒度调用。

提交前必须重新验证：

- token 未过期、未使用且属于当前应用 generation；
- staged 文件摘要和 staged 内容未变化；
- 当前 Vault revision、Runtime generation 集合和 Route applied revision 仍与 Stage 时一致；
- 用户已经在预览界面明确确认“恢复后不会自动联网，需再次激活”。

任一事实变化都返回 `ErrRestorePreviewStale` 并要求重新预览，不能把旧确认应用到新状态。mutation lock 持有期间，Start、AutoStart、Host 更新、Route Apply 和其他 Vault mutation 必须等待或返回明确的 busy 错误。

#### 可恢复的 Runtime 暂停

RuntimeBiz 增加恢复专用的深 Module Interface：

```go
type RuntimeSuspendPlan struct {
    Entries []SuspendedForward
}

func (b *RuntimeBiz) SuspendAll(ctx context.Context) (RuntimeSuspendPlan, error)
func (b *RuntimeBiz) Resume(ctx context.Context, plan RuntimeSuspendPlan) ResumeResult
```

- `SuspendAll` 快照所有 running/reconnecting generation，复用 SSH-02 的并行 `Stop(ctx)` 和单一 5 秒总 deadline。
- 暂停期间由外层 mutation lock 禁止新 Start；RuntimeBiz 本身不进入永久 `closing`。
- 任一实例未能可靠停止时，Commit 不替换 Vault；按原配置恢复已经停止成功的项，并返回逐项停止和补偿结果。
- `Resume` 只使用后端捕获的计划，不接受前端传回 Forward ID 列表；新启动仍通过 RUN-01 分配新 generation。
- 应用退出继续调用永久 `Shutdown`，设置 `closing=true` 后不可 Resume。

#### 提交事务顺序

在通过所有提交前置校验后，固定执行：

1. 写入持久 restore journal，记录 transaction ID、旧 Vault revision、staged 摘要、旧 Route applied revision、旧运行计划和当前阶段。
2. 在同一受保护数据目录生成并校验新的加密 Vault candidate，但暂不替换正式文件。
3. 调用 `SuspendAll(ctx)`，确保旧运行集合在总 deadline 内停止。
4. 通过 ROUTE-02 的 reconcile coordinator 把旧系统副作用收敛到安全中性态：停止自有 Caddy、删除 TunnelBoard managed hosts 区块、撤销当前用户中由本应用登记的 CA，并清除旧 applied 状态。
5. 原子替换正式 Vault；恢复备份中的期望配置字段，但清除所有机器本地实际状态字段。
6. 清空旧 SSH pool entries，并写入持久 `restoreQuarantine=true`；不得根据恢复出的 AutoStart 或 Route enabled 状态启动任何网络行为。
7. 提交 journal，刷新只读快照并消费 staged token。

现有 `CATrustedSHA256` 表示当前用户、当前机器证书库中的登记状态，不属于可移植配置。ROUTE-02 已确认将它迁出 Vault，放入每用户本机 RouteAppliedState；完全还原不读取备份中的旧值，并在用户以后显式激活 Route 时重新生成或核验当前用户 CA。

#### 恢复隔离态与交互

备份中的 `AutoStart`、Route enabled、hosts enabled 和 Caddy enabled 仍作为期望配置保留，但 `restoreQuarantine` 具有更高优先级：

- Commit 完成后所有 Forward 显示 stopped，managed hosts 不存在，Caddy 不运行，CA 不被信任。
- 应用重启后隔离态仍然存在，启动流程不得执行 AutoStart 或 Route reconcile。
- Overview 和相关页面显示明确横幅，列出将要启动的 Forward 数量、将写入的域名和需要当前用户 CA 信任的 Route。
- 用户点击“应用恢复的网络配置”后再次确认；后端在一个有类型命令中按当前配置重新预检端口、SSH Host、hosts、Caddy 与 CurrentUser CA，再执行激活。
- 激活全部成功后才清除 `restoreQuarantine`。若只允许部分成功，必须保留隔离态并逐项展示真实结果，不得因部分副作用成功就静默解除门禁。
- 用户也可以选择“保持配置但不激活”，以后手动逐条启动；在明确放弃整批恢复激活时清除隔离态，但不能自动补做任何网络副作用。

#### 失败、补偿与崩溃恢复

- Stage 失败：零 Runtime、Vault、hosts、Caddy 和 CA 副作用。
- `SuspendAll` 失败：正式 Vault 不变；恢复本次已停止的旧运行项。
- 中性态 reconcile 失败：正式 Vault 不变；尽力恢复旧 Route applied 状态和旧运行计划。
- Vault 原子替换失败：恢复旧 Vault 文件，并按 journal 恢复旧 Route 与 Runtime。
- 新 Vault 已替换后崩溃：下次启动读取 journal，优先确保旧网络副作用被清理，并以新 Vault + `restoreQuarantine=true` 收敛；绝不自动启动恢复配置。
- 任一补偿失败：保留可恢复的 pending journal，返回包含原始错误和逐项补偿结果的结构化复合错误；界面显示需要人工重试的真实状态。
- journal 只保存恢复所需的 revision、ID、摘要和阶段，不保存密码、明文 Vault 或私钥内容。

#### 预期代码范围

- `internal/biz/backup.go`：Stage/Commit、staged token store、restore journal、隔离态和补偿编排。
- `internal/biz/runtime.go`：可恢复的 `SuspendAll/Resume`；永久 `Shutdown` 保持独立语义。
- `internal/biz/router.go`：提供收敛到中性态和按 revision 激活的 Interface，具体事务与 ROUTE-02 对齐。
- `internal/model/vault.go`：区分可移植期望配置与机器本地实际状态；`CATrustedSHA256` 不参与还原。
- `app.go`/后续应用 Module：只暴露有类型 `StageRestore/CommitRestore/ActivateRestoredNetwork`，移除前端对恢复步骤的拼装。
- Settings/Overview：预览、二次确认、持久隔离横幅、激活摘要和补偿错误展示。

#### 验收标准

- 未确认、错误密码、损坏备份、非法 schema、非法引用、过期 token 和 stale preview 均不得停止 Forward、写 Vault、调用 Helper 或改变 Caddy/CA。
- Stage 响应、Wails 事件、日志和诊断包中搜索不到测试密码、私钥口令或解密 Vault 内容。
- Commit 时任一 Runtime generation、Vault revision 或 Route revision 变化，旧 token 被拒绝且零副作用。
- 完全还原成功后，即使备份内 AutoStart 和 Route enabled 均为 true，也没有监听端口、managed hosts、Caddy 进程或受信任 CA。
- 应用在新 Vault 替换后的各 journal 阶段崩溃并重启，最终都收敛到新配置 + 隔离态 + 无旧网络副作用。
- `SuspendAll` 失败、Route 清理失败和 Vault 替换失败均能恢复旧 Vault/旧运行集合/旧 Route，补偿失败时保留可重试 journal。
- 隔离态跨应用重启保持；只有显式激活全部成功或用户明确放弃整批激活后才清除。
- 完全还原不会恢复其他机器或其他 Windows 用户的 `CATrustedSHA256`；激活时只在当前用户上下文重新建立 CA 信任。

#### 后续关联

- `RUN-01` 已同步区分永久 `Shutdown` 与可恢复 `SuspendAll`；还原期间的并发门禁由应用 mutation lock 提供。
- `SSH-02` 的 Stop Interface、精确连接代 Abort 和 5 秒全局预算由 `SuspendAll` 直接复用。
- `SEC-05` 已确认为 StageRestore、StageImport、私钥提取和导出提供统一 BackupPackage Module 与资源预算，恢复不能另建解析入口。
- `SEC-06` 需要保证 RestorePreview 和 staged token 不把任何秘密送入 WebView。
- `ROUTE-02` 需要让“清理旧副作用”和“显式激活恢复配置”都走同一 reconcile coordinator 与 journal 代际规则。
- `ARCH-01` 应把 Stage、Commit 和 Activate 作为应用 Module 的三个有类型命令，而不是暴露内部 Adapter。

### 4.12 SSH-04：首跳池级探活、末跳端到端探活并局部重建尾链

#### 状态与结论

- 状态：已实现并通过多跳、探活与停止生命周期测试验证
- 确认日期：2026-07-21
- 问题结论：成立。当前多跳链只因为首跳来自连接池就返回 `shared=true`，Forward 随后完全跳过自身 keepalive；当 H1 正常而 H2、H3 或其间网络黑洞时，池级首跳探活仍成功，末跳 `client.Wait()` 又可能长期不返回，界面会继续显示 running。
- 产品决策：单跳链只由 SSHConnPool 探活；多跳链由 SSHConnPool 探活 H1，同时由当前 Forward 对末跳 Hn 做端到端有界探活。末跳失效且首跳健康时只重建该 Forward 独占的 H2～Hn 尾链。
- 术语约定：一条 Forward 只有一条有序 SSH 主机链；“多条链共享首跳”是指多个 Forward 各自的链共同复用同一 H1 connection generation。

例如：

```text
Forward A：本机 → H1 → H2 → 目标服务 A
Forward B：本机 → H1 → H3 → 目标服务 B
```

H1 失败会影响 A、B，但连接池只单飞重拨一次 H1；H2 失败只重建 A 的尾链，H3 失败只重建 B 的尾链。

#### 探活职责

- 单跳 `本机 → H1`：只运行 SSH-01 的池级 keepalive，Forward 不再重复发送相同探测。
- 多跳 `本机 → H1 → ... → Hn`：池级 keepalive 探测 H1；ChainLease 对 Hn 运行额外的端到端 keepalive。
- 末跳探测复用 SSH-01 的同一个有界、单飞 probe Module：interval 与 timeout 分离，同一 tail generation 同一时刻最多一个探测，timeout 后主动关闭该代尾链使阻塞调用退出。
- 不逐跳轮询 H2、H3。对 Hn 的 SSH global request 必须经过整个嵌套链，任一中间 transport 失效或黑洞都会使末跳探测失败；修复时重建当前全部尾链即可，不需要可靠判断具体坏在哪一跳。
- `WaitLoss(ctx)` 同时监听首跳 connection generation 的 Done、末跳 `client.Wait()`、末跳 probe 结果和 Forward run context，并以 once 只发布一次当前 chain generation 的失效。

#### ChainLease 与代际

删除 `shared bool`，由 SSH-02 已同步更新的 ChainLease Interface 隐藏以下实现事实：

- 捕获的首跳 pool entry、ConnectionIdentity 和 connectionGeneration；
- 当前 Forward 独占的 H2～Hn client/conn closers；
- 单调递增的 tailGeneration；
- 首跳失效通知、末跳探活取消、正常 Release 和精确 Abort；
- 基于当前所有权选择“只建尾链”或“重新 Acquire 首跳再建尾链”的 Rebuild。

LocalForward 只调用 `Terminal/WaitLoss/Rebuild/Release/AbortGeneration`，不读取首跳引用数、pool entry 或 `PooledPrefixLen`。这些细节属于 ChainLease Module 的实现；否则所有权判断会重新散落到 Local、Remote 和 Dynamic 三种 Forward。

每次尾链重建分配新的 tailGeneration。旧末跳 `Wait()`、keepalive timeout、closer 和重建结果落地前都必须验证 lease/tailGeneration 仍是当前代，不能关闭或覆盖后来成功的新尾链。

#### 失效与重建流程

末跳探活失败时：

1. 将当前 tailGeneration 标记 dead，并发布一次 disconnected。
2. 关闭本 Forward 的旧活动业务连接和 H2～Hn 尾链，使阻塞 channel 退出，但暂不释放 H1 lease。
3. 如果捕获的 H1 connectionGeneration 仍健康，直接通过 H1 重新建立 H2～Hn。
4. 若尾链建立表明 H1 已失效，或 pool 已关闭该 generation，则释放旧 lease，通过 SSHConnPool Acquire 当前 H1 generation，再建立完整尾链。
5. 对 Remote Forward，在新末跳上重新 bind remote listener；Local/Dynamic Forward 保留本地 listener，但旧业务连接必须结束，新连接只使用新末跳。
6. 成功后发布一次 reconnected；旧代的任何迟到事件不得再次改变状态。

H1 池级探活失败时：

1. SSHConnPool generation-safe 地标记 H1 dead 并关闭真实 transport。
2. 所有捕获该 generation 的 ChainLease 收到 Done；每个 Forward 进入自己的 reconnecting 状态。
3. 多个 Forward 并发 Rebuild 时由 SSHConnPool single-flight 只拨一次新 H1；随后各自重建独占尾链。

#### 重试和停止规则

- 网络中断、timeout 和临时连接拒绝使用现有指数退避，退避上限为一分钟。
- 认证失败、主机指纹变化或主机配置无效属于不可自动重试错误，Forward 落 error 并等待用户处理。
- 手动 Stop、DATA-01 SuspendAll 和永久 Shutdown 取消 probe 与 backoff，均不得在停止后重新建立尾链。
- Stop 继续遵循 SSH-02 的 5 秒总预算；正常路径只关闭本 Forward 的业务连接和尾链。只有不可取消操作超过兜底阈值时才允许 Abort 捕获的精确首跳 generation。

#### 明确不采用

- 不保留 `shared bool`：它无法同时表达“首跳共享、尾链独占、该探测谁负责、该关闭哪一代”。
- 不为每个 Forward 重复探测 H1：会按租户数放大请求，并在故障时产生重复结论。
- 不对每个中间跳分别启动周期探活：增加定时器和并发状态，却不能避免最终仍需重建整段尾链。
- 不在末跳失败时默认 CloseAll 或关闭健康 H1：单条尾链故障不应中断其他共享同一首跳的 Forward。
- 不在 H1 故障后让每条 Forward 各自直接拨 H1：必须经过池的 single-flight。

#### 预期代码范围

- `internal/forward/conn_pool.go`：首跳 generation Done、结构化 ChainLease、保留首跳的尾链 Rebuild 和 single-flight Acquire。
- `internal/forward/port_forward.go`：删除 `shared bool` 分支；生命周期监控改为 `lease.WaitLoss/Rebuild`，并为 Remote Forward 重绑 listener。
- `internal/forward/probe.go`：让池级首跳和 lease 末跳复用同一个有界 probe Module。
- `internal/forward/*_test.go`：增加可独立控制 H1、尾链 Dial、末跳 probe、旧代 Wait 和 Remote bind 的 fake Adapter。
- RuntimeBiz 不判断具体链路哪一跳失败，只接收当前 Forward generation 的 disconnected/reconnected/error 事件。

#### 验收标准

- 单跳 Forward 只存在一个池级 probe，不出现 Forward 重复探测。
- H1 正常、H2 黑洞时，末跳 probe 在 timeout 内失败，Forward 进入 reconnecting；只重建 H2 以后尾链，H1 实际拨号次数保持 1。
- H1 正常、三跳链的任一中间跳断开时，末跳端到端探活能够发现并重建尾链。
- 两条或更多 Forward 共享 H1 时，H1 失败只发生一次实际重拨，各 Forward 分别恢复自己的尾链。
- A 的 H2 失败不会断开共享 H1 的 B，也不会触发 B 的 reconnecting。
- 旧 tailGeneration 的 Wait、probe timeout、Close 和重建成功在新代建立后迟到，不影响新 client 和 Runtime 状态。
- Remote Forward 重建尾链后重新 bind；旧 remote listener/channel 被关闭，不残留双重监听。
- probe、backoff 或 tail Dial 期间 Stop，所有操作及时取消且之后无新连接产生。
- 认证失败、指纹变化和手动 Stop 不自动重试；网络错误的指数退避不超过一分钟。
- 上述交错在 `go test -race ./internal/forward ./internal/biz` 下通过，probe 和 goroutine 数不随重连次数增长。

#### 后续关联

- `SSH-01` 的有界 probe 和首跳 connectionGeneration 是本项的唯一池级健康事实来源。
- `SSH-02` 已同步将停止、探活和重建能力收敛到 ChainLease，并保留精确 generation Abort。
- `SSH-03` 的 ConnectionIdentity 必须覆盖链上每个 Host；任一中间或末跳 Host 修改都会让相关尾链换代。
- `RUN-01` 继续负责 Forward generation；ChainLease 的 tailGeneration 只管理一次 Forward 内部的 SSH 链代际，二者不能合并。

### 4.13 ROUTE-02：以 desired/applied 状态、串行协调器和持久 journal 管理 Route 副作用

#### 状态与结论

- 状态：已实现并通过 revision、journal、补偿与并发交错测试验证
- 确认日期：2026-07-21
- 问题结论：成立。当前 Save、Apply、Remove、DeleteSelection 后的 Reconcile 和启动 Resume 可以并发进入 `applySystem`，各自读取不同 Vault/hosts/Caddy 快照。固定 `.tunnelboard.tmp/.bak` 会互相覆盖，旧事务的迟到回滚也可能覆盖新事务已经成功的 hosts 或 Caddy 状态。
- 产品决策：Vault 中的 Route 是用户保存的 desired state；hosts、Caddy 和当前用户 CA 是本机 applied state。应用失败时保留用户配置并明确显示 pending/error/conflict，不把“保存成功”伪装成“系统已生效”，也不因执行失败静默丢弃编辑。
- 事务决策：所有 Route 配置变更和系统副作用必须经过唯一的 RouteCoordinator Module 串行执行，并以 desired revision、applied revision、transaction ID 和持久 journal 防止迟到回滚及崩溃后状态漂移。

当前应用已经通过 Wails `SingleInstanceLock` 限制同一应用实例；结合已确认“不支持多 Windows 用户同时运行 Web Route”，本项不再增加跨进程分布式锁。RouteCoordinator 仍必须处理同一应用内来自 UI、启动恢复、删除级联和备份恢复的并发入口。

#### desired state 与 applied state

可移植的加密 Vault 只保存期望配置：

- WebRoute 的域名、Forward 引用、hosts enabled、Caddy enabled、upstream/SNI 等字段；
- 每次配置 mutation 产生的单调 revision；
- 不保存当前机器是否正在运行 Caddy、是否已写 hosts、是否已信任 CA 等事实。

每用户 `%LocalAppData%\TunnelBoard\state` 下保存不参与备份的 RouteAppliedState：

```go
type RouteAppliedState struct {
    AppliedDesiredRevision string
    HostsDigest            string
    AppliedHosts           []route.HostEntry
    CaddyConfigDigest      string
    CaddyGeneration        uint64
    CATrustedSHA256        string
    Status                 RouteApplyStatus
    PortConflict           string
    LastError              string
    PendingTxID            string
}
```

- `AppliedHosts` 只保存 TunnelBoard managed 区块的条目，不复制 hosts 文件其他内容。
- `CATrustedSHA256` 从现有 Vault Prefs 迁入本机 applied state，并通过实际 CurrentUser Root 查询校验，不能单靠记录宣称已信任。
- 删除后的 Route 可以从 desired Vault 消失；如果旧系统副作用尚未清理，applied state/journal 保留机器本地 cleanup tombstone，UI 仍展示“待清理”入口，直到清理成功。
- LastError 必须脱敏并限制长度；journal/applied state 不存证书私钥、备份密码或 SSH 秘密。

#### RouteCoordinator Interface

应用层只通过以下有类型 Interface 访问 Route 变更：

```go
type RouteCoordinator interface {
    PreviewChange(ctx context.Context, change RouteChange) (RouteChangePreview, error)
    CommitChange(ctx context.Context, token string) (RouteCommitResult, error)
    ReconcileCurrent(ctx context.Context, reason ReconcileReason) (RouteReconcileResult, error)
    Neutralize(ctx context.Context, expectedRevision string) (RouteReconcileResult, error)
    Status(ctx context.Context) (RouteSystemStatus, error)
}
```

- `PreviewChange` 规范化并校验候选 Route，编译完整目标 hosts/Caddy 状态，检查域名确认、引用、端口与 CA 需求，返回绑定当前 desired/applied revision 的短时 token。
- `CommitChange` 在应用 mutation lock 和 RouteCoordinator 串行锁内重新验证 token，原子保存 desired state，再按同一 revision reconcile 系统。
- `ReconcileCurrent` 供正常启动、显式重试和删除 Forward 后的级联清理使用；调用方只提供原因，不能绕过协调器直接调用 Helper/Caddy/CA Adapter。
- `Neutralize` 供 DATA-01 使用，把当前 applied state 收敛为无 managed hosts、无自有 Caddy、无登记 CA 的安全中性态。
- `Status` 只汇总 desired、applied、journal 和 CaddySupervisor 的自有进程事实，不在查询路径写状态或用 bind 结果伪造应用状态。

RouteCoordinator 是深 Module：hosts 写入、Caddy Apply、CA 查询/信任、journal、补偿和崩溃恢复都是内部实现。前端与 App Module 不再拼接 `SaveWebRoute → ApplyRoute`。

#### 正常提交顺序

一次 Commit 固定执行：

1. 获取应用 mutation lock，再获取 RouteCoordinator 串行锁；检查 restore quarantine 和未完成 journal。
2. 重新验证 preview token、desired revision、applied revision 和候选完整目标状态。
3. 写入 `pending` journal：transaction ID、beforeApplied、候选 desired revision、目标摘要、确认事实和阶段。
4. 原子保存新的 desired Vault revision。配置保存从此成功，即使后续系统应用失败也不丢弃用户编辑。
5. 应用 managed hosts；请求携带 transaction ID 和期望 managed-block digest，Helper 发现区块已被外部修改时拒绝覆盖并返回冲突。
6. 把完整 Caddy 目标交给 CaddySupervisor `Apply(revision, config)`；由 Supervisor 根据自有进程 generation 决定热重载、冷启动或结构化端口冲突。
7. 根据目标查询、信任或撤销当前用户 CA；不经过提权 Helper。
8. 每步成功后先持久更新 journal phase；全部完成后原子写 RouteAppliedState、清除 pending journal 并返回逐副作用结果。

系统应用始终基于“当前 Vault 全部 Route 编译出的完整目标”，不按单条 Route 对 hosts/Caddy 做增量猜测。这样一次修改仍可使全部系统副作用收敛到同一个 desired revision。

#### 失败、降级与补偿

- Preview 取消、域名未确认或 token stale：在 journal 和 desired 写入前失败，零副作用。
- Vault 保存失败：不执行任何系统副作用。
- hosts、Caddy 或 CA 的非预期错误：desired 配置保留；在锁内按 journal 的 beforeApplied 逆序补偿本事务已经完成的副作用，使系统回到上一 applied state。状态记录为 saved-but-not-applied/error，并提供显式重试。
- 443 被外部进程占用：按既有产品决策属于可解释的 degraded 结果，不回滚已成功的 hosts。RouteAppliedState 记录 hosts 已应用、Caddy port-conflict、CA 未需要或未应用，界面显示 hosts-only。
- 补偿失败：保留 pending journal 和逐项真实状态，阻止后续普通 Route mutation；用户通过“修复 Route 状态”继续补偿或重新收敛，不能覆盖证据后继续操作。
- 删除 Route 时 desired state 可以移除该 Route，但 beforeApplied 和 cleanup tombstone 保留旧 managed effects；清理失败后 UI 仍提供重试，不会出现配置项消失且残留无法管理。
- 所有补偿都校验 transaction ID 和 beforeApplied revision；事务锁释放后不得再运行旧的异步 rollback。CaddySupervisor 和 Helper 也必须拒绝迟到的旧 revision。

#### journal 与崩溃恢复

Route journal 使用当前用户独占权限，写入同目录唯一临时文件、flush 后原子替换；不复用固定 `.tmp/.bak`。

应用启动时在任何 `ResumeCaddy`、AutoStart 或 Route Apply 之前检查 journal：

- 无 pending journal：按 applied state 和当前 desired state执行正常 `ReconcileCurrent(startup)`；DATA-01 restore quarantine 存在时跳过所有自动网络副作用。
- 有 pending journal：不自动弹 UAC、不盲目继续新 desired 应用。先查询 managed hosts、CaddySupervisor 和 CurrentUser CA 的真实状态，并停止不应继续运行的自有 Caddy。
- 如果恢复只需无特权且可确定的本机操作，可以按 journal 收敛；如果必须写 hosts，则展示“Route 状态需要修复”，由用户点击后启动本应用生命周期的会话级 Helper。
- 修复完成前阻止新的 Route mutation，但其他不依赖 Route 的功能可以继续使用。
- 崩溃恢复选择补偿到 beforeApplied，或继续到 journal 已确认的 desired revision时，都必须是一个新的、有记录的 recovery generation，不能复用旧异步回调。

#### hosts 文件事务

- Helper 每次写入使用 hosts 同目录下包含 transaction ID 和随机 nonce 的唯一临时文件。
- 在替换前重新读取 managed 区块并校验 expected digest；区块外的用户/系统内容以替换瞬间的最新文件为基础重新渲染，不能用旧全文件快照覆盖外部修改。
- 写临时文件后 flush，并使用平台可靠的原子替换方式；失败时保留可诊断结果，但不依赖一个全局 `.bak` 作为跨事务回滚来源。
- 补偿是一个带新 expected digest 的新 managed-block 写入，不直接把旧完整 hosts 文件复制回来。
- Helper 仍只能修改 TunnelBoard 标记区块和回环地址，transaction ID 不扩大其能力。

#### 状态与交互

RouteStatus 至少区分：

- `applied`：desired revision 的全部要求已生效；
- `hosts_only`：hosts 已应用，但 Caddy 因 443 冲突按产品策略降级；
- `pending`：配置已保存，尚未尝试或等待显式修复/激活；
- `error`：应用或补偿失败，包含脱敏的失败步骤；
- `cleanup_pending`：Route 已从 desired 删除，但旧系统副作用尚未清理；
- `quarantined`：DATA-01 恢复隔离态禁止应用；
- `unknown`：无法可靠读取本机事实，不能显示为 stopped/not applied。

UI 的开关、保存和删除均等待一个 `CommitChange` 结果，随后同时刷新 desired snapshot 和 applied status。UI-02/UI-03 后续只负责正确展示这些结构化事实，不自行推导或乐观伪造状态。

#### 预期代码范围

- `internal/biz/router.go`：重构为 RouteCoordinator，增加串行执行、revision/token、desired/applied 状态、journal、补偿和恢复。
- `internal/model/vault.go`：Route desired revision；移除可移植 `CATrustedSHA256` 事实字段并提供迁移。
- 新增每用户 RouteAppliedState/journal Store Adapter，与加密 Vault 备份范围分离。
- `internal/helper/hostsfile.go`、Helper protocol：transaction ID、expected managed digest、唯一临时文件和基于最新外部内容的区块替换。
- CaddySupervisor：`Apply(revision, config)` 和 generation-safe 结果，拒绝迟到旧 revision。
- `app.go`/后续应用 Module：用 Preview/Commit/Reconcile/Status 取代 Save、Apply、Remove 和 Resume 的前端编排。
- 前端 Routes/Overview：显示 saved-but-not-applied、hosts-only、cleanup pending、quarantined 和恢复操作。

#### 验收标准

- Apply+Apply、Apply+Remove、启动 Reconcile+用户 Commit、DeleteSelection+Commit 强制交错时，同一时刻最多一个 Route 事务进入 Adapter；最终状态只对应最高已接受 desired revision。
- 旧事务在新事务成功后迟到返回错误或成功，都不能回滚或覆盖新 hosts、Caddy、CA 和 RouteAppliedState。
- 两次 hosts 写入使用不同临时文件；外部进程在事务期间修改 managed 区块外内容时，该内容保持，TunnelBoard 只更新自己的区块。
- expected managed digest 不匹配时拒绝覆盖并显示冲突；不会拿旧完整 hosts 快照回滚用户新内容。
- 任一 hosts/Caddy/CA 步骤注入失败时，desired 配置保留，系统补偿到 beforeApplied；补偿失败保留 pending journal 和真实逐项状态。
- 443 冲突保留 hosts-only，状态明确为 degraded，不信任无实际 Caddy 需要的 CA，也不报告完整 applied。
- 删除清理失败后，即使 desired 中已无 Route，UI 仍显示 cleanup pending 并可重试直至旧副作用清除。
- 在每个 journal phase 模拟进程崩溃；重启不自动弹 UAC、不执行无记录的新副作用，并能在用户确认后收敛到 beforeApplied 或已确认 desired。
- DATA-01 restore quarantine 存在时，启动 Reconcile 和普通自动恢复均不写 hosts、不启动 Caddy、不信任 CA。
- RouteStatus 查询不修改文件、不重载 Caddy、不信任 CA；无法确认实际状态时返回 unknown。
- 并发及故障注入在 `go test -race ./internal/biz ./internal/helper ./internal/caddy` 下通过。

#### 后续关联

- `SEC-02` 已同步把 `CATrustedSHA256` 从可移植 Vault 移到每用户 RouteAppliedState，实际证书库查询仍是事实来源。
- `ROUTE-01/SEC-03` 的 CaddySupervisor 是 RouteCoordinator 唯一的 Caddy Adapter；RouterBiz 不再判断热重载、冷启动或端口所有权。
- `DATA-01` 的 Neutralize 和 ActivateRestoredNetwork 直接复用本协调器、journal 和 applied revision，不再另造恢复专用 Route 事务。
- `UI-02/UI-03` 必须按本项结构化状态展示保存与生效差异。
- `ARCH-01` 的应用 Module 应把 Preview/Commit 隐藏在有类型命令后，不继续暴露 Save+Apply 的调用顺序。

### 4.14 SEC-05：为备份解析、KDF、实体和私钥建立统一资源预算

#### 状态与结论

- 状态：已实现并通过恶意输入与资源预算测试验证
- 确认日期：2026-07-21
- 问题结论：成立。PreviewImport、ApplyImport、RestoreBackup 和 SaveImportKeyFile 都会先 `os.ReadFile` 整个文件并重复解密；备份头允许最高 1 GiB Argon2 内存、10 轮和 24 并行度，解密载荷、实体数、字符串和私钥文件均无上限。恶意或损坏备份可造成内存耗尽、长时间无响应和高复杂度导入。
- 产品决策：所有备份导出、导入、完全还原和私钥提取统一经过 BackupPackage Module；资源预算是固定安全策略，不作为普通前端设置开放。
- 交互决策：导入和恢复使用后端 staged token。Preview 只读取/解密一次，Commit 消费已验证的 staged 数据，不重新读取路径、不重新接收密码、不重复执行 KDF。

#### 统一资源预算

所有长度按 UTF-8 字节计算；计数在解密后、构造完整领域对象前尽早验证。

| 资源 | 硬限制 |
| --- | ---: |
| 加密备份文件 | 64 MiB |
| 解密载荷 | 64 MiB 减去格式开销 |
| Argon2 memory | 32～256 MiB |
| Argon2 time | 2～6 |
| Argon2 parallelism | 1～8 |
| 同时执行的 KDF | 全应用 1 个 |
| 备份密码输入 | 1 KiB |
| 文件夹 | 500 |
| SSH Host | 1,000 |
| Forward | 5,000 |
| Web Route | 2,000 |
| HostKey | 5,000 |
| 全部实体总数 | 10,000 |
| 单条 SSH 主机链 | 16 跳 |
| 单个私钥文件 | 1 MiB |
| 私钥文件总量 | 16 MiB |
| 私钥文件数量 | 64 |
| 普通名称、用户等短字段 | 256 B |
| Host、域名 | 255 B |
| 文件路径 | 4 KiB |
| Notes | 16 KiB |
| 全部字符串累计 | 8 MiB |
| JSON 最大嵌套深度 | 32 |

Argon2 parallelism 的导入上限固定为 8，而不是 `min(8, 当前 CPU)`：parallelism 是可移植备份格式的一部分，不能因导入机器 CPU 较少而拒绝另一台机器生成的合法备份。官方导出继续使用 64 MiB、time=3、parallelism=`min(4, GOMAXPROCS)`；Go 调度器限制实际并行执行，全应用 KDF 信号量防止多个操作叠加资源。

这些限制对个人桌面隧道工具已经显著高于正常规模。未来若确有合法场景超过限制，应升级带版本的格式和预算策略，不能让前端参数临时放宽解析器。

#### BackupPackage Module

该 Module 在备份文件与业务导入/恢复之间形成唯一 seam：

```go
type BackupPackage interface {
    Stage(ctx context.Context, path string, password SecretInput, purpose StagePurpose) (StagePreview, error)
    CommitImport(ctx context.Context, token string, plan ImportPlan) (ImportSummary, error)
    TakeRestore(ctx context.Context, token string) (StagedRestore, error)
    SaveKeyFile(ctx context.Context, token, keyID, destination string) error
    Export(ctx context.Context, destination string, password SecretInput, options ExportOptions) (ExportSummary, error)
    Cancel(token string)
}
```

- 调用方不接触解密 payload、私钥字节或 KDF 参数实现。
- StagePurpose 明确 import/restore；同一份 token 不能跨用途使用。
- 全应用同时只保留一个 staged package。新的 Stage、取消、过期、Commit 或应用退出会废弃旧 token，并尽力清理密码、解密载荷和私钥内存。
- token 使用高熵随机值，绑定当前应用 generation、文件摘要、purpose 和 Stage 时 Vault revision；单次使用，默认 10 分钟过期。
- Commit 时 Vault revision 变化返回 stale，要求重新 Stage，避免预览之后当前数据变化导致冲突计划失效。

#### 有界读取和格式预检

1. 使用 `os.Open` 得到稳定句柄，拒绝目录、设备、管道等非普通文件。
2. 对已打开句柄执行 `Stat`；大小超过 64 MiB 立即拒绝。
3. 即使 Stat 合法，仍通过 `io.LimitReader(max+1)` 读取，防止文件在检查后增长；读到第 `max+1` 字节返回 `ErrBackupTooLarge`。
4. 在 Argon2 前校验 magic、格式版本、完整头长度、密文最小长度、KDF 上下限和密码长度。
5. 获取全应用容量为 1 的 KDF 信号量后再派生密钥；排队可响应 context 取消。
6. `argon2.IDKey` 本身不能可靠中途取消，因此执行期间不启动第二个 KDF；严格参数上限保证最坏资源有界，结束后再次检查 context。
7. AEAD 解密结果受文件大小上限约束；密码错误和密文损坏继续返回统一错误，不泄漏更多内容。

不能只使用 `Stat`，也不能先 `os.ReadFile` 再检查长度；这两种方式都无法避免分配超大缓冲区。

#### 解密载荷预检

在 typed `json.Unmarshal` 前，先用流式 JSON token 扫描器验证：

- 最大嵌套深度、对象/数组结构和总 token 数；
- 各实体数组长度和总实体数；
- 单字符串及累计字符串字节数；
- keyFiles 数量、路径长度、单文件和累计 base64 解码后大小；
- 数字范围、单条链长度和明显非法的容器形状。

预检通过后才构造 `VaultData` 和 keyFiles，并继续执行 schema、ID 唯一性、引用完整性、文件夹深度、字段枚举和领域 Validate。这样 64 MiB 文件上限不会因 JSON 对象膨胀变成无界 typed allocation。

#### Stage、Commit 与私钥提取

- `StageImport` 返回实体数量、无秘密冲突摘要、私钥逻辑 ID/原文件名和预算警告；不得返回完整 SSHHost 或私钥内容。
- `CommitImport(token, plan)` 直接使用后端 staged 数据，不重新读取文件、不重新执行 KDF。
- 合并前同时检查“导入包自身”和“当前 Vault + 导入结果”均不超过预算；不能利用多次小导入绕过最终 Vault 上限。
- `SaveKeyFile` 通过 token 和不含路径语义的 keyID 选择暂存私钥，只把字节写入用户通过保存对话框选择的目标；不把字节送入 WebView。
- 私钥输出在 Unix 使用 0600，在 Windows 设置为当前用户可读写的受限 ACL；目标写入使用唯一临时文件和原子替换。
- Import/Restore token 只能消费一次；单独保存多个私钥时由 Stage 中的受限 key-export lease 管理已导出集合，全部完成、取消或过期后清理。

#### 导入复杂度

- 进入 Vault Update 前分别扫描一次当前 folders/hosts/forwards/routes/hostkeys 最大 ID，之后单调递增分配，删除逐实体 `nextID` 全表扫描。
- 当前与导入 SSH endpoint 建立 map，冲突检测从双重循环降为 O(current+imported)。
- ID 重映射、folder 映射和 HostKey 去重均使用有界 map；创建 map 前已经通过实体预算。
- Apply 的 CPU 与内存复杂度必须近似 O(n)，不能因输入接近上限退化为 O(n²)。

#### 导出预算

- 导出前验证当前 Vault 的实体、字段、链长度和累计字符串预算；超限时拒绝并指出类别，不生成未来自身无法导入的备份。
- 私钥文件通过 open + Stat + `LimitReader(1 MiB+1)` 读取；同一路径只读取一次，累计超过 16 MiB 或数量超过 64 时停止。
- JSON 编码完成后验证明文和最终密文均在 64 MiB 内。
- 导出和导入共用同一个 KDF 信号量与 BackupLimits，不复制常量。
- 写目标使用同目录唯一临时文件、flush 和原子替换；失败不留下看似完整的备份。

#### 错误和可观察性

- 使用结构化错误区分 file-too-large、kdf-out-of-budget、entity-limit、field-too-long、key-file-limit、invalid-schema、wrong-password-or-corrupt 和 busy/cancelled。
- 日志只记录错误类别、文件总字节和超限类别，不记录密码、解密内容、私钥、完整 Host 配置或用户文件路径。
- UI 对合法超限给出明确数字，例如“备份含 5,231 个 Forward，当前上限为 5,000”，但密码错误与篡改仍保持统一提示。

#### 预期代码范围

- `internal/vault/backup.go`：版本化格式头、BackupLimits、KDF 校验、有界解密和 JSON 预算预检。
- `internal/biz/backup.go`：staged token store、单一 KDF 门禁、Import/Restore/KeyExport 生命周期、O(n) ID/冲突映射和合并后预算。
- `app.go`/后续应用 Module：用 Stage/Commit/Cancel/SaveKeyFile token Interface 替换四处 `os.ReadFile + ParseBackup`。
- 导出路径：私钥有界读取、目标原子写和统一预算。
- 测试增加可控文件增长、非普通文件、KDF 阻塞、stale token、超限 JSON 和大实体集 fake Adapter。

#### 验收标准

- 64 MiB 边界文件可进入解析，64 MiB+1 在 KDF 前拒绝；即使文件在 Stat 后增长也最多读取 max+1。
- 声明 257 MiB memory、time=7 或 parallelism=9 的头在 Argon2 前拒绝；合法 parallelism=8 的备份可在低核心机器导入。
- 并发发起 PreviewImport、StageRestore 和 Export 时只有一个 KDF 执行，其他可取消且不额外分配 KDF 内存。
- 超数量实体、17 跳链、1 MiB+1 单私钥、16 MiB+1 私钥总量、超长字段、8 MiB+1 累计字符串和深度 33 JSON 均在 typed 模型构造或 Vault Update 前拒绝。
- Preview 后修改原文件不影响 Commit 使用的 staged 内容；修改当前 Vault revision 后旧 token 被拒绝。
- ApplyImport 不再次读取源文件、不再次要求密码、不再次执行 KDF。
- 当前 Vault 接近上限时，小备份若使合并结果超限也被拒绝，Vault 零变化。
- 5,000 Forward 规模的 ID 分配和冲突检测保持线性，不出现逐实体全表扫描。
- 导出无法包含超限私钥或生成超过自身导入预算的文件；失败时目标路径无半成品。
- token 过期、取消、消费和应用退出后不能再导入、恢复或导出私钥；秘密不出现在响应、日志和诊断包。

#### 后续关联

- `DATA-01` 的 StageRestore 直接使用本 Module 的 restore-purpose token 和资源预算，CommitRestore 消费 StagedRestore 而非路径/密码。
- `SEC-06` 必须把 ImportPreview、RestorePreview 和 KeyExport 改成无秘密 DTO，且所有 token 只是不透明随机标识。
- `ARCH-01` 的应用 Module 应暴露有类型的 Stage/Commit 命令，不让前端接触 BackupPackage 的内部 Adapter。
- 预算改变属于备份格式兼容性变更，REL-01 的 artifact smoke 应包含官方历史 fixture 和跨机器 parallelism fixture。

### 4.15 SEC-06：已保存秘密不返回 WebView，新秘密只允许单向一次性提交

#### 状态与结论

- 状态：已实现并通过 DTO、单向秘密提交与响应面测试验证
- 确认日期：2026-07-21
- 问题结论：成立。当前 `GetVaultData` 返回完整 `model.VaultData`，其中 SSHHost.Password 会进入 Wails 响应并长期保存在前端 `sshHosts` 数组；编辑主机时又把该值重新填入密码框。ImportPreview 的 HostConflict 也返回完整 SSHHost，可再次泄漏导入包中的密码或私钥口令。
- 产品决策：后端绝不把已经持久化的 SSH 密码、私钥口令、备份密码或私钥字节发送给 WebView。用户新输入的秘密只允许通过有类型命令单向传入 Go，并在请求结束后尽快从前端状态清除。
- 诚实边界：用户在 Wails 页面输入秘密时，明文不可避免会短暂存在于 WebView 和绑定请求中。本方案减少长期驻留、复制和回传面，不承诺能够阻止同用户高权限调试器读取进程内存，也不虚假承诺 Go/JavaScript GC 内存可立即物理清零。

#### 内部模型与 Wails DTO seam

内部 `model.SSHHost` 继续供加密 Vault、备份和 SSH Runtime 使用，可以保留 Password 字段；它不得直接出现在任何 Wails 绑定 Interface 的参数、返回值或事件中。

Wails 只使用专用 DTO：

```go
type SecretState string
const (
    SecretAbsent SecretState = "absent"
    SecretStored SecretState = "stored"
)

type SSHHostView struct {
    ID                  int
    Name                string
    Host                string
    Port                int
    User                string
    AuthType            string
    KeyPath             string
    AgentSocketPath     string
    SecretState         SecretState
    KeepAliveIntervalMs int
    TimeoutMs           int
    HostKeyAlgorithms   string
    Notes               string
}

type SecretAction string
const (
    SecretKeep    SecretAction = "keep"
    SecretReplace SecretAction = "replace"
    SecretClear   SecretAction = "clear"
)

type SaveSSHHostCommand struct {
    Host         SSHHostView
    SecretAction SecretAction
    SecretValue  SecretInput
}
```

- SSHHostView 只暴露 `absent/stored`，不暴露长度、哈希、掩码字符数或最后若干字符，避免形成额外秘密侧信道。
- SecretInput 是仅用于入站请求的命名类型；格式化和意外 JSON 序列化时应输出固定 `[REDACTED]`，业务实现必须显式消费，禁止把整个 command 结构写日志。
- Save 返回 SSHHostView；错误只包含字段和错误类别，不回显 SecretValue。
- GetVaultData 替换为 VaultView，最终并入 ARCH-01 的 GetSnapshot。VaultView 中 folders/forwards/routes/prefs 使用非敏感 View，SSHHosts 只含 SSHHostView。
- 所有前端事件、toast 和批量结果也必须复用 View/Result DTO，不能因为“内部调用”重新暴露 model.SSHHost。

#### SecretAction 语义

后端集中验证以下状态机，前端不能自行决定空字符串含义：

- 新建 password Host：必须 `replace` 且 SecretValue 非空。
- 新建 ssh_key Host：私钥口令可选；有口令用 `replace`，无口令用 `clear`。
- 新建 ssh_agent Host：只允许 `clear`，且 AgentSocketPath 按平台规则校验。
- 编辑且 AuthType 未变化：默认 `keep`；输入新值用 `replace`；用户明确删除保存的秘密用 `clear`。
- AuthType 发生变化：`keep` 一律拒绝，因为同一个旧值不能从 SSH 密码静默变成私钥口令；password 必须 replace，ssh_key 可以 replace/clear，ssh_agent 强制 clear。
- password Host 被 clear 后允许保存为 `SecretState=absent`，但 Start/预检必须返回结构化 `CredentialMissing`，且该错误不可自动重连；这样用户可以主动移除秘密，而界面不会假装该 Host 仍可连接。
- `replace/clear` 只有在持久秘密实际变化时递增 SSH-03 的 CredentialRevision；keep 不递增。

空字符串不再同时表示“保留旧值”和“清空”。所有秘密合并、AuthType 转换、CredentialRevision 更新和领域校验集中在后端一个 Module 内完成。

#### 前端交互

- 编辑 Host 时密码框始终为空；SecretState=stored 时显示“已保存，不修改”，不能填入掩码假值。
- 输入内容后状态明确为“将替换”；另有显式“清除已保存密码/口令”操作，不能靠删除掩码字符推断。
- 保存、取消、Escape 关闭或页面卸载后清空 SecretValue；保存请求在 `finally` 清空本地秘密，即使后端返回错误也不长期保留。
- 新建 Host 的两个入口（Hosts 页面和 Forward 内嵌创建）复用相同表单领域规则，不能一个使用 SecretAction、另一个继续提交完整 SSHHost。
- 密码、口令和备份密码不进入 localStorage、sessionStorage、URL、路由参数、持久化 store、剪贴板自动操作或前端错误对象。
- 可以使用浏览器原生 password input 与合理 autocomplete，但关闭生产构建 DevTools/CSP 只能作为纵深防御，不能替代后端不回传秘密的 Interface。

#### 备份、导入和恢复 DTO

- SEC-05 的 StageImport/StageRestore 在后端持有解密数据，只返回不透明 token 和无秘密 Preview。
- HostConflictView 只包含导入项的 name、规范化 host/port/user、authType、keyPath basename、SecretState 和 existing ID；不得包含 Password、完整私钥路径或 CredentialRevision。
- KeyFileView 使用随机 keyID、文件 basename 和大小；不返回源机器完整路径或字节。
- CommitImport 只接收 token 和冲突计划；SaveKeyFile 只接收 token、keyID 和保存对话框目标。
- RestorePreview 只返回实体计数、网络意图、缺失外部文件警告和当前副作用摘要。
- 备份密码在 Stage 请求结束后立即从前端表单清空；后续提交、恢复和私钥另存只使用 token。

#### 日志、错误和诊断

- 结构化日志使用字段白名单，禁止记录 VaultData、SSHHost、SaveSSHHostCommand、backup payload、认证配置或任意请求 body。
- SSH/备份错误在 Adapter seam 转换为安全错误类别；不得把外部库可能包含的认证 payload 原样拼入用户错误或 slog attrs。
- 诊断包继续只包含计数、非敏感 Runtime/Route 状态和脱敏日志；组装前再运行统一 redactor 作为纵深防御。
- Caddy 日志不应包含 SSH 秘密；PERF-01 的统一日志 Module 仍必须执行长度限制和已知敏感字段过滤。
- 不通过“把秘密替换成星号后记录”保存请求结构，因为字段新增或嵌套变化会导致漏脱敏；只记录事先允许的非敏感字段。

#### Wails 生成面

- 删除返回 model.VaultData 的 GetVaultData 和接受/返回 model.SSHHost 的 SaveSSHHost 绑定。
- 重新生成 `frontend/wailsjs` 后，响应 View 类型中不得出现 Password；只有入站 SaveSSHHostCommand 可以包含 `secretValue`。
- TypeScript 不应生成可从 GetSnapshot、ImportPreview、RestorePreview 或事件中取得 SecretValue 的类型路径。
- 后端增加 binding contract 测试，递归检查所有绑定响应 DTO 的 JSON key，拒绝 password、passphrase、secretValue、privateKey 等禁用字段；对明确的入站 command 类型单独建立允许清单。

#### 内存生命周期

- Go string 和 JavaScript string 都不可可靠原地擦除；实现目标是最少复制、最短作用域、请求后解除引用。
- staged 私钥和解密备份优先用可覆盖的 byte slice，过期/消费时尽力覆写，再解除引用；这仍是 best effort。
- SecretInput 不缓存到长期 App struct，不跨 goroutine 复制，不作为事件 payload，不进入 retry closure。
- SSH Runtime 从加密 Vault 读取所需凭据并只传入当前拨号链；连接建立后释放临时认证配置引用。

#### 预期代码范围

- 新增应用 DTO/转换 Module：VaultView、SSHHostView、SaveSSHHostCommand、SecretInput、Import/Restore Preview View。
- `app.go`/ARCH-01 应用 Module：用 GetSnapshot/SaveSSHHostCommand 替换内部持久模型绑定。
- `internal/biz/catalog.go`：集中 SecretAction 合并、AuthType 转换、CredentialRevision 和 CredentialMissing 校验。
- `internal/biz/backup.go`：只生成无秘密 preview，私钥通过 staged keyID 导出。
- HostsPage、ForwardModal、SettingsPage：空白编辑框、keep/replace/clear 状态、finally 清理和 token 流程。
- Wails 绑定生成和契约测试：响应禁用字段、入站秘密类型允许清单、sentinel 泄漏扫描。

#### 验收标准

- Vault 中保存唯一 sentinel SSH 密码和私钥口令后，GetSnapshot、Host 保存响应、Runtime/Route 状态和所有 Wails 事件 JSON 均搜索不到 sentinel。
- 打开编辑 Host 弹窗时输入框为空，页面只知道 stored/absent；不查看 DOM、Vue state 或网络响应取得旧秘密。
- keep 保存保留原秘密且 CredentialRevision 不变；replace 正确替换并递增；clear 删除秘密并递增。
- AuthType 变化时 keep 被拒绝；password 缺秘密返回 CredentialMissing，且 Runtime 不自动重连。
- ImportPreview、RestorePreview、HostConflictView 和 KeyFileView 搜索不到导入包 sentinel、私钥字节和源机器完整路径。
- Preview 成功后前端备份密码被清空；CommitImport、CommitRestore 和多次 SaveKeyFile 均不再次要求或传输密码。
- 生成的 wailsjs 响应类型无 Password；SecretValue 只存在于明确的入站 SaveSSHHostCommand。
- slog buffer、磁盘日志、Caddy 日志、toast、前端错误对象和诊断包均搜索不到 sentinel。
- 关闭/取消/失败后前端 reactive state 中 SecretValue 为空，未写入任一 Web Storage。
- 加密 Vault 和加密备份文件原始字节搜索不到 sentinel；解密后内部领域模型仍能正确用于 SSH 认证。

#### 后续关联

- `SSH-03` 已同步使用 SSHHostView、SecretAction 和 CredentialRevision，连接身份不包含秘密明文或可离线猜测哈希。
- `SEC-05/DATA-01` 的 staged token 是备份密码和私钥不重复进入 WebView 的前置条件。
- `PERF-01` 的日志轮转 Module 必须同时执行结构化字段白名单、单行限制和诊断 redaction。
- `ARCH-01` 应以本项 DTO 作为应用 Module 的唯一外部 Interface，不能在重构期间临时保留完整 Vault 绑定。
- `ARCH-02` 的 Host 表单复用必须包含完整 SecretAction 状态机，不能只复用视觉字段。

### 4.16 PERF-01：统一日志轮转、写入安全处理和 generation cursor tail

#### 状态与结论

- 状态：已实现并通过轮转、游标、上限与脱敏测试验证
- 确认日期：2026-07-21
- 问题结论：部分成立且需要精确表述。TunnelBoard 主程序日志已经使用 2 MiB、保留一档的简单轮转；Caddy stdout/stderr 仍永久追加到 `caddy.log`。`GetLogTail` 对两个来源都从 offset 无界 `io.ReadAll` 到 EOF，并且只有裸字节 offset，无法可靠识别文件轮转和替换。
- 产品决策：TunnelBoard 与 Caddy 日志统一进入 LogStore Module，使用固定磁盘、单行、队列和单次 tail 预算；前端通过 `{generation, offset}` cursor 增量读取，轮转、截断和丢弃必须显式返回。
- 安全决策：SEC-06 的结构化字段白名单、脱敏和单行限制发生在写入任何磁盘、内存环、页面 tail 或诊断包之前，不能只在导出诊断包时补做正则替换。

#### 固定预算

| 项目 | 硬限制 |
| --- | ---: |
| 单个当前日志文件 | 5 MiB |
| 每个来源历史文件 | 3 个 |
| 每个来源磁盘上限 | 20 MiB（当前 + 3 档） |
| TunnelBoard + Caddy 总量 | 40 MiB |
| 单行最大长度 | 64 KiB |
| 每来源待写内存队列 | 1 MiB |
| 单次 Tail 最大字节 | 256 KiB |
| 单次 Tail 最大行数 | 500 |
| 前端每来源保留行数 | 2,000 |

预算是产品常量，不允许前端传入更大的 maxBytes。SEC-01 的临时 Helper 不属于 Logs 页面来源，继续使用独立且有界的 2 MiB 当前文件 + 1 档历史，并随会话 Helper 生命周期关闭。

#### LogStore Interface

```go
type LogSource string
const (
    LogTunnelBoard LogSource = "tunnelboard"
    LogCaddy       LogSource = "caddy"
)

type LogCursor struct {
    Generation uint64 `json:"generation"`
    Offset     int64  `json:"offset"`
}

type LogTailResult struct {
    Lines        []string `json:"lines"`
    NextCursor   LogCursor `json:"nextCursor"`
    Rotated      bool `json:"rotated"`
    Truncated    bool `json:"truncated"`
    DroppedBytes int64 `json:"droppedBytes"`
}

type LogStore interface {
    Append(source LogSource, line []byte)
    Tail(source LogSource, cursor *LogCursor) (LogTailResult, error)
    CloseSource(source LogSource) error
    Close() error
}
```

- source 是后端 enum，不接受任意文件名或路径。
- Append 接收一条逻辑日志；清洗、截断、排队、轮转和 generation 都隐藏在 Module 内。
- Tail 的预算固定在实现内，调用方不能要求读取整个文件。
- CloseSource 等待写队列在 deadline 内排空并关闭文件；超时记录 dropped count 后结束，不能阻塞应用或 Caddy Stop。

#### 写入与轮转

1. slog 记录先经过 SafeLogHandler：只保留允许的 attrs，把敏感字段替换为 `[REDACTED]`，规范化用户目录并限制 message/attr 总长度。
2. Caddy stdout/stderr 由 CaddySupervisor 的 pipe reader 按行读取；不再把子进程永久绑定到一个追加文件句柄。
3. pipe reader 使用固定缓冲分段查找换行。单行超过 64 KiB 后只保留前缀和 `[truncated]`，继续丢弃到下一个换行，不能让 `bufio.Scanner` 或动态 slice 无界增长。
4. 每个来源最多排队 1 MiB。磁盘过慢时 reader 继续排空子进程 pipe 并丢弃超预算日志，之后写一条聚合的 `dropped N bytes` 标记，避免日志反压卡死 Caddy。
5. 当前文件下一条完整日志会超过 5 MiB 时轮转：关闭当前文件，按 `.2→.3`、`.1→.2`、current→`.1` 顺序处理并重新打开 current。
6. Windows 上每个目标在替换前显式处理旧档，所有句柄先关闭；不能依赖 `os.Rename` 自动覆盖已有 `.1`。
7. 成功轮转后 generation 单调递增。轮转失败时保留当前可写句柄或进入明确 degraded 状态，不允许日志错误递归写入同一失败 LogStore。

CaddySupervisor 已在 SEC-03 中拥有进程句柄和 Job Object，因此 stdout/stderr reader、写队列和关闭顺序也属于该进程 generation。旧 Caddy generation 的迟到输出可以完成入队，但不能关闭或替换新 generation 的 LogStore ownership。

#### Tail 语义

- `cursor=nil`：返回当前 generation 最新最多 256 KiB/500 行，从完整行边界开始；旧历史档不自动回放。
- generation 匹配且 backlog 在预算内：从 offset 读取到本次预算或当前完整行末尾。
- generation 匹配但 backlog 超预算：跳到最新 256 KiB 窗口，按下一完整行对齐，设置 `Truncated=true` 和 DroppedBytes；不让页面永久追赶旧日志。
- generation 不匹配：说明文件已轮转/替换，按首次读取规则返回当前最新窗口，并设置 `Rotated=true`。
- offset 大于当前文件大小也视为替换/截断，不能简单设为 EOF 后静默返回空。
- 文件尾是不完整行时不向前端发布；NextCursor 停在最后一个完整换行之后。若该行最终超过 64 KiB，则按超长行规则截断并推进。
- 读取使用 `ReadAt`/SectionReader 和固定缓冲，禁止 `io.ReadAll`。
- Tail 在 LogStore 内与轮转协调；返回的 NextCursor 必须与实际读取 generation 一致。

#### 页面行为

- Logs 页面为 tunnelboard/caddy 分别保存 cursor 和最多 2,000 行，切换来源不混用 offset。
- Rotated 时保留已有可见内容或插入明显分隔标记，再接续新 generation；不能把轮转显示为普通空日志。
- Truncated/DroppedBytes 显示“日志过多，已跳过 N 字节”，不逐条弹 toast。
- 暂停只停止前端请求；恢复时按 cursor 获取最新窗口，超预算按上述规则跳过。
- “清空视图”仍只清前端内容；若未来增加删除磁盘日志，必须是独立确认命令并生成新 generation。
- 迟到的旧 source/cursor 响应继续按 request token 丢弃，不能在切换来源后混入另一页面。

#### 脱敏和诊断

- SafeLogHandler 以 attrs 白名单为主；禁止记录 VaultData、SSHHost、SecretInput、备份 payload、HTTP Authorization/Cookie 和完整请求对象。
- 文本 redactor 覆盖 password/passphrase/secret/token/private key、Bearer、JSON 键值和 URL query 中的敏感键，作为第三方错误/Caddy 文本的纵深防御。
- redactor 在磁盘和内存环之前执行；BuildBundle 可以再次执行相同 redactor，但不应成为第一道处理。
- Caddy access log 若未来启用，默认不记录 query、Authorization、Cookie 和上游敏感 header；启用字段必须列入允许清单。
- 所有日志文件使用当前用户受限权限；日志目录不进入备份包。

#### 生命周期

- App 启动时先创建 LogStore，再组装 SafeLogHandler、内存环和 CaddySupervisor。
- Caddy 启动成功的定义不依赖日志 reader，但 reader 必须在 child resume 前就绪，避免 pipe 填满。
- Caddy Stop：先停止/等待进程，持续排空 pipe 至 EOF，再在有界 deadline 内关闭该 source generation。
- App Shutdown：停止产生新日志的 Module，关闭 Caddy，再排空 LogStore，最后恢复最小 stderr fallback；关闭期间的错误不能触发递归日志死锁。
- 崩溃后的已有文件在下次启动按大小恢复，必要时先轮转；新的应用进程使用新内存 generation，前端 cursor 本来也从 nil 开始。

#### 预期代码范围

- `internal/diag/logstore.go`：多来源预算、队列、轮转、generation、Tail 和关闭。
- `internal/diag/safe_handler.go`：结构化字段白名单、redactor 和长度限制。
- 替换现有 `diag.LogFile` 的单档实现，保留兼容迁移/测试 fixture。
- `internal/caddy` 的 Supervisor/进程 Adapter：stdout/stderr pipes、固定行 reader、旧 generation 生命周期。
- `app.go`/ARCH-01 应用 Module：`GetLogTail(source, cursor)` 返回有界结果，不直接打开路径。
- LogsPage：每来源 cursor、rotated/truncated 标记、最多 2,000 行和迟到响应校验。

#### 验收标准

- 对 TunnelBoard 和 Caddy 各连续写入 1 GiB 模拟日志，单来源磁盘始终不超过当前 + 3×5 MiB，两个来源总量不超过 40 MiB 加文件系统微小元数据。
- Windows 连续轮转至少 20 次不因 `.1/.2/.3` 已存在而失败，保留顺序正确。
- 单次写入 10 MiB 无换行内容时，只产生一条不超过 64 KiB 的 truncated 行，进程内存不随输入线性增长。
- 磁盘 writer 阻塞且队列满时，Caddy pipe reader 继续排空；Caddy 不因日志反压卡死，恢复后出现聚合 dropped 标记。
- 100 MiB 既有日志执行一次 Tail，后端读取、分配和响应均不超过规定预算；不调用 io.ReadAll。
- 轮转恰好发生在 Stat/ReadAt 之间时，结果要么来自完整旧 generation，要么返回 Rotated 后的完整新 generation，不拼接两个文件。
- backlog 超过预算时返回最新完整行、Truncated 和准确/保守的 DroppedBytes，NextCursor 后续可继续读取。
- 超长半行跨多次写入和多次 poll 时只发布一次截断记录，不重复、不永久卡住 cursor。
- 日志 sentinel 密码、口令、token、Authorization 和私钥片段在内存环、当前/历史磁盘文件、Tail JSON、页面 DOM 和诊断包中均不存在。
- Caddy 启停和应用 Shutdown 后 pipe reader、queue worker 与文件句柄全部退出；高次数循环下 goroutine 和 handle 数不增长。

#### 后续关联

- `SEC-03/ROUTE-01` 的 CaddySupervisor 负责把当前进程 generation 的 stdout/stderr 接入 LogStore，并在 Stop 时有界排空。
- `SEC-06` 的字段白名单与 redactor 在进入 LogStore 前执行，所有 sink 共用一次安全处理。
- `ARCH-01` 的应用 Module 只暴露有界 GetLogTail，不暴露日志路径或文件读取 Adapter。
- Logs 页面已有 2,000 行前端上限可以保留，但必须改用 per-source generation cursor。

### 4.17 UI-01：以 AppSnapshotStore 区分 loading、真实空数据、stale 和读取失败

#### 状态与结论

- 状态：已实现并验证
- 确认日期：2026-07-21
- 问题结论：成立。当前 `loadVault` 无论首次加载还是已有数据后的刷新失败，都会把 folders、sshHosts、forwards、webRoutes 全部清空且吞掉错误，所有页面随后把后端故障渲染成“暂无数据”。多个 `vault-changed` 还可并发触发加载，迟到旧响应可能覆盖新状态。
- 产品决策：前端建立唯一 AppSnapshotStore Module，显式管理 `idle/loading/ready/refreshing/error`。首次失败显示可重试错误页；刷新失败保留最后成功快照并标记 stale，绝不通过清空数组表示错误。
- 安全决策：stale 时禁止基于旧配置执行 create/edit/delete/move/Route Apply 等 mutation，但保留 Stop Forward、显式退出、诊断导出和只读浏览等安全操作。

#### Snapshot 状态模型

```ts
type SnapshotPhase = 'idle' | 'loading' | 'ready' | 'refreshing' | 'error'

interface SnapshotState {
  phase: SnapshotPhase
  snapshot: AppSnapshot | null
  stale: boolean
  lastError: AppErrorView | null
  lastSuccessAt: number | null
  requestGeneration: number
}
```

- `snapshot=null` 只表示本应用生命周期中尚无成功快照，不等于数据为空。
- `ready + snapshot + collection.length===0` 才允许显示对应实体的真实空状态。
- 首次请求：idle→loading→ready；失败进入 error，snapshot 保持 null。
- 已有快照刷新：ready→refreshing；失败进入 error，但 snapshot 保持最后成功值，stale=true。
- 重试成功：一次性替换完整 snapshot、更新 revision/lastSuccessAt、清除错误和 stale，再进入 ready。
- 最后成功快照只存在于本次 WebView 内存，不写 localStorage/sessionStorage，也不跨应用启动作为当前事实。

AppSnapshot 使用 SEC-06 的无秘密 View，并至少携带后端 Vault revision、Route applied revision、Runtime snapshot generation 和生成时间。页面不能从四个独立请求拼装一个看似一致的快照。

#### Store Interface

```ts
interface AppSnapshotStore {
  state: Readonly<SnapshotState>
  refresh(reason: RefreshReason): Promise<RefreshResult>
  canMutate: ComputedRef<boolean>
}
```

- App.vue 和各页面不再直接调用 GetVaultData，也不再维护四组可独立清空的顶层 ref。
- `refresh(reason)` 是唯一加载入口；reason 区分 startup、after-command、manual-retry、runtime-event 等，仅用于诊断和合并策略。
- `canMutate` 只在存在非 stale 的 ready snapshot 且没有 mutation lock/pending recovery 时为 true。
- 页面接收 snapshot View 或通过 provide/inject 使用 Store；不得重新复制一份可独立漂移的数据源。
- Store 不依赖 Pinia 持久化；使用轻量 Vue composable 即可，避免为了一个全局状态机引入新的持久化面。

#### 并发刷新和迟到响应

1. 每次实际发出请求前递增 requestGeneration，并捕获本次 generation。
2. 响应落地前必须同时确认请求仍是最新 generation，且返回 snapshot revision 不低于当前已接受 revision。
3. refresh 正在执行时收到更多普通 `vault-changed`，只设置 `refreshAgain=true`；当前请求完成后最多再执行一次合并刷新，不并发堆积请求。
4. manual-retry 和 after-command 可以提升优先级，但仍通过同一 generation 规则；旧 startup 响应不能覆盖命令后的新快照。
5. 页面卸载或应用关闭使当前请求 token 失效，迟到结果不再写状态。

后端 revision/token 校验仍是防止 stale write 的最终保障；前端 generation 只保证显示一致性，不能作为并发安全控制。

#### 首次加载体验

- 应用 shell、侧栏和窗口控制可以先显示；主内容区域显示稳定骨架，不渲染 Overview/Forwards/Hosts/Routes 的空态。
- 首次失败显示持久页面级错误：安全错误摘要、重试按钮和导出诊断入口。不能只显示数秒 toast。
- 错误页使用 `role=alert` 或合适的 live region；重试按钮可键盘聚焦，重试开始后显示 busy 状态并防止重复提交。
- 日志、设置中不依赖 Vault 的安全功能可以继续访问；所有需要 snapshot 的新建/编辑动作禁用并说明原因。

#### 刷新失败与 stale 门禁

- 已显示数据后的刷新失败保留页面、选择、筛选和滚动位置，并显示全局持久横幅：“数据可能不是最新，配置修改已暂停”。
- 横幅提供重试、错误详情和诊断入口；成功刷新后自动消失。
- stale 时允许：查看最后快照、复制非敏感信息、查看日志、导出诊断、Stop 单条/全部 Forward、显式退出。
- stale 时禁止：创建/编辑/删除 Host/Forward/Route/Folder、批量移动、Route Apply、导入 Commit、恢复 Commit 和其他依赖当前 revision 的 mutation。
- Stop Forward 仍可用，因为停止是降低网络活动的安全动作；Stop 结果直接依据命令返回，随后刷新失败只会让展示 stale，不能阻止用户继续停止其他已知运行项。
- 如果用户操作需要当前后端事实但快照 stale，统一返回前端 `SnapshotStale` 提示并聚焦重试入口，而不是每个页面自定义错误。

#### 命令成功后的刷新

- mutation 命令的成功/失败与后续 snapshot refresh 是两个独立事实。
- 命令成功后返回 `acceptedRevision` 和结构化业务结果，然后触发 `refresh(after-command)`。
- 刷新成功：展示新快照和成功摘要。
- 刷新失败：明确提示“操作已成功，但界面刷新失败”；保留旧快照、设置 stale、禁用后续 mutation。不能改写成“保存失败”，也不能乐观拼出完整新快照。
- 命令本身失败：不假设数据未变化；若错误声明 `stateMayHaveChanged=true`，仍触发刷新并按结果决定 stale。
- 页面发出的旧 optimistic 状态必须在命令结束时撤销；最终展示只来自命令结果和后端 snapshot。

#### 空状态规则

每个页面的 EmptyState 必须同时满足：

```ts
state.phase === 'ready' &&
!state.stale &&
state.snapshot !== null &&
targetCollection.length === 0
```

refreshing 时可以继续显示已有真实空状态并加轻量刷新指示；error/stale 时不能把旧空状态作为当前事实，必须同时显示错误横幅。首次 loading/error 绝不渲染实体空态。

#### 错误 Interface

- GetSnapshot 返回结构化、安全的 AppErrorView：code、用户可读 message key、retryable、diagnostic ID；不把 SEC-06 的秘密或内部路径带到 WebView。
- Store 保存最近一次错误对象，但错误成功恢复后清除；不累计无限历史。
- toast 只用于瞬时成功反馈，不能承载加载失败/stale 这种持续状态。
- 同一错误连续失败时更新重试时间，不重复堆叠横幅或 toast。

#### 预期代码范围

- 新增 `frontend/src/composables/useAppSnapshotStore.ts` 或等价 Module：状态机、generation、刷新合并、stale 门禁和错误归一。
- `App.vue`：移除 folders/sshHosts/forwards/webRoutes 独立加载和 catch 清空，改为提供 Store 并渲染 loading/error/stale shell。
- Overview/Forwards/Hosts/Routes/Settings：只读取统一 snapshot，mutation 控件接入 canMutate。
- 后端 ARCH-01 GetSnapshot：一次返回无秘密 VaultView、Runtime、Route applied state、设置和 revisions。
- 前端测试：fake 延迟响应、错误、命令结果和 stale 门禁；真实页面验收错误/重试/空态。

#### 验收标准

- 首次 GetSnapshot 失败时页面显示错误和重试，不出现“暂无 Forward/Host/Route/Folder”。
- 已成功加载至少一项后刷新失败，旧数据、选择和滚动仍保留，stale 横幅持续显示。
- 已成功加载真实空集合时才显示空状态；loading/error 不显示。
- 两次请求按 A 先发、B 后发、B 先回、A 后回交错，最终只保留 B；A 的错误也不能覆盖 B 的 ready。
- refresh 中连续触发 100 次 vault-changed，不产生 100 个并发请求，最多当前一次加一次合并刷新。
- 命令成功、刷新失败时成功事实保留并显示 stale；不能提示命令失败或伪造新实体。
- stale 时所有配置 mutation disabled/后端 revision 兜底拒绝，Stop Forward 和退出仍可操作。
- manual retry 成功后 snapshot/revisions 原子更新，错误和 stale 清除，mutation 恢复。
- 快照不写入 Web Storage；GetSnapshot/错误对象符合 SEC-06 响应禁用字段契约。
- 错误页和 stale 横幅可通过键盘访问，读屏能够感知状态变化但不会重复播报风暴。

#### 后续关联

- `SEC-06` 的 VaultView/SSHHostView 是 AppSnapshot 的数据面，任何秘密不得因“保留旧快照”延长驻留。
- `ROUTE-02` 的 desired/applied revision、cleanup pending 和 restore quarantine 由同一 Snapshot 返回。
- `UI-02/UI-03` 的 Route 开关与状态必须消费 SnapshotStore 和结构化 RouteStatus，不再自行乐观推导。
- `ARCH-01` 的 GetSnapshot 是本项后端 Interface；实现顺序上应先建立 DTO/聚合快照，再迁移页面状态机。
- `PERF-02` 页面异步加载后仍共享同一个 Store，不能为每个 lazy page 重复拉取 Vault。

### 4.18 UI-02：Route 开关发送精确意图，并按 desired/applied 结果受控显示

#### 状态与结论

- 状态：已实现并验证
- 确认日期：2026-07-21
- 问题结论：成立。当前 checkbox 的浏览器外观先自行切换，代码却用 `!route.hostsEnabled` 推导目标值；保存或应用失败后不恢复受控状态。前端还依次执行 SaveWebRoute、PreviewRoute、ApplyRoute，并用旧 Route 全字段 payload 实现 hosts/Caddy 联动，可能覆盖并发更新。
- 产品决策：Route 开关显示 desired state，状态徽标显示 applied state。前端只发送用户的精确 flag/checked 意图；所有联动不变量、预览、保存和系统应用由 ROUTE-02 RouteCoordinator 在一个 revision-bound 命令中执行。
- 回滚决策：只有 desired 配置未保存时才恢复开关原值；desired 已保存但系统应用失败时，开关保持新值并显示“已保存但未应用”，提供显式重试。

#### 用户意图命令

前端从 change 事件读取真实 checked，不再对旧 props 取反，也不提交整条 Route：

```ts
interface SetRouteFlagIntent {
  routeId: number
  flag: 'hostsEnabled' | 'caddyEnabled'
  enabled: boolean
  expectedRevision: string
}
```

调用流程固定为：

```text
PreviewRouteChange(intent)
        ↓ 返回 revision-bound token 和完整影响预览
用户确认（如需要）
        ↓
CommitRouteChange(token)
        ↓
RouteCommandResult + SnapshotStore refresh
```

- Caddy enabled=true 自动要求 hosts enabled=true；hosts enabled=false 自动把 Caddy desired 设为 false。规则只在后端一个函数维护和测试。
- Preview 必须列出联动变化、将写入的域名、Caddy/CA 影响、端口冲突预检和当前 revisions。
- 前端不能根据预览自行重建 payload；Commit 只提交不透明 token。
- Preview 取消、token 过期或 revision stale 均发生在 desired 保存前，零副作用。

#### RouteCommandResult

所有可预期的业务结果使用结构化返回，不把“配置已保存但系统应用失败”压缩成一个无法判定副作用的普通异常：

```ts
interface RouteCommandResult {
  outcome:
    | 'applied'
    | 'hosts_only'
    | 'saved_not_applied'
    | 'rejected'
    | 'state_unknown'
  desiredSaved: boolean
  acceptedRevision?: string
  route?: RouteView
  applied?: RouteAppliedView
  error?: AppErrorView
  stateMayHaveChanged: boolean
}
```

- applied：desired/applied 都成功。
- hosts_only：desired 已保存，hosts 已应用，Caddy 因 443 冲突按产品策略降级。
- saved_not_applied：desired 已保存，但非预期系统错误使本 revision 未完整应用或已补偿到上一 applied state。
- rejected：确认取消、stale、校验或 Vault 保存失败，desired 未变。
- state_unknown：执行/补偿结果无法确认，必须进入 UI-01 stale 并阻止后续 mutation。
- 真正的绑定/进程通信错误若无法返回完整结果，前端也按 stateMayHaveChanged=true 的未知结果处理，不能武断回滚。

#### 受控开关状态

每个开关的显示值按以下优先级计算：

```text
当前命令已确认保存的 RouteView
    > 正在 Preview/Commit 的 pending target
    > AppSnapshot 中的 desired value
```

RouteMutationState 至少包含：

```ts
interface RouteMutationState {
  routeId: number
  flag: RouteFlag
  originalValue: boolean
  targetValue: boolean
  phase: 'previewing' | 'confirming' | 'committing' | 'refreshing'
  requestToken: number
  committedRoute?: RouteView
  error?: AppErrorView
}
```

- checkbox 必须是 Vue 受控值；不能依赖浏览器已经改变但数据源未变的 DOM property。
- pending target 仅用于表达正在处理的用户意图，不代表已保存。控件同时显示 busy，不能把 pending 当成功。
- Preview 取消、rejected 或明确 desiredSaved=false：清除 pending/committed overlay，checked 自动恢复 snapshot 原值。
- desiredSaved=true：使用返回的权威 RouteView 作为临时 committed fact，直到 SnapshotStore 接受不低于 acceptedRevision 的新快照。
- state_unknown：不选择旧值或目标值冒充事实；保留最后已知显示、标记 unknown/stale 并禁用控件。

#### 全局串行交互

ROUTE-02 后端虽然会串行化事务，前端仍一次只允许一个 Route mutation：

- 任一 Route 正在 preview/confirm/commit/refresh 时，禁用所有 Route 开关、编辑、删除和新建按钮。
- 这样不会让第二个用户操作立即使第一个确认 token stale，也避免多个 UAC/确认对话框堆叠。
- Stop Forward、页面导航、日志和诊断不受此 Route mutation 门禁影响。
- 控件 disabled 原因通过可见 busy 文本和 `aria-describedby` 提供，不能只改变颜色。
- request token 防止旧 Preview/Commit 响应在用户取消或页面切换后重新打开确认框、覆盖新结果。

#### 各结果的界面语义

- Preview/确认取消：恢复原值，不显示失败 toast；如用户主动取消，可用轻量中性反馈。
- 校验或 Vault 保存失败：恢复原值，在该 Route 行显示持久错误和重试入口。
- applied：显示新 desired 值和 applied 状态，再刷新 Snapshot。
- hosts_only：hosts/Caddy desired 开关按保存结果显示；状态区明确显示 hosts 已生效、Caddy 端口冲突，不显示“全部成功”。
- saved_not_applied：开关保持新 desired 值，状态区显示“已保存但未应用”，提供 ReconcileCurrent 重试。
- Snapshot refresh 失败：保留命令返回的 committed Route fact，显示 UI-01 全局 stale 横幅并暂停后续 mutation；不能把命令改报为失败。
- refresh 成功且 revision 达标：移除临时 overlay，完全以 Snapshot desired/applied 为准。

#### 保存弹窗与删除的一致性

- RouteModal 保存也走 PreviewRouteChange/CommitRouteChange，不再 Save 后由页面拼接 Apply。
- 编辑弹窗在 Commit 成功前保持可恢复表单；确认域名覆盖时不提前关闭并丢弃上下文。
- 删除使用同一个 RouteCoordinator 命令；cleanup_pending 属于已接受删除结果，界面按 ROUTE-02 本机 tombstone 显示，而不是重新插回一个已从 desired 删除的 Route。
- 所有路径共享相同 acceptedRevision、stateMayHaveChanged 和 Snapshot refresh 规则。

#### 可访问性

- checkbox pending 时设置 `aria-busy=true`，并通过相邻 live region 宣布“正在预览/正在应用”。
- 行内错误与对应开关通过 `aria-describedby` 关联；重试按钮可键盘访问。
- 确认弹窗的焦点、Escape 和恢复行为最终复用 UI-06 BaseDialog；取消后焦点返回原开关。
- 快速 Space/Enter 连击在 busy 时不产生第二个命令，也不播放重复状态。

#### 预期代码范围

- RoutesPage：受控 checked、RouteMutationState、全局 mutation 门禁、结构化结果和 inline error；删除 Save→Preview→Apply 调用链。
- 后端 RouteCoordinator：SetRouteFlag intent、联动不变量、Preview/Commit token 和 RouteCommandResult。
- AppSnapshotStore：acceptedRevision、临时 committed fact、refresh-after-command 和 stale 处理。
- RouteModal/删除流程：迁移到同一命令 Interface。
- 测试覆盖事件 checked、取消、部分成功、未知结果、迟到响应和键盘操作。

#### 验收标准

- snapshot=false，用户 change.checked=true，发送的 intent 必为 true；不依赖 `!routeValue`。
- Preview 取消、token stale、校验失败和 Vault 保存失败时 checked 恢复原值，系统零副作用。
- desired 保存成功但 Caddy Apply 失败时 checked 保持新值，显示 saved_not_applied 和重试；不能回滚为旧配置。
- 443 冲突时 hosts/Caddy desired 保持开启，状态明确 hosts_only/port conflict，不显示 stopped 或完整 applied。
- Caddy 开启联动 hosts、hosts 关闭联动 Caddy 均由后端结果体现；前端没有提交旧整条 Route。
- 快速点击同一或不同 Route 开关只产生一个 active Preview/Commit；busy 时后续输入被忽略且视觉不抖动。
- Commit 成功但 refresh 失败时保留 committed Route fact、进入 stale 并禁用 mutation；重试刷新后 overlay 清除。
- stateMayHaveChanged/通信中断时不盲目回滚，进入 unknown/stale，后端 revision 刷新后恢复。
- 旧 request token 的迟到 Preview/Commit 不打开弹窗、不更改 checked、不覆盖新错误。
- 键盘、读屏和焦点返回路径可完成开关、确认、取消和重试。

#### 后续关联

- `ROUTE-02` 是本项唯一后端事务来源，RouteCommandResult 必须同时报告 desired 和 applied 事实。
- `UI-01` SnapshotStore 负责 acceptedRevision、stale 门禁和命令后刷新，本项不维护第二份长期 Route store。
- `UI-03` 负责把 applied/hosts_only/pending/error/unknown/cleanup_pending/quarantined 映射为不误导的状态展示。
- `UI-06` 提供确认弹窗的统一可访问性语义。
- `ARCH-01` 的应用 Module 应暴露有类型 PreviewRouteChange/CommitRouteChange，不保留旧 Save/Apply 顺序。

### 4.19 UI-03：以明确的 desired/applied 状态枚举替代布尔值默认 false

#### 状态与结论

- 状态：已实现并验证
- 确认日期：2026-07-21
- 问题结论：成立。当前 RoutesPage 使用 `statusOf(id)?.hostsApplied/caddyRunning ? running : stopped`；状态尚未加载、请求失败、条目缺失和真实 false 全部落入同一个 stopped 分支。GetRouteStatus 又在查询时调用端口诊断，并仅凭 Caddy 全局进程和 Vault 指纹推导每条 Route 状态，无法证明对应 revision 实际生效。
- 产品决策：Route 开关只表达 desired state；状态区域只表达 applied state。后端返回明确枚举和 revisions，前端不得把 null/undefined/请求失败强制转换为 false。
- 数据决策：Route applied 状态并入 UI-01 的 AppSnapshot，不再由 RoutesPage 自建 statusMap 和静默失败的独立五秒轮询。

#### 状态模型

整体状态与单项副作用状态分开，避免把 hosts_only 强行用于 CA 等无关效果：

```ts
type RouteOverallState =
  | 'disabled'
  | 'checking'
  | 'pending'
  | 'applying'
  | 'applied'
  | 'hosts_only'
  | 'conflict'
  | 'error'
  | 'cleanup_pending'
  | 'quarantined'
  | 'unknown'

type RouteEffectState =
  | 'disabled'
  | 'checking'
  | 'pending'
  | 'applying'
  | 'applied'
  | 'conflict'
  | 'error'
  | 'quarantined'
  | 'unknown'

interface RouteAppliedView {
  routeId?: number
  desiredRevision?: string
  appliedRevision?: string
  overall: RouteOverallState
  hosts: RouteEffectState
  caddy: RouteEffectState
  ca: RouteEffectState
  retryable: boolean
  error?: AppErrorView
  observedAt: string
  stale: boolean
}
```

- cleanup tombstone 对应的 desired Route 已不存在，因此 routeId 可以缺省；它通过独立 cleanup item ID 展示。
- appliedRevision 只在 RouteCoordinator 确认相应完整目标或明确 degraded 状态落盘后更新。
- stale 是“该 applied observation 可能已过期”，不能改变原始 overall 枚举；UI 同时展示原状态与过期标记。
- error 只包含安全错误 code/message key/失败步骤/diagnostic ID，不含 SEC-06 禁止字段。

#### 枚举语义

- `disabled`：用户明确关闭对应 desired 效果，显示“未启用”，不是“已停止”。
- `checking`：首次 Snapshot 尚在读取本机 applied state，尚无可依赖历史事实。
- `pending`：desired 已保存，但尚未执行、等待显式激活或排队 reconcile。
- `applying`：当前 RouteCoordinator transaction 正在处理该 desired revision。
- `applied`：对应 desired revision 的全部要求已经由 Adapter 确认生效。
- `hosts_only`：整体的预期降级状态；hosts 已应用，Caddy 因已确认的 443 冲突未启动，CA 不被错误标记为完整生效。
- `conflict`：managed hosts digest、443 所有权或其他明确外部冲突，不能当普通 stopped。
- `error`：已知 Apply/补偿/进程异常失败，具有失败步骤和可重试信息。
- `cleanup_pending`：desired 已删除，但 applied/journal 仍记录旧 managed hosts、Caddy 或 CA 待清理。
- `quarantined`：DATA-01 restore quarantine 阻止任何网络副作用；配置仍存在但未激活。
- `unknown`：无法可靠读取 applied state、journal 损坏、进程 ownership 不明或请求失败且无历史状态。

状态缺失的默认规则是 checking/unknown，绝不默认 disabled、stopped 或 not-applied。

#### 后端事实来源

- desired 来自当前 Vault revision。
- hosts applied 来自 ROUTE-02 RouteAppliedState 的 managed digest，并与实际 managed 区块查询比对；无法读取时为 unknown。
- Caddy applied 来自 CaddySupervisor 自有进程 generation、最后成功配置 digest 和 applied revision；仅检测进程存在不足以证明某条 Route 已加载。
- CA applied 来自当前用户证书库实际查询结果与 RouteAppliedState 登记指纹；旧指纹字段本身不能证明信任仍存在。
- port conflict 只来自 CaddySupervisor 最近一次针对该 revision 的真实 Apply/启动结果。Status 查询不能调用 DiagnosePort/bind 探测重新猜测。
- pending/applying/error/cleanup 来自 RouteCoordinator journal 和 applied state，而不是前端根据时间推断。

`Status()` 是只读 Interface：不写 hosts、不重载/停止 Caddy、不信任 CA、不修改 journal。修复或重新应用只能通过显式 ReconcileCurrent 命令。

#### Snapshot、事件和兜底刷新

- GetSnapshot 一次返回 Route desired views、RouteAppliedViews、Route applied revision 和 journal/recovery 摘要。
- CaddySupervisor 异常退出、Route transaction phase 变化、CA/hosts 观察结果变化时发布 typed `route-status-changed` 事件，仅携带 revision/generation 和需要刷新的提示，不携带秘密。
- AppSnapshotStore 收到事件后按 UI-01 generation 规则合并 refresh，不能直接把事件 payload 当完整状态。
- 可以保留低频兜底刷新以修复事件丢失，但经过 SnapshotStore 单飞/合并，失败设置 stale 并保留最后成功状态。
- 页面挂载/卸载不创建自己的五秒 statusMap 定时器；lazy page 重新进入也复用全局 Snapshot。

#### 显示规则

每条 Route 至少显示：

```text
期望配置：Hosts 已启用 / Caddy 已启用
实际状态：Hosts 已应用 / Caddy 端口冲突 / CA 未需要或未信任
总体状态：仅 Hosts 生效
```

- disabled 使用中性样式和“未启用”。
- checking/pending/applying 使用信息型样式和明确动词。
- applied 使用成功样式。
- hosts_only/conflict/quarantined 使用警告样式，并显示原因。
- error 使用错误样式和可重试/诊断入口。
- unknown 使用问号图标和“状态未知”，不能使用停止图标。
- stale 在原 chip 旁追加“可能已过期”，降低视觉强调但不抹掉最后事实。
- 颜色不是唯一线索；每个状态必须有文本和图标。

#### 全局与逐项状态

Caddy 配置和 hosts managed block 都是全量目标，不是真正的逐 Route 独立进程：

- 行内状态帮助定位每条 desired Route 是否包含于最后 applied revision。
- 全局 Route 横幅显示当前 RouteCoordinator 状态、journal、Caddy generation 和整体重试动作。
- “重试”实际调用全量 ReconcileCurrent，应标为“重试应用全部 Route”，不能在单行按钮上暗示只影响该 Route。
- cleanup pending 在独立区域展示旧域名/副作用摘要和全量修复入口，即使原 Route 已从列表消失。
- 单条错误详情可以展开，但执行仍按完整 Vault 目标 reconcile，以保持 ROUTE-02 事务一致性。

#### 异常退出和手工修改

- desired Caddy 已启用但自有进程异常退出：overall=error 或 pending recovery，caddy=error；不能显示普通 stopped。
- 用户手工删除 CurrentUser CA：ca=error/pending（按是否可重试），整体不再是完整 applied。
- 用户手工修改 TunnelBoard managed hosts 区块：hosts=conflict，禁止自动覆盖，要求显式 Reconcile。
- 用户只修改区块外 hosts 内容：不改变 applied 状态；下一次写入仍保留外部内容。
- Caddy 进程在状态读取瞬间退出：Supervisor generation 变化触发事件；旧 Snapshot 标记 stale，直到刷新确认。

#### 可访问性

- 状态摘要区域使用 `aria-live=polite`，只在 overall 或失败步骤实际变化时播报，不因定时刷新重复朗读。
- 图标设置可访问名称或与可见文本组合；纯装饰图标 aria-hidden。
- 展开错误和重试按钮可键盘访问；焦点不会因后台状态刷新跳走。
- checking 使用非强制播报；error/unknown 首次出现时播报一次。

#### 预期代码范围

- 后端 RouteCoordinator/RouteAppliedState：明确 overall/effect 枚举、revisions、observedAt、retryable 和安全错误。
- CaddySupervisor/CurrentUser CA/hosts Adapter：提供只读事实，不在 Status 路径执行修复。
- ARCH-01 GetSnapshot/事件：聚合 RouteAppliedView 和 route-status-changed 通知。
- RoutesPage：删除 statusMap、GetRouteStatus 独立轮询和布尔 fallback，改用状态映射与全局 reconcile 横幅。
- StatusChip/i18n：为全部枚举提供文本、图标、样式和 stale 辅助说明。

#### 验收标准

- 首次状态尚未返回时显示 checking；请求失败且无历史状态时显示 unknown，二者都不显示 stopped/not-applied。
- 请求失败但存在最后状态时保留原枚举并标记 stale；成功刷新后 stale 清除。
- desired 全关闭显示 disabled/未启用，不使用 stopped。
- desired 启用且对应 revision 完整生效时才显示 applied。
- 443 冲突显示 hosts applied、caddy conflict、overall hosts_only；CA 不被错误显示为完整可信。
- Caddy 自有进程异常退出后显示 error/pending recovery，旧 applied 不继续保持绿色。
- CurrentUser CA 被手工删除或 managed hosts 区块被手工修改后，下一次观察分别显示 CA error/hosts conflict。
- restore quarantine 显示 quarantined，应用重启后仍保持，不能被启动轮询改成 pending/applied。
- desired Route 删除但清理失败时独立 cleanup_pending 项仍可见并可触发全量修复。
- Status 查询期间所有 fake Adapter 断言没有写 hosts、Apply/Stop Caddy、信任 CA 或更新 Vault/journal。
- 状态事件风暴被 SnapshotStore 合并；页面无独立高频轮询，迟到状态不能覆盖新 revision。
- 所有枚举在五种 locale 下有文案，键盘/读屏能够理解状态和全量重试影响。

#### 后续关联

- `ROUTE-02` 的 desired/applied/journal 是本项唯一事实来源，Status 保持严格只读。
- `UI-01` 负责 Snapshot loading/error/stale 和事件刷新，本项只定义 Route 状态内容及呈现。
- `UI-02` 负责开关的 desired 语义与命令中间态；本项负责 applied 结果，不重复维护 mutation state。
- `DATA-01` 的 restore quarantine 直接映射为 quarantined，不能降级成普通 pending。
- `ARCH-01` 的 GetSnapshot/SubscribeRuntimeEvents 必须携带 revisions，不能恢复成页面自行轮询多个布尔值。

### 4.20 UI-04：批量移动收敛为单次 Vault 事务，并把选择目标与执行动作分开

#### 状态与结论

- 状态：已实现并验证
- 确认日期：2026-07-21
- 问题结论：成立。当前前端按选中 ID 循环调用单项 `MoveForward`；每一项都是独立 Vault Update，任一中途失败都会留下部分成功。刷新又只发生在整个循环成功之后，因此失败时界面可能继续展示旧位置，既不能说明实际移动了哪些项，也无法可靠重试。
- 产品决策：批量移动必须是后端单命令、单次 Vault 事务、全有或全无。目标文件夹的选择不再自动触发写入，用户先选择目标，再通过明确的“移动”按钮提交并确认影响范围。
- 运行时决策：文件夹仅是管理和展示分组，移动不停止、不重启、不重建正在运行的 Forward，也不改变其连接参数。

#### 应用命令 Interface

由应用 Module 暴露有类型命令，不让页面循环调用单项业务方法：

```go
type MoveForwardsCommand struct {
    ForwardIDs    []int  `json:"forwardIds"`
    TargetFolderID int   `json:"targetFolderId"`
    ExpectedRevision string `json:"expectedRevision"`
}

type MoveForwardsResult struct {
    MovedIDs         []int  `json:"movedIds"`
    UnchangedIDs     []int  `json:"unchangedIds"`
    AcceptedRevision string `json:"acceptedRevision"`
}
```

- `ForwardIDs` 必须非空；去重后的数量上限为 5000，超过时在读取和复制大量对象前拒绝。
- 后端去重并保持稳定顺序，重复 ID 不导致重复写入或重复结果。
- `TargetFolderID` 必须在同一份待更新 Vault snapshot 中存在。
- 所有 Forward ID 必须存在。只要有一个未知、已删除或不属于当前 revision，整个命令失败，零项移动。
- `ExpectedRevision` 必须与当前 Vault revision 一致；过期命令返回结构化 revision conflict，不能把旧选择应用到新数据。
- 已经位于目标文件夹的项放入 `UnchangedIDs`；其余放入 `MovedIDs`。全是 unchanged 时可返回当前 revision，不制造无意义写盘和事件。
- 返回结果只包含 ID 和 revision，不返回 SEC-06 禁止的持久秘密或完整内部模型。

#### 原子事务边界

CatalogBiz 在一次 `VaultStore.Update` 回调中完成：

1. 校验 ExpectedRevision。
2. 建立 Folder/Forward ID 索引。
3. 校验目标文件夹和全部去重后的 Forward。
4. 计算 moved/unchanged 集合。
5. 只有全部校验通过后才修改所有目标对象。
6. 由 VaultStore 原子保存一次并生成新 revision。

任何校验、序列化、临时文件写入或原子替换失败，都不得提交部分对象。CatalogBiz 不直接逐项调用现有 `MoveForward`，因为那会重新引入多个事务 seam。单项移动可以改为调用同一命令并传一个 ID，避免两套不变量漂移。

#### 前端交互

- 批量工具栏中的目标文件夹控件只更新本地 `pendingTargetFolderID`，`change` 事件不调用后端。
- 用户选择目标后，显示明确的“移动 N 项”按钮；未选择目标、选择为空、Snapshot stale 或已有批量命令执行中时禁用。
- 点击按钮打开统一 UI-06 确认框，显示数量和目标文件夹名称；确认后冻结本次 ID、目标和 expectedRevision 快照。
- 命令执行期间保持一次 busy 状态，禁止重复点击、改变目标或修改选中集合；页面其他 mutation 同样受 UI-01 门禁约束。
- 成功后清空已移动/未变化项的选择，应用命令返回的 acceptedRevision 临时事实，并触发一次 AppSnapshotStore refresh。
- 后端失败时保留原选中集合和目标，展示一个可重试的汇总错误，不按条弹出大量 toast。
- 命令已成功但 refresh 失败时，不能把移动改报为失败；记录 acceptedRevision，进入 UI-01 stale，提示“移动已保存，列表可能不是最新”，暂停后续配置 mutation，直到刷新成功。
- revision conflict 时保留选择但先刷新；刷新后已不存在的 ID 从选择中移除，并要求用户重新确认新的目标和影响数量，不能自动重放旧命令。

#### 并发与事件

- 同一应用实例一次只允许一个 Vault mutation 通过应用 Module 执行；后端仍以 ExpectedRevision 和 VaultStore 原子写作为最终保护，不能只依赖前端 busy。
- 后端提交成功后发布一次 snapshot-invalidated/revision-changed 通知，不为每个 Forward 发布一次刷新风暴。
- 迟到的旧命令或旧 refresh 受 UI-01 request generation 保护，不得覆盖更新 revision。
- 外部或恢复流程使目标文件夹消失时，旧命令整体失败；不得自动移动到默认文件夹。

#### 预期代码范围

- `internal/biz/catalog.go`：新增 `MoveForwards`，把目标/全部 ID 校验和修改放入一次 Store.Update；单项入口复用该实现。
- `internal/vault`：Update/Save 返回或可读取 accepted revision，并保持写失败零提交语义。
- `app.go`/ARCH-01 应用 Module：绑定 `MoveForwardsCommand -> MoveForwardsResult`，执行统一 mutation lock 和结构化错误转换。
- `frontend/src/components/pages/ForwardsPage.vue`：删除逐项 await 循环和 select-change 自动提交，改为 pending target、显式按钮、单次命令和汇总反馈。
- AppSnapshotStore：接收 acceptedRevision、合并一次刷新并处理成功后刷新失败的 stale 状态。
- UI-06 BaseDialog：提供确认数量/目标、焦点和 busy 语义。

#### 验收标准

- 选择 100 个 Forward，其中一个 ID 不存在：返回整体失败，99 个有效项也全部保持原文件夹。
- 目标文件夹在命令提交前被删除：revision conflict 或 target-not-found，零项移动且不回退到其他文件夹。
- Vault 保存/原子替换故障注入：重新加载后所有项都在原文件夹，不存在内存成功、磁盘部分成功。
- 输入含重复 ID：每项最多移动一次，结果稳定且没有重复事件。
- 全部或部分项已经在目标文件夹：分别进入 UnchangedIDs，其余项一次提交；全 unchanged 不产生新 revision。
- 两次快速点击、两个窗口或迟到旧命令：最多一个 revision 成功，另一命令明确冲突，不覆盖更新选择。
- 移动正在运行的 Forward 后，listener、SSH connection generation 和 runtime status 均不变化，只有 folderId 和列表分组变化。
- 命令成功且 refresh 成功：选择清空、列表显示新分组、revision 前进一次。
- 命令失败：选择和目标保留，页面与重新加载后的 Vault 一致，可修正后重试。
- 命令成功但 refresh 失败：显示已保存 + stale，不显示失败、不再次提交，刷新恢复后状态收敛。
- 键盘用户可以依次选择目标、触发移动、确认和回到批量工具栏，读屏能听到数量、目标、成功或失败摘要。

#### 后续关联

- `UI-01` AppSnapshotStore 负责 stale mutation 门禁、acceptedRevision 临时事实和命令后的单次刷新。
- `UI-06` 提供可访问确认框；本项不重复实现 Modal 状态机。
- `ARCH-01` 的应用 Module 必须把原子 MoveForwards 作为唯一外部 Interface，前端不得继续循环调用细粒度 MoveForward。
- Catalog/Vault seam 必须保持单次 Update 的事务深度；不能把“批量”只包装在 Wails 方法表面、内部仍逐项保存。

### 4.21 UI-05：文件夹导航采用嵌套列表与独立原生按钮，不引入不完整的 ARIA Tree

#### 状态与结论

- 状态：已实现并完成键盘语义验证
- 确认日期：2026-07-21
- 问题结论：成立。当前顶层和子文件夹都使用无语义的 `div @click` 作为选择入口，不能通过 Tab、Enter 或空格操作，也没有向读屏暴露当前选中项。文件夹行内部又包含新增、删除按钮，继续依赖整行 click 和 `@click.stop` 会让交互关系脆弱。
- 产品决策：文件夹层级最多两层，采用语义化嵌套 `ul/li` 加原生 button；不使用 `role=tree/treeitem`，因此无需承担方向键、Home/End、展开折叠和 roving tabindex 等完整 Tree Widget Interface。
- 交互决策：文件夹选择与行操作拆成并列按钮。整个视觉行仍可保持大点击区域，但 DOM 中不形成按钮嵌套，也不依赖阻止冒泡区分动作。

#### 语义结构

建议结构：

```html
<nav aria-labelledby="forward-folders-title">
  <h2 id="forward-folders-title">文件夹</h2>
  <ul class="folder-list">
    <li>
      <div class="folder-row">
        <button type="button" class="folder-select" aria-current="page">
          <span>开发环境</span>
          <span aria-hidden="true">5</span>
          <span class="visually-hidden">5 个转发</span>
        </button>
        <button type="button" aria-label="在开发环境中新建子文件夹">…</button>
        <button type="button" aria-label="删除文件夹：开发环境">…</button>
      </div>
      <ul class="folder-list child-list">…</ul>
    </li>
  </ul>
</nav>
```

- `nav` 使用可见标题建立名称，不能只依赖图标。
- 每个文件夹名称和数量由一个 `folder-select` 原生按钮承载，负责唯一的选择动作。
- 当前项设置 `aria-current="page"`；未选中项不输出该属性。选中状态同时有文本/图标或明显轮廓，不能只用颜色。
- 数量的视觉文本可 `aria-hidden`，另提供本地化的完整读屏文本，避免读成无上下文数字。
- 新建子文件夹、删除是 `folder-select` 的兄弟按钮，名称包含具体文件夹，禁止嵌套 button。
- CSS Grid 让 `folder-select` 覆盖图标、名称和数量区域，行操作保持紧凑；不再使用父行 click 或 `@click.stop`。
- 子列表是顶层 `li` 的真实后代，保持两层关系。当前没有折叠功能，不添加虚假的 `aria-expanded`。

#### 键盘和焦点规则

- Tab/Shift+Tab 在文件夹选择按钮和可用行操作之间移动；Enter/空格使用浏览器原生按钮行为。
- 不劫持方向键。若未来产品需要真正的树形键盘导航，再把整个 Module 一次性升级为完整 ARIA Tree，而不是只添加 role。
- 选择文件夹只更新当前内容过滤条件，不抢焦点、不把焦点跳到列表内容；读屏通过 `aria-current` 和内容区标题感知变化。
- 新建文件夹成功后，选择新文件夹，并在 DOM 更新后把焦点移到它的选择按钮。
- 删除当前子文件夹成功后，选择并聚焦父文件夹；删除当前顶层文件夹成功后，优先选择/聚焦下一个同级，否则上一个同级，否则第一个剩余文件夹。
- 删除非当前文件夹后，焦点优先移动到同位置的下一个/上一个操作对象，同时保持当前选择不变。
- 删除失败或取消时焦点回到原删除按钮；确认弹窗的捕获与恢复由 UI-06 负责。
- Snapshot refresh 保留仍存在的 selectedFolderId 和 focusedFolderId；只有 ID 已不存在时才按上述确定性规则回退。

#### 与 stale、busy 和权限状态的关系

- UI-01 stale 时仍允许选择文件夹、搜索、展开只读详情和停止运行项；浏览操作不属于配置 mutation。
- stale、全局 Vault mutation busy 或文件夹命令执行中时，禁用新增、删除和 UI-04 批量移动按钮，并给出可访问原因；文件夹选择按钮保持可用。
- 删除/创建命令不能通过禁用整条 folder row 顺带让当前选择失去焦点。
- 后端失败时保留选择和焦点，错误摘要由稳定的 live region 宣告一次，不重复朗读。

#### 组件职责

- 新增轻量 `FolderNavigation.vue` Module，Interface 只接收 folders、counts、selectedFolderId 和 mutation 状态，输出 `select/create-child/delete` 意图。
- FolderNavigation 不读取 Vault、不调用 Wails、不自行保存，也不持有第二份长期选择状态；ForwardsPage/AppSnapshotStore 仍是数据和命令编排者。
- 文件夹行可以作为 FolderNavigation 的私有实现拆分，但不扩大外部 Interface。这样可访问语义、焦点回退和两层渲染集中在一个 seam，避免顶层和子层模板继续漂移。
- 如果当前改动规模不值得立即抽文件，也必须先以同一模板/辅助函数表达相同语义；ARCH-02 重构时再移动到独立文件，不能以“以后抽取”为由继续保留不可访问 div。

#### 预期代码范围

- `frontend/src/components/pages/ForwardsPage.vue`：把 folder-row click div 改成嵌套列表、选择按钮与兄弟操作按钮，增加稳定 focus key/ref 管理。
- 可选 `frontend/src/components/forwards/FolderNavigation.vue`：封装两层导航、可访问名称和焦点恢复。
- i18n：增加“某文件夹，N 个转发”“在某文件夹中新建子文件夹”“删除文件夹：某名称”和禁用原因等五种 locale 文案。
- UI-01 SnapshotStore：刷新时保留/校正 selectedFolderId；stale 仅禁 mutation。
- UI-06 BaseDialog：删除确认关闭后恢复到原操作按钮，本项只决定删除成功后的下一焦点。

#### 验收标准

- 仅使用键盘可以选择任意顶层/子文件夹，创建子文件夹并删除文件夹；不需要鼠标，也不依赖模拟 click 的自定义 keydown。
- 读屏能获知导航名称、文件夹名、Forward 数量、当前项和每个行操作的具体对象。
- DOM 不存在交互元素嵌套、无语义 click div、重复 ID 或仅靠 `@click.stop` 区分动作。
- 当前项同时具有 `aria-current` 和清晰视觉状态；高对比度和 200% 缩放下仍能分辨。
- 新建成功后焦点位于新文件夹；删除当前子项回到父项；删除当前顶层项按 next/previous/first 规则稳定落点。
- 删除失败/取消后选择不变，焦点恢复到原按钮；后台 refresh 不会无故抢走焦点。
- stale 状态下文件夹切换仍可用，但创建、删除和移动禁用并说明原因。
- 自动化测试覆盖 Tab/Enter/空格、aria-current、动态可访问名称、创建/删除后的焦点和 snapshot 删除选中项的回退。
- 使用 axe 或等效规则检查无 button nesting、无无名按钮、列表结构有效，并辅以至少一次真实键盘/读屏冒烟。

#### 后续关联

- `UI-01` 决定 stale 时的只读可用性以及 refresh 后选择 ID 的保留/回退。
- `UI-04` 的目标选择和批量移动按钮应复用相同文件夹名称与 ID 事实，但不复用 FolderNavigation 的交互状态。
- `UI-06` 负责删除确认的焦点捕获、陷阱、Escape 和关闭恢复；本项负责命令成功后新的业务焦点落点。
- `ARCH-02` 可把重复文件夹模板收敛成 FolderNavigation Module；其外部 Interface 保持意图驱动，不暴露 Wails Adapter。

### 4.22 UI-06：以 BaseDialog 统一语义、焦点、关闭和 busy 生命周期

#### 状态与结论

- 状态：已实现并完成焦点、Escape、busy 与恢复焦点测试
- 确认日期：2026-07-21
- 问题结论：成立。当前 ConfirmDialog、Host/Forward/Route Modal、HostKeyDialog、文件夹弹窗和 DNS 确认分别复制 overlay/dialog-card；除 Settings 更新结果外普遍缺少 `role=dialog`、`aria-modal` 和标题关联，所有实现都没有完整焦点陷阱、Escape、背景 inert、滚动锁和关闭后焦点恢复。重复实现使修复无法覆盖全部入口。
- 架构决策：新增一个深的 `BaseDialog` Module，把 WebView 对话框基础设施隐藏在小 Interface 后；ConfirmDialog 等领域弹窗只负责内容、动作和业务状态。
- 产品决策：正常流程一次只呈现一个对话框，不设计嵌套 Modal。若状态错误导致多个同时打开，内部 DialogStack 只允许最上层响应键盘和读屏，并在开发环境告警。
- 关闭决策：非 busy 时允许 Escape，点击遮罩不关闭；所有关闭都经过 `request-close`。busy 时禁止 Escape、关闭按钮和重复提交，避免把结果未知的操作伪装成取消。

#### BaseDialog Interface

```vue
<BaseDialog
  :open="visible"
  :title="title"
  :description="description"
  kind="form | confirm | danger | info"
  size="sm | md | lg"
  :busy="busy"
  @request-close="onClose"
>
  <template #default>...</template>
  <template #actions>...</template>
</BaseDialog>
```

- `open` 为受控输入；BaseDialog 不私自修改调用方业务状态。
- `request-close` 携带 `escape|button` 原因，调用方可以处理未保存表单，但不能绕开 busy 保护。
- title 为必填可见文本；BaseDialog 自动生成稳定实例 ID，并设置 `aria-labelledby`。可选 description 自动关联 `aria-describedby`。
- kind 决定初始焦点策略和基础视觉语义，不决定业务按钮文案或命令。
- size 只表达布局预算，不允许调用方覆盖定位、z-index、focus trap 或 inert 实现。
- body/actions 使用 slot；动作按钮遵循少量内部标记约定，领域包装 Module 负责添加，普通页面不需要学习焦点实现细节。
- BaseDialog 不调用 Wails、不执行业务命令、不展示通用成功 toast；其 Interface 只管理对话框生命周期。

#### DOM 与语义

- 使用 Vue Teleport 把 overlay 渲染到 `body`，避免页面容器的 overflow、transform 和 stacking context 截断弹层。
- 内容容器设置 `role="dialog"`、`aria-modal="true"`、`aria-labelledby`；有描述时设置 `aria-describedby`。
- 标题始终可见。纯装饰图标 `aria-hidden`；危险类型仍需文本说明，不能只靠红色或警告图标。
- 对话框内容超高时滚动 body 区域，标题和动作区保持可见；200% 缩放和最小窗口仍可访问全部操作。
- 打开时将应用根节点设为 `inert`，并对兼容性需要同步处理可访问树隐藏；由于弹窗已 Teleport 到 body，不会把弹窗自身隐藏。
- body 滚动锁、inert 和事件监听使用引用计数/栈管理，并在异常卸载时清理，不能永久锁死页面。

#### 焦点生命周期

1. `open: false -> true` 前记录当前 `document.activeElement`，仅接受仍连接、可聚焦的元素作为返回目标。
2. DOM 完成后按 kind 选择初始焦点：
   - form：第一个 `aria-invalid=true` 字段，否则第一个可编辑字段；
   - confirm：取消按钮；
   - danger：取消按钮，Host Key mismatch 也不得默认落到“替换并启动”；
   - info：关闭按钮；
   - 没有可聚焦子项时聚焦带 `tabindex=-1` 的 dialog 容器。
3. Tab/Shift+Tab 每次按键重新计算当前可见且未禁用的 tabbable 集合，支持表单字段动态出现/消失；焦点在首尾循环。
4. 非最上层 Dialog 和 inert 背景不能获得焦点；若焦点因 DOM 更新逃逸，拉回当前 Dialog。
5. 关闭并完成 DOM 更新后，若原触发元素仍连接且可用则恢复焦点。
6. 原触发元素因成功删除而消失时不强行聚焦 body；由 UI-05 等领域 Module 在关闭后选择父项/相邻项。两者通过“原元素不存在则 BaseDialog 不恢复”的规则避免抢焦点。

#### Escape、busy 与提交

- 只有栈顶 Dialog 处理 Escape；非 busy 时发出一次 request-close，busy 时忽略并保持明确的进行中状态。
- 点击 overlay 不关闭，避免误丢表单、危险确认或恢复预览；所有取消动作都有可见按钮。
- 领域表单使用原生 `<form @submit.prevent>`。单行字段 Enter 可提交，多行 textarea 保持换行；不再在个别 input 上散落 `@keyup.enter`。
- 提交开始后立即设置 busy，主按钮显示进度并禁用全部会触发第二次 mutation 的动作；BaseDialog 设置 `aria-busy=true`。
- 命令成功后由调用方关闭；失败时保持 Dialog、保留用户输入并在稳定错误区显示。不能像当前 DNS 确认那样先关闭再执行，使失败只剩 toast。
- busy 不能无限持续：底层应用命令必须遵守各问题已确定的 deadline/结构化未知结果；BaseDialog 不自行猜测命令是否完成。

#### 专用包装 Module

- `ConfirmDialog` 基于 BaseDialog，Interface 提供 title/message/confirm label/variant/busy/showCancel，自动生成取消优先焦点和一次性 confirm。
- DNS 覆盖、Host Key、恢复预览等需要富内容的确认可以使用 ConfirmDialog 的 body slot，但复用相同生命周期。
- HostModal、ForwardModal、RouteModal 使用 BaseDialog(kind=form)，保留领域表单校验，不复制 overlay。
- 文件夹创建使用 BaseDialog(kind=form) 和原生 form submit。
- Settings 更新结果使用 BaseDialog(kind=info)，删除独立 Bootstrap modal/backdrop 实现。
- 所有包装 Module 的 close/confirm/submit 都是意图事件，不直接实例化 Wails Adapter；页面或应用 Module 编排业务命令。

#### 错误与读屏通知

- 对话框级错误放在稳定容器中，使用 `role=alert` 或适度 `aria-live`，只在错误实际变化时播报一次。
- 字段错误仍由领域表单设置 `aria-invalid` 和 `aria-describedby`；BaseDialog 不吞并字段校验 Interface。
- 打开时标题提供上下文，关闭后不额外重复播报；成功结果由页面 toast/live region 负责。
- busy 文案必须可读，不只显示 spinner；spinner 为装饰元素。

#### 预期代码范围

- 新增 `frontend/src/components/common/BaseDialog.vue` 和内部 `useDialogStack/useFocusTrap` 实现。
- 重写 `ConfirmDialog.vue` 以 BaseDialog 为唯一基础。
- 迁移 HostModal、ForwardModal、RouteModal、HostKeyDialog、文件夹弹窗、DNS 确认和 Settings 更新结果。
- 删除页面级 overlay、Bootstrap backdrop 和重复 dialog-card 生命周期代码；保留统一样式 token。
- i18n 补充 busy、关闭受限、错误摘要等必要文案。
- 测试 Adapter 注入可控命令 Promise，覆盖 pending、resolve、reject 和永不返回的状态。

#### 验收标准

- 每个对话框都有唯一且有效的 role、aria-modal、aria-labelledby；有描述时 aria-describedby 指向存在节点。
- 只用键盘可以打开、遍历全部控件、提交、取消和关闭；Tab/Shift+Tab 不离开 Dialog。
- confirm/danger 默认聚焦取消，form 聚焦首个错误或首字段，info 聚焦关闭。
- 非 busy 时 Escape 只关闭栈顶一次；busy 时 Escape、关闭和双击确认均不产生第二个命令或错误关闭。
- 点击遮罩不会关闭任何 Dialog。
- 打开期间 Sidebar、页面按钮和其他 Dialog 不可聚焦/不可被读屏导航；关闭后 inert 和滚动锁完整清理。
- 普通关闭恢复原触发按钮；删除成功导致触发按钮消失时，UI-05 的业务焦点规则胜出，不落到 body。
- 表单失败保留输入和 Dialog，错误得到一次读屏通知；成功后焦点恢复且页面收到成功状态。
- 动态字段出现/隐藏后焦点陷阱仍正确；零可聚焦内容时容器可接收焦点。
- axe/等效检查、组件键盘测试和真实 Wails WebView 键盘冒烟全部通过；最小窗口和 200% 缩放无操作按钮裁切。
- 仓库中不再残留业务页面自建 `.overlay`/`.modal-backdrop` 生命周期；所有 Dialog 经统一 seam。

#### 后续关联

- `UI-02` 的 Route 变更确认、`UI-04` 的批量移动确认和 `UI-05` 的删除确认统一复用 ConfirmDialog/BaseDialog。
- `UI-05` 负责删除成功后新的业务焦点落点；BaseDialog 只恢复仍存在的原触发元素。
- `UI-01` stale/busy 决定哪些业务动作可用，BaseDialog 只执行传入的 busy/dismissible 事实。
- `ARCH-02` 应删除各页面重复 Modal 状态机和 overlay，实现集中到 BaseDialog seam；领域表单仍保留自己的数据与校验。

### 4.23 UI-07：以会话 generation 隔离端口预检，并保持真实 bind 为权威结果

#### 状态与结论

- 状态：已实现并通过 generation 竞态测试验证
- 确认日期：2026-07-21
- 问题结论：成立。当前 ForwardModal 的 400ms 防抖只能取消尚未执行的 timer；已经发出的 Wails 调用没有身份。旧地址的迟到成功会清除新地址的冲突，旧地址的迟到失败会给新地址显示冲突，关闭/重开弹窗或切换 remote 也不能阻止旧请求写回。catch 又把所有系统/通信错误都翻译成“端口冲突”。
- 产品决策：端口预检是及时提示，不是端口预留，也不是保存/启动成功承诺。最终 Start 的真实 bind 结果始终权威。
- 架构决策：建立领域化 `useForwardPortCheck` Module，通过单调 generation 和完整候选快照保证 only-latest-wins；不抽象成与领域无关的通用异步验证框架。
- 取消决策：Wails 调用无法可靠通过浏览器 AbortController 取消；关闭和输入变化只做逻辑失效，旧调用即使返回也不得产生状态或通知。

#### 前端状态模型

```ts
type PortCheckState =
  | 'idle'
  | 'debouncing'
  | 'checking'
  | 'available'
  | 'occupied'
  | 'owned_by_self'
  | 'not_applicable'
  | 'invalid'
  | 'unknown'

interface PortCheckView {
  state: PortCheckState
  candidate?: { mode: string; host: string; port: number; forwardId?: number }
  error?: AppErrorView
}
```

- `idle`：弹窗关闭或尚无完整候选。
- `debouncing`：输入有效，等待约 400ms 稳定期。
- `checking`：当前 generation 的后端预检尚未完成。
- `available`：检查瞬间可以绑定；不表示已经保留。
- `occupied`：后端确认被其他进程或其他 Forward 占用。
- `owned_by_self`：编辑正在运行的同一个 Forward，监听地址未变化；不能把自身监听误报成外部冲突。
- `not_applicable`：remote 模式不在本机监听。
- `invalid`：地址或端口未通过结构化校验。
- `unknown`：权限、系统或 Wails 通信失败，无法得出 occupied 结论。

#### generation 与会话规则

- Module 持有不回绕、不因关闭重置的单调 generation。
- host、port、mode、editingForwardId、Dialog open/close 任一变化以及 Module unmount 都先递增 generation，再清 timer/计算新状态。
- 发起请求时冻结：`{generation, dialogSession, forwardId, mode, normalizedHost, port}`。
- Promise resolve/reject 后必须同时满足：generation 仍为当前值、Dialog 仍打开、session 相同、当前规范化字段与冻结候选完全一致，才允许落地。
- 不比较展示字符串或对象引用；host 使用与后端一致的规范化规则，port 在发起前转为合法整数。
- remote/非法输入/关闭立即进入 not_applicable/invalid/idle，并使所有旧请求失效。
- onBeforeUnmount 清 timer 并递增 generation，禁止卸载后的写入和 toast。

#### 后端 Preview Interface

将错误型 `CheckLocalPortAvailable(host, port) error` 替换为领域化命令：

```go
type PreviewLocalListenerCommand struct {
    ForwardID int    `json:"forwardId,omitempty"`
    Mode      string `json:"mode"`
    Host      string `json:"host"`
    Port      int    `json:"port"`
}

type LocalListenerPreview struct {
    State             string `json:"state"`
    NormalizedAddress string `json:"normalizedAddress,omitempty"`
    OwnerForwardID    int    `json:"ownerForwardId,omitempty"`
    Error             *AppErrorView `json:"error,omitempty"`
}
```

- 应用 Module 先执行模式、loopback 地址和端口范围校验。
- RuntimeBiz 按当前 runEntry/generation 判断该地址是否由相同 Forward 持有；匹配时返回 owned_by_self。
- 不是自身监听时再调用真实 socket bind Adapter；成功后立即关闭并返回 available，地址占用返回 occupied。
- 权限、地址族、系统资源或不可分类错误返回 unknown/结构化错误，前端不得改写为 occupied。
- Adapter 只观察检查瞬间，不创建 reservation，不修改 Runtime，不停止现有监听。
- 即使 Preview 为 available，Save/Start 仍必须重新校验并以真实 bind 失败为最终结果，处理预检后的 TOCTOU。

#### UI 行为

- 输入有效后进入 debouncing；到期后进入 checking。两者显示轻量、非阻塞状态。
- available 可以显示低强调成功提示；occupied 显示明确警告；owned_by_self 显示“当前 Forward 正在使用此监听地址”。
- unknown 显示“暂时无法检查端口”，附安全错误摘要或重试提示，不能显示“端口已占用”。
- not_applicable 和 idle 不显示本地端口警告。
- 预检不自动触发 toast，不抢焦点；状态文本使用稳定 `aria-live=polite`，只播报当前 generation 的实质变化。
- 预检结果不作为跳过后端保存/启动校验的缓存凭证，也不允许 available 解锁原本非法的表单。
- occupied 是否允许只保存配置由后端命令语义决定；若保存后立即启动，则真实 bind 失败必须以结构化运行状态显示，不能因预警只是 advisory 而假报启动成功。

#### 可测试 seam

当前前端没有测试脚本或测试依赖。实施本项的第一步是加入与现有 Vue/Vite 版本兼容并固定版本的 Vitest、Vue Test Utils 和 DOM Adapter；先建立红色回归测试，再修改实现。

- `useForwardPortCheck` 接收注入的 Preview Adapter 和可控 timer，因此测试可以分别挂起/resolve/reject 每个 Promise，并使用 fake timer 精确推进 400ms。
- 测试通过 Module 的公开 Interface 观察 PortCheckView，不测试私有 generation 数值。
- 后端使用 fake Listener Adapter 和 fake Runtime ownership 查询，分别验证 occupied、available、owned_by_self 和 unknown 分类。
- 至少保留一条挂载 ForwardModal 的集成测试，证明组件 watch、Dialog 会话和 Module seam 正确连接，避免只测试脱离真实调用方式的辅助函数。

#### 预期代码范围

- `frontend/src/composables/useForwardPortCheck.ts`：generation、timer、状态机和注入的 Preview Adapter。
- `ForwardModal.vue`：删除裸 timer/portWarning，消费 PortCheckView，并在 show/editing/candidate 变化时驱动 Module。
- `app.go`/ARCH-01：暴露 `PreviewLocalListener(command)` 的有类型 Interface，删除或内部化旧 CheckLocalPortAvailable 绑定。
- `internal/biz/runtime.go`：查询自有监听地址与 Forward generation，不泄露可变 runs map。
- `internal/forward`：保留实际 bind Adapter，返回可分类错误；最终 Start 路径继续独立 bind。
- i18n：补充 checking/available/occupied/owned-by-self/unknown/not-applicable 文案。
- 前端测试配置：添加精确依赖、fake timer 和 Wails Adapter mock；不改用与现有 Vite 不兼容的最新版组合。

#### 验收标准

- A=3000 慢速 occupied，B=4000 快速 available：最终只显示 B available，A 迟到不得覆盖。
- A 慢速 available，B 快速 occupied：A 迟到不得清除 B 警告。
- local 请求在途时切换 remote：立即 not_applicable，旧 local 结果永不出现。
- 请求在途时关闭并用相同字段重开：旧 session 结果不得进入新 session，新请求独立决定状态。
- Modal 卸载后旧 Promise resolve/reject：无状态写入、无 Vue warning、无 toast。
- 快速连续输入使用 fake timer 证明只为最后候选调用一次 Preview；跨越多个 debounce 窗口时允许在途多次，但只有最新结果生效。
- 编辑正在运行且监听地址未变的 Forward 返回 owned_by_self；其他 Forward/进程占用返回 occupied。
- 后端权限或通信错误显示 unknown/无法检查，不显示端口冲突。
- 预检 available 后由测试进程抢占端口，真实 Start 必须失败并进入结构化 error/retry 状态，证明没有把预检当 reservation。
- 端口状态变化不会抢焦点，读屏只播报最新 generation。

#### 后续关联

- `RUN-01` 的 runEntry/generation 是识别 owned_by_self 的运行时事实来源，Preview 只能只读查询。
- `UI-06` 负责 Modal 打开/关闭生命周期；关闭事件必须调用 port-check invalidate，busy/focus 逻辑不由本项重复实现。
- `ARCH-01` 应暴露结构化 PreviewLocalListener，而不是让前端从任意 error string 推断端口状态。
- 后续若其他字段也出现异步验证竞态，应先验证是否共享同一领域不变量；不能仅因代码相似就扩大成浅的通用框架。

### 4.24 UI-08：新安装默认开启更新检查，但运行时只有成功读取 true 才允许自动联网

#### 状态与结论

- 状态：已实现并通过前后端 fail-closed 与生命周期单次检查测试验证
- 确认日期：2026-07-21
- 问题结论：成立。App 启动和 Settings 页面都先把更新偏好设为 true，读取失败后继续保持 true；前者会直接访问 GitHub，后者会把未知状态伪装成“自动检查已开启”。这同时违反隐私 fail-closed 和 UI 状态真实性。
- 产品决策：全新安装创建 Vault 时，`UpdateCheckEnabled` 明确初始化为 true，保证普通用户能够获知后续版本；这是一项数据默认值，而不是读取失败时的运行时 fallback。
- 隐私决策：在本次应用生命周期中，只有偏好数据读取成功且明确为 true，才允许自动联网检查。loading、读取失败、明确 false 和 DATA-01 恢复隔离态全部零自动请求。
- 显示决策：偏好尚未读取或读取失败时，开关视觉上显示为关闭，同时禁用交互并显示“正在读取”或“读取失败”；不能让未勾选外观被理解成已经成功保存 false。
- 手动决策：用户点击“立即检查更新”是一次明确联网意图，不受自动检查开关、偏好读取状态或本次自动检查是否执行的限制。

#### 区分安装默认值与运行时 fallback

- 新 Vault 不能再依赖 Go bool 零值。由唯一的 `NewVaultData()`/初始化函数显式写入 `{Version: current, Prefs.UpdateCheckEnabled: true}`。
- `vault.Open` 的所有“文件不存在/空文件初始化”路径复用该函数，避免一个入口默认 true、另一个入口仍为 false。
- 用户已经保存的 false 必须永久保留；升级不能把 false 重置为 true。
- 旧数据若缺少字段且没有足够 schema 证据证明这是“从未设置”，按 false 处理，不擅自联网。只有确定的新安装初始化才默认 true。
- Backup/Restore 保留包内显式值，但 restored true 仍受 DATA-01 quarantine 门禁；不能因为默认值或备份值在恢复后立即联网。

#### 偏好展示状态

```ts
type UpdatePreferenceView =
  | { state: 'loading'; enabled: false }
  | { state: 'ready'; enabled: boolean }
  | { state: 'error'; enabled: false; error: AppErrorView }
```

- App 和 Settings 初始均为 `{state: loading, enabled: false}`，不得先显示 true。
- loading：开关未勾选、disabled，显示“正在读取更新偏好”。
- error：开关未勾选、disabled，显示“无法读取更新偏好”和重试按钮。
- ready：开关才反映真实 enabled，并允许用户修改。
- 设置保存期间开关 disabled；失败后恢复此前 ready 值并显示错误，不把内存选择冒充持久结果。
- 用户把 false 改为 true 只保存偏好，不在当前动作中自动访问 GitHub；“立即检查”仍是独立按钮。
- enabled=false 的视觉文案是“自动检查已关闭”；loading/error 的文案分别说明未知原因，三者虽然均未勾选但语义不可混同。

#### 后端 Update Module Interface

自动联网门禁必须由后端执行，不能只依赖 WebView 条件：

```go
type UpdateCheckTrigger string // startup | manual

type CheckForUpdatesCommand struct {
    Trigger UpdateCheckTrigger `json:"trigger"`
}

type UpdateCheckOutcome struct {
    State      string          `json:"state"` // checked | skipped | failed
    SkipReason string          `json:"skipReason,omitempty"`
    Result     *updater.Result `json:"result,omitempty"`
    Error      *AppErrorView   `json:"error,omitempty"`
}
```

- 后端直接使用构建版本，WebView 不再传入 currentVersion，避免显示版本与检查版本漂移。
- startup trigger 的固定顺序：检查本应用会话是否已尝试 → 读取恢复隔离状态 → 读取偏好 → 仅在读取成功且 true 时调用 updater Adapter。
- 偏好读取失败返回 `skipped/preference_unavailable` 或安全结构化状态，HTTP Adapter 调用次数必须为零。
- 明确 false 返回 `skipped/disabled`；恢复隔离返回 `skipped/restore_quarantined`；都不得初始化会发请求的 updater 调用路径。
- 同一应用生命周期的 startup 自动检查 single-flight 且至多执行一次；页面重挂载、语言切换和 Snapshot refresh 不会重复检查。
- manual trigger 只由明确按钮动作发出，允许在偏好 false/loading/error 时联网；仍受 URL 白名单、超时、响应大小和一次性 busy 防重约束。
- 开关从 false 保存为 true 后不补做 startup trigger；如用户想立即查询，需点击手动按钮。
- 自动检查失败只进入安全日志/可选非打扰提示；手动检查通过 UI-06 信息 Dialog 显示 up-to-date/update-available/error。

#### 启动编排与 Snapshot

- App 启动立即把 UpdatePreferenceView 设为 loading/unchecked，然后通过 UI-01 GetSnapshot 或专用读取取得真实 prefs。
- 自动检查不能由 `let enabled=true; try Load; catch ignore` 编排。启动编排调用后端 startup Interface，由后端原子完成 policy gate 和网络调用。
- Snapshot 成功返回 prefs=true 只负责正确显示；它本身不重复触发检查。Update Module 会话状态记录本次 startup 是否 skipped/checked。
- Snapshot 失败时 UI-01 保留或进入 error/stale；没有成功偏好事实时，Update Module 仍必须 fail-closed。
- 后续 Snapshot 成功读到 true 不追补本次已因读取失败 skipped 的自动检查，避免一次后台刷新产生意外联网；用户可手动检查，下一次应用启动再自动尝试。

#### 与完全还原的关系

- StageRestore 解析出来的 true 只是预览值，不触发联网。
- CommitRestore 后应用处于持久 quarantine；本次会话和重启恢复流程中的 startup 均返回 restore_quarantined。
- 用户执行 DATA-01 明确 Activate 时只解除隔离并保存配置，不立即执行自动更新检查。
- 激活后的下一次应用启动，若偏好成功读取为 true，才执行自动检查；用户也可以在激活后立即手动检查。
- Restore 的 false 保持 false，不因“新安装默认 true”被覆盖。

#### 预期代码范围

- `internal/model`/`internal/vault`：增加唯一 NewVaultData 初始化路径，显式设置新安装 UpdateCheckEnabled=true；保护已有 false。
- 后端 Update Module：按 trigger 执行 policy gate、session single-flight、恢复隔离检查和 updater Adapter 调用。
- `app.go`/ARCH-01：用有类型 CheckForUpdates(command) 和 Snapshot prefs 替换 Get 后由前端拼接联网顺序；currentVersion 不再由 WebView传入。
- `frontend/src/App.vue`：删除 updateCheckEnabled=true fallback 和前端自主自动检查条件。
- `SettingsPage.vue`：使用 loading/ready/error 状态；loading/error 未勾选且禁用，提供错误和重试；手动检查保持独立。
- UI-06：手动检查结果使用统一 info Dialog，busy 时防止重复请求。
- privacy contract：增加运行时 fake HTTP Adapter 计数测试，不再只靠 import allowlist 推断没有外联。

#### 验收标准

- 全新数据目录首次创建后，持久化 prefs 明确为 true；首次启动成功读取后自动检查一次。
- 全新安装在偏好写入成功但随后读取失败时，仍然零 HTTP 请求；数据默认 true 不能越过运行时门禁。
- App/Settings 初始渲染开关未勾选且 disabled；成功读取 true 后才勾选，成功读取 false 后保持未勾选并启用。
- 偏好读取失败时开关未勾选、disabled，并显示错误/重试；HTTP Adapter 调用为零。
- enabled=false + startup：零请求；enabled=true + startup：恰好一次请求。
- startup 被重复调用或并发调用：最多一个真实请求，其他调用复用/返回已处理状态。
- loading、error、false 任一状态下用户明确点击 Manual：恰好执行一次请求，并显示对应结果；双击不重复。
- false 保存为 true 后当前动作零请求；点击手动按钮才请求，或下次启动自动请求。
- 已有用户明确保存 false，升级/重启/迁移后仍为 false；缺失字段且来源不明时也不自动改 true。
- restore 包内 true 在 Stage、Commit、quarantine 恢复和 Activate 当下均零自动请求；激活后的下一次启动才允许自动检查。
- startup 使用后端构建版本，不接收 WebView 传入的伪造或过期 currentVersion。
- 运行时网络契约测试对 preference error/disabled/quarantined 断言 HTTP Adapter 调用数为 0，而不是只断言 UI 没调用某函数。

#### 后续关联

- `DATA-01` 的恢复 quarantine 是 startup Update policy 的前置门禁；Activate 不立即触发检查。
- `UI-01` Snapshot 负责正确显示 preference loading/ready/error，但是否联网最终由后端 Update Module 决定。
- `UI-06` 提供手动检查结果 Dialog 和 busy 防重；自动检查不弹出阻塞 Dialog。
- `ARCH-01` 应把 currentVersion、偏好读取和网络 gate 隐藏在 CheckForUpdates(trigger) Interface 内，前端不再拼装隐私关键顺序。

### 4.25 UI-09：侧栏更新入口始终可发现、可键盘操作，并统一消费 UpdateNoticeStore

#### 状态与结论

- 状态：已实现并完成可发现性、键盘与 aria-live 验证
- 确认日期：2026-07-21
- 问题结论：成立。当前更新入口是版本文本内部的 `span @click`，不在键盘 Tab 顺序中，也没有动作语义；折叠侧栏时父元素 `.sidebar-version.compact` 被 `display:none`，更新标记一并消失。`new` 和红色又缺少版本及动作上下文。
- 产品决策：版本文本和更新动作分离；发现新版本后，侧栏展开与折叠状态都保留一个原生 button。点击先打开应用内更新详情，再由用户明确打开官方 Releases 页面。
- 状态决策：UI-08 的 startup/manual 检查统一写入根级 `UpdateNoticeStore`；Sidebar 只消费可展示状态并发出查看意图，不自行检查更新或保存第二份布尔值。
- 持久性决策：更新提示在本次应用生命周期中持续存在，关闭详情不清除；只有后续成功检查确认无更新或应用升级后才移除。检查失败不得抹掉已知的 available 事实。

#### UpdateNoticeStore

```ts
interface UpdateNoticeView {
  state: 'none' | 'available'
  currentVersion: string
  latestVersion?: string
  releaseNotes?: string
  observedAt?: string
}
```

- Store 位于 App/root seam，生命周期独立于 SettingsPage 是否挂载，避免 PERF-02 lazy page 卸载后丢失通知。
- startup 与 manual 的 checked 结果通过同一 reducer 更新 Store；available 写入版本和纯文本发行说明，无更新时清为 none。
- skipped/failed 不把已有 available 改成 none；失败详情属于当前检查 Dialog/日志，不是否定此前成功观察。
- Store 不保存任意外部 URL。打开下载页使用 Update Module 校验后的官方 GitHub Releases 目标，防止远端响应把 WebView 变成任意 URL 启动器。
- 本项不持久化“已看过/已忽略”。用户关闭详情后仍能再次进入，避免提示永久丢失。

#### 展开侧栏

- Footer 中版本号保持普通文本，例如 `v1.0.0`。
- 新版本入口是版本号的兄弟 button，而不是嵌套 span；显示更新图标和“发现新版本 v1.1.0”。
- 按钮使用清晰但不过度抢占主导航的提示样式，具有 hover、active、focus-visible 和高对比度状态。
- 文本和图标共同表达，不用颜色或英文 `new` 作为唯一线索。
- 可访问名称包含最新版本和动作，例如“发现新版本 1.1.0，查看更新详情”。

#### 折叠侧栏

- 隐藏版本文本不影响更新按钮；Footer 仍渲染至少 32×32 的独立更新图标按钮。
- 可以显示装饰性通知圆点，但圆点 `aria-hidden`，不承担点击或语义。
- button 自身有完整 aria-label；title/tooltip 只作鼠标补充，不能替代可访问名称。
- 折叠/展开不销毁 UpdateNoticeStore，也不触发重新检查或重复播报。
- 按钮保持在可预测的 Sidebar Tab 顺序中，不通过正 tabindex 抢到导航之前。

#### 更新详情与外部页面

- 点击按钮打开 UI-06 BaseDialog(kind=info)，展示当前版本、最新版本和纯文本发行说明。
- Dialog 提供“稍后”和“打开下载页面”；关闭/稍后只关闭 Dialog，不清除通知。
- 打开下载页是明确用户动作。Update Module 只允许 `https://github.com/HanZephyr/TunnelBoard/releases` 及严格允许的同仓库 release/tag 路径；其他 scheme、host、userinfo、重定向式字符串全部拒绝或回退官方 Releases 根页。
- 发行说明按纯文本渲染，不使用 `v-html`，防止远端内容注入 WebView。
- 外部页面打开失败时 Dialog 保持或显示结构化错误，通知入口继续存在。

#### 读屏与播报

- `UpdateNoticeStore` 从 none 首次变为 available 时，通过全局 `aria-live=polite` 播报一次“发现新版本 X”。
- 同一版本的重复检查、Sidebar 重新渲染、折叠/展开和打开/关闭 Dialog 不重复播报。
- 后续观察到更高版本时可再播报一次；Store 以 latestVersion 作为去重键。
- 图标为装饰；按钮可见文本或 aria-label 提供完整语义。
- focus-visible 必须明显，不能被 overflow 裁切；200% 缩放下版本文本和按钮不重叠。

#### 预期代码范围

- App/root：新增轻量 UpdateNoticeStore，统一处理 UI-08 startup/manual outcomes 和一次性 live announcement。
- `AppSidebar.vue`：删除 `span @click`，将版本文本和更新 button 分离；展开/折叠使用同一语义按钮。
- Sidebar CSS：compact 只隐藏版本文本，不隐藏更新入口；增加稳定尺寸、focus-visible 和高对比度样式。
- UI-06：更新详情迁移到 BaseDialog info 类型。
- Update Module/ARCH-01：校验官方 Release 地址，向 View 返回纯文本内容；打开动作不得接受任意 WebView URL。
- i18n：补齐“发现新版本 X”“查看更新详情”“打开下载页面”“稍后”和打开失败文案，删除硬编码 `new`。

#### 验收标准

- available 状态下，展开与折叠 Sidebar 都能看见并点击更新入口；折叠 CSS 不再把按钮 display:none。
- 仅用 Tab、Enter、空格可打开详情、关闭并再次打开；focus-visible 清晰，关闭后焦点回到更新按钮。
- 读屏获得当前/最新版本和动作说明；通知圆点和图标不产生重复噪音。
- available 首次出现只播报一次；同版本重复检查、重渲染和侧栏折叠不重复播报。
- 关闭更新详情后入口仍存在；检查失败也保留此前 available。
- 后续成功检查 no-update 或应用当前版本已达到 latest 时，入口消失且不会遗留空按钮。
- startup 和 manual 发现同一版本时生成相同通知及详情，不维护两套结果。
- release notes 中包含 HTML/script 字符串时仅按文本显示；恶意/非官方 URL 不会交给系统浏览器。
- 最小窗口、200% 缩放、明暗主题和五种 locale 下按钮不被截断、不与版本号重叠。

#### 后续关联

- `UI-08` 的 Update Module 是通知事实唯一来源；skipped/failed 与 available 的合并规则由本项 Store 统一处理。
- `UI-06` 提供详情 Dialog、初始焦点和关闭恢复；Sidebar 不自行创建 overlay。
- `PERF-02` 异步加载 SettingsPage 后，UpdateNoticeStore 仍位于根级且不随页面 chunk 卸载。
- `ARCH-01` 的更新 Interface 不应向 WebView 暴露未校验任意 URL；打开页面使用固定/严格校验的官方目标。

### 4.26 PERF-02：按页面和语言真实 seam 拆分首包，并以 CI 预算阻止回归

#### 状态与结论

- 状态：已实现并通过生产构建预算验证
- 确认日期：2026-07-21
- 问题结论：成立。2026-07-21 重新执行 `pnpm run build`，119 个 Module 被打入单一 `index.js`：531.32 KiB minified / 154.33 KiB gzip，Vite 明确发出 500 KiB 告警；CSS 为 346.35 KiB / 50.19 KiB gzip。App 同步导入六个页面，i18n 同步导入五种语言，main 又导入完整 Bootstrap JS bundle，而业务仅使用 Tooltip。
- 性能结论：Wails 资源来自本地，拆包主要收益是减少冷启动解析、编译、执行和常驻 Module 图，而不是网络下载时间；不能夸大为传统 Web 首屏带宽优化。
- 架构决策：以页面和语言这两个真实 seam 动态加载；Overview、AppSnapshotStore、UpdateNoticeStore、Sidebar 和全局错误设施保持首包。暂不按任意文件大小编写 manualChunks。
- 依赖决策：删除 `bootstrap.bundle.min.js` 全量入口，Tooltip 使用精确导入；Bootstrap CSS 暂不在同一改动中大规模重写，先设置不回归预算后再凭测量决定裁剪。
- 门禁决策：不提高 `chunkSizeWarningLimit` 隐藏告警；GitHub Actions 对实际 dist 建立可失败的 JS/CSS 体积和动态资源完整性门禁。

#### 当前基线

| 资源 | Minified | Gzip | 备注 |
| --- | ---: | ---: | --- |
| `index.fb651f4f.js` | 531.32 KiB | 154.33 KiB | 单 chunk，超过 Vite 500 KiB 阈值 |
| `index.fa6b8601.css` | 346.35 KiB | 50.19 KiB | 含完整 Bootstrap CSS、Bootstrap Icons CSS 和应用样式 |
| Bootstrap Icons woff2 | 130.90 KiB | 不适用 | 浏览器优先字体资源 |
| Bootstrap Icons woff | 176.06 KiB | 不适用 | 兼容字体资源，通常不与 woff2 同时加载 |

基线来自当前工作区真实生产构建；`frontend/package.json` 的用户已有 packageManager 改动未被本项修改。

#### 页面级动态加载

- Overview 保持同步加载，作为应用启动后的默认页面。
- Forwards、Hosts、Routes、Logs、Settings 使用 `defineAsyncComponent(() => import(...))` 或等价 PageLoader Module。
- 页面注册表只描述 key、loader、标题和图标；不能把每个页面的业务 Store 生命周期放入 loader。
- AppSnapshotStore、UpdateNoticeStore、Runtime/Route 事件订阅位于根级。异步页面只接收现有 View/command Interface，加载时不得重复 GetSnapshot、重复 startup update check 或创建第二个全局轮询。
- 当前 `v-if` 切页本就卸载页面；拆包后不默认引入 KeepAlive，避免所有重页面访问一次后永久驻留。需要保留的筛选/选择状态应由明确页面状态 Store 决定，而不是靠缓存整个页面实例。
- 本地资源加载仍可能因安装包缺 chunk、文件损坏或版本混装失败；PageLoader 必须显示可操作错误、重试和诊断入口，不能白屏或让 Sidebar 失效。
- 首次页面加载期间保留 App shell、当前页面标题和轻量 skeleton；完成后焦点不被强制移动。用户主动重试成功时把焦点放到页面主标题。

#### 语言动态加载

- `i18n.js` 不再静态 import 五个 JSON。
- 启动先确定保存/系统 locale，再加载当前语言；当前语言不是英文时同时加载英文 fallback。
- 语言 loader 使用固定枚举映射，不能把任意用户字符串拼入 import 路径。
- Settings 切换语言时先异步加载目标 messages，成功后再切 locale、保存偏好并同步 tray；失败时保留原 locale 和已加载 messages，显示可重试错误。
- 同一 locale 的并发加载 single-flight，成功后在本应用生命周期缓存；失败 Promise 不永久污染缓存，允许重试。
- 五种 locale 的 key parity 继续由静态测试扫描源 JSON，不能因为运行时 lazy load 放弃完整性检查。
- 语言 chunk 来自 Wails 内嵌资源，不允许 CDN 或远端 fallback。

#### Bootstrap JS 与 Tooltip

- 删除 `main.js` 的 `import 'bootstrap/dist/js/bootstrap.bundle.min.js'`。
- `IconActionButton`/`TooltipText` 改为 Bootstrap Tooltip 的精确 ESM 路径，构建结果只包含 Tooltip 及其真实依赖；若精确导入仍带入不可接受体积，再评估以轻量原生/CSS Tooltip Module 替换。
- 不同时保留 bundle import 和精确 import，避免重复实现与不可预测 tree-shaking。
- UI-06 已用自有 BaseDialog，不需要 Bootstrap Modal JS；Sidebar、折叠和下拉当前也不应依赖 bundle 的全局副作用。
- 删除 bundle 后逐项验证 Tooltip dispose、动态 title 更新和页面卸载，避免遗留事件监听或 detached DOM。

#### 暂不执行的优化

- 不在第一步手写 Vue/vendor/manualChunks；桌面内嵌资源没有传统 CDN 长缓存收益，错误拆分反而可能增加公共 chunk 和部署耦合。
- 不单纯提高 Vite 告警阈值。
- 不在同一提交大规模替换 Bootstrap CSS utility；当前应用模板广泛依赖它，需先通过 coverage/构建分析再确定收益。
- 不把每个 Modal、按钮或小 Module 都异步化；过细 chunk 增加加载状态和测试面。
- 不删除字体兼容格式或子集化图标，除非后续真实加载/包体测量证明收益足以覆盖跨平台字体风险。

#### 体积预算与 CI

新增构建后预算脚本读取实际 dist，并在 GitHub Actions 中失败：

- 初始 entry JS：≤ 250 KiB minified 且 ≤ 90 KiB gzip。
- 任一异步 JS chunk：≤ 200 KiB minified。
- 任一 JS chunk：不得达到 500 KiB；Vite 构建不得输出 chunk-size warning。
- 初始 CSS 暂定 ≤ 360 KiB minified 且 ≤ 55 KiB gzip，先防回归；Bootstrap CSS 裁剪完成后再向下收紧。
- 预算脚本同时报告 entry、shared、page、locale、CSS 和字体总量，避免拆成大量小文件后总量无限上涨。
- 预算数值是发布门禁，若确有产品功能需要提高，必须附新的 bundle 分析和决策记录，不能静默改常量。

#### 动态资源与发布完整性

- Vite 生成 manifest；REL-01 打包清单必须递归包含 index 引用的全部 page、shared、locale、CSS、字体和静态资源。
- CI 从最终 Windows/macOS artifact 解包后校验 manifest 文件存在与 SHA-256，而不是只检查工作区 dist。
- self-check 至少解析入口/manifest 的静态和动态依赖闭包；删除任一 lazy chunk 或语言资源必须失败。
- 不允许部署旧 index 配新 chunk 或新 index 配旧 chunk。安装/升级采用完整目录原子替换或版本目录切换，不能逐文件覆盖后立即启动。
- Wails 内嵌构建同样验证动态 import 可由其资源协议加载；浏览器 dev server 成功不能代替安装产物验证。

#### 预期代码范围

- `App.vue`：页面注册表和异步 PageLoader；根级 Store/事件保持常驻。
- `i18n.js`/Settings locale flow：固定 loader map、single-flight、成功后切换。
- `main.js`、IconActionButton、TooltipText：删除 Bootstrap bundle 全量 import，使用精确 Tooltip Module。
- 新增 bundle budget/manifest verification 脚本，并由 `pnpm run build` 或专用 CI gate 调用。
- `.github/workflows/build.yml` 与 REL-01 打包入口：验证最终 artifact 的动态资源闭包。
- 前端测试：PageLoader resolve/reject/retry、locale load failure、Store 不重复初始化和 Tooltip 卸载。

#### 验收标准

- 生产构建生成独立 entry、二级页面和 locale chunks；Overview 首屏不加载 Forwards/Hosts/Routes/Logs/Settings 实现及非当前语言。
- entry 与所有 chunk 满足预算，Vite 不再出现 >500 KiB 告警；不能通过提高 warning limit 达标。
- 冷启动 GetSnapshot、startup update check 和事件订阅各最多一次；首次进入 lazy page 不重复调用。
- 依次进入全部页面，功能、状态、键盘焦点、UI-06 Dialog 和运行时事实与拆包前一致。
- 页面 loader reject 时 App shell/Sidebar 可用，显示错误、重试和诊断入口；成功重试后页面正常。
- 五种语言首次加载、切换、失败与重试正确；失败时不出现半语言界面或清空当前 messages。
- 删除 Bootstrap bundle 后所有 Tooltip 正常创建、更新和 dispose，未使用的 Bootstrap Modal/Dropdown/Collapse JS 不在 bundle 中。
- 动态资源无外部网络请求，离线安装环境可加载全部页面和语言。
- 从最终 artifact 删除任一 page/locale/shared chunk，manifest/self-check 必须失败；完整 artifact 的 Windows Wails 实机切页冒烟通过。
- 记录冷启动脚本执行/解析时间和进程内存作为前后对照；如果体积下降但启动指标无改善，报告真实结果，不伪称性能提升。

#### 后续关联

- `UI-01` AppSnapshotStore 保持根级，lazy page 不建立第二数据源或把加载失败伪装为空状态。
- `UI-09` UpdateNoticeStore 保持根级，SettingsPage 卸载或尚未加载时更新提示仍存在。
- `UI-06` BaseDialog 取代 Bootstrap Modal JS，为删除全量 bundle 提供前提。
- `REL-01` 必须把 Vite manifest 的动态依赖闭包纳入最终 artifact 清单和 self-check。
- `ARCH-01` 的 GetSnapshot/command Interface 使页面 chunk 只依赖稳定 View，不因拆包重新暴露大量 Wails 细粒度绑定。

### 4.27 ARCH-01：以根级 ApplicationClient 和有类型高层命令建立真正的应用 Module

#### 状态与结论

- 状态：部分实现并验证；高风险写入已迁移到根级 ApplicationClient 与有类型命令，旧只读绑定和页面轮询仍作为 P3 迁移债保留
- 确认日期：2026-07-21
- 问题结论：成立。当前 `app.go` 虽自称应用 Module，但仍公开三十多个 Wails 方法，绝大多数只是对 Catalog/Runtime/Router/Backup 的薄转发；各 Vue 页面直接 import 生成绑定，并自行编排 Save→Preview→Apply、Load→Refresh、Stage→Commit 等顺序。复杂度没有消失，只是扩散到调用方。
- 架构决策：新增后端 `internal/application` 深 Module，并在前端建立唯一 `ApplicationClient` Adapter。页面只依赖 ApplicationClient，不直接导入 Wails 生成绑定或 Events runtime。
- 类型决策：不实现 `Execute(name string, payload map[string]any) any`。应用 Module 保留十几个有类型的高层用例命令；深度来自隐藏编排、不变量和错误模式，而不是把方法数量机械压成一个无类型入口。
- 查询决策：GetSnapshot 聚合小型、无秘密、带 revisions 的 View；日志正文、备份包、文件流等有界大查询保持专用 Interface，不塞入万能 Snapshot。
- 事件决策：规格中的 SubscribeRuntimeEvents 由前端 ApplicationClient 封装 Wails Events 实现；后端发单一失效通知和 sequence，不伪造一个从 Go 绑定返回的长期流。

#### Module 与 seam

```text
Vue Pages
   │
   ▼
ApplicationClient（唯一前端 Interface）
   │  Wails generated bindings + EventsOn Adapter
   ▼
app.go（Wails 生命周期/传输 Adapter）
   │
   ▼
internal/application.Service（应用 Module）
   ├── CatalogBiz
   ├── RuntimeBiz
   ├── RouteCoordinator
   ├── BackupPackage/RestoreCoordinator
   ├── Update Module
   ├── LogStore
   └── Native dialog / OS Adapter
```

- `app.go` 只负责 Wails startup/shutdown/beforeClose、DTO 传输和极薄委托，不包含业务编排。
- application.Service 负责用例顺序、OperationGate、revision/idempotency、跨领域补偿入口和安全错误转换；它不重新实现 Catalog/SSH/Route 的领域规则。
- 下层 Module 的 Adapter seam 保持 internal；不能为了页面或测试把 Vault、Caddy、Helper、socket 等端口暴露到应用外部 Interface。
- 第三方 GitHub updater 作为 external port 注入生产/测试 Adapter；Vault/文件系统使用可替代的内存/临时目录 Adapter；Runtime/Route 使用受控 fake 实现测试编排。

#### 前端 ApplicationClient

只有一个文件树（例如 `frontend/src/application/client.ts`）允许导入：

- `wailsjs/go/main/App`
- `wailsjs/runtime` 的 EventsOn/Off
- Wails 生成的 DTO

页面看到的 Interface 按能力组织：

```ts
application.snapshot.get()

application.commands.createFolder(command)
application.commands.moveForwards(command)
application.commands.saveSSHHost(command)
application.commands.saveForward(command)
application.commands.deleteSelection(command)
application.commands.startForwards(command)
application.commands.stopForward(command)
application.commands.previewRouteChange(command)
application.commands.commitRouteChange(command)
application.commands.stageRestore(command)
application.commands.commitRestore(command)
application.commands.activateRestore(command)
application.commands.setPreferences(command)
application.commands.checkForUpdates(command)

application.queries.getLogTail(query)
application.events.subscribe(listener)
```

- Client 只适配传输、DTO 和统一错误，不重复后端业务规则或乐观推导状态。
- 页面不传函数引用给通用 `callBackend`，也不从 raw error string 判断 code。
- Client Interface 可使用 TypeScript 精确类型；底层 Wails 方法数量不是页面需要了解的 Interface。
- 文件选择、打开配置目录、导出目标等本机动作也经 Client 的有类型 native command，不允许 SettingsPage 直接调用散落的 Wails runtime。

#### 高层命令而非无类型 Execute

每个公开命令对应完整用户意图，例如：

- `CommitRouteChange` 内部完成 revision 检查、desired 保存、RouteCoordinator Apply/补偿和结构化结果，不暴露 SaveWebRoute→PreviewRoute→ApplyRoute。
- `SaveSSHHost` 内部完成 SEC-06 SecretAction、SSH-03 ConnectionIdentity 预览、受影响 Forward 停止/保存/失效/重启。
- `MoveForwards` 在单次 Vault Update 中原子提交 UI-04 批量操作。
- `CommitRestore` 消费 staged token，进入 maintenance、SuspendAll、替换 Vault、neutralize Route 和 quarantine，不暴露 Shutdown。
- `CheckForUpdates(startup|manual)` 隐藏偏好、恢复隔离、版本和联网门禁。

允许存在十几个明确方法，因为调用方能从类型和名称理解完整语义。拒绝：

```go
Execute("save-route", map[string]any{...})
```

这种接口把 payload 约束、错误模式和结果类型退化为运行时约定，方法数虽少但深度更差。

#### Snapshot View

```go
type AppSnapshot struct {
    SchemaVersion int
    EventSequence uint64
    ObservedAt    time.Time
    Revisions     DomainRevisions
    Catalog       CatalogView
    Runtime       []RuntimeView
    Routes        RouteStateView
    Preferences   PreferencesView
    Recovery      RecoveryView
    Capabilities  CapabilityView
}
```

- CatalogView 使用 SEC-06 无秘密 DTO；SSH 密码、口令、私钥内容和内部 Vault model 永不返回。
- Revisions 分域记录 vault/runtime/route/preferences；不制造一个无法证明原子一致的全局 revision。
- EventSequence 用于判断 Snapshot 期间是否有更新事件，不用时间戳猜测新旧。
- Recovery 暴露 quarantine/pending journal/maintenance 摘要，不暴露备份密码、staged plaintext 或系统路径秘密。
- CapabilityView 表达平台能力、Helper/Caddy 可用性和是否允许 mutation；页面不通过 OS 名称自行推导。
- 日志内容通过 PERF-01 GetLogTail；备份预览通过 SEC-05 staged token；大内容不进入首屏 Snapshot。

#### Snapshot 一致性

- Application Service 为配置 mutation 维护串行 gate，因此 Vault/desired 状态不会在一个命令内部被其他配置命令插入。
- Runtime 和进程观察仍可能独立变化；Snapshot 返回各域 revision/event sequence，而不是长时间锁住网络 Runtime 强求全局原子。
- ApplicationClient 先注册事件监听，再获取首个 Snapshot；若排队事件 sequence 高于 Snapshot.sequence，立即合并一次 refresh，避免订阅窗口丢更新。
- GetSnapshot 读取期间若关键 Vault revision 变化，可进行一次有界重试；仍变化时返回各域事实和 `refreshRecommended`，不能无限循环。
- UI-01 AppSnapshotStore 以 request generation、event sequence 和 domain revisions 合并；迟到 Snapshot 不能覆盖新命令结果。

#### CommandMeta、幂等与结果

所有 mutation 输入统一嵌入：

```go
type CommandMeta struct {
    CommandID       string
    ExpectedRevision string
}
```

- CommandID 由 ApplicationClient 为一次用户意图生成；同一 ID 的传输重试必须返回原结果，不能重复写入或重复副作用。
- 后端维护有界、按应用生命周期的 recent-result cache；缓存 key 同时绑定命令类型和安全输入摘要，ID 相同但 payload 不同返回冲突。
- ExpectedRevision 使用对应领域 revision；旧 revision 返回结构化 conflict 和当前 revision，不进行部分 mutation。
- 命令结果包含 accepted revisions、event sequence、typed data、warnings 和安全错误；不能只返回 nil/error 后要求前端猜测发生了什么。
- 长事务的 staged token/txID 仍由 DATA/ROUTE 领域 Module 管理，不用 CommandID 替代领域 journal。

#### OperationGate

不使用一把全局 mutex 无差别阻塞所有动作。应用 Module 把命令分为：

- Read：Snapshot、状态、日志 tail；允许正常并发并带 revision。
- SafeStop：Stop Forward、退出；在 stale、maintenance 和恢复阶段仍允许，避免用户无法止损。
- Mutation：配置保存、Start、Route、偏好修改；在 normal 状态按规则串行或交给领域锁。
- Maintenance：CommitRestore 等独占用例；阻止新 Start/配置 mutation，但内部可执行 SuspendAll/Neutralize。
- Shutdown：拒绝所有新 mutation，只有幂等清理继续完成。

- Gate 状态和拒绝原因进入 Capability/Recovery View。
- SafeStop 虽可绕过配置 mutation 队列，仍必须遵守 RUN-01 generation 和 SSH-02 有界 Stop。
- 长 Route Apply 不应让用户无法 Stop 一个 Forward；领域依赖冲突由命令返回结构化状态，而不是永久等待全局锁。

#### 安全错误 Interface

统一返回：

```go
type AppErrorView struct {
    Code             string
    MessageKey       string
    Retryable        bool
    StateMayHaveChanged bool
    CurrentRevision  string
    FieldErrors      map[string]string
    DiagnosticID     string
}
```

- 不向 WebView 发送 raw wrapped error、密码、口令、私钥、任意文件内容或可用于命令注入的细节。
- `StateMayHaveChanged=true` 时 UI 进入 stale/refresh，不盲目回滚。
- FieldErrors 使用稳定字段 key，由五种 locale 翻译；后端仍执行最终校验。
- 完整内部错误进入 SafeLogHandler/诊断 ID，前端只显示安全摘要。

#### 事件 Interface

- 后端只发一个版本化事件，例如 `app:state-invalidated:v1`。
- payload 仅包含 `{sequence, domains, revisions, reason}`；不发送完整 Vault、日志、秘密或高频字节统计。
- Runtime generation、Route applied 状态、Vault revision、prefs/recovery 变化均转换为失效域；AppSnapshotStore 决定何时刷新。
- ApplicationClient 的 `events.subscribe` 封装 EventsOn 并返回唯一 unsubscribe；页面不直接 EventsOffAll，避免误删其他订阅。
- 事件风暴在 Client/Store 中短窗口合并；Stop/error 等用户关键状态仍及时刷新。
- 前端断线/页面重载后通过 GetSnapshot 恢复事实，事件不承担 durable log 职责。

#### 迁移方式：replace，不长期 layer

1. 定义无秘密 View、AppError、CommandMeta、AppSnapshot 和 application.Service；建立 facade 测试。
2. 建立前端 ApplicationClient 和 AppSnapshotStore，先迁移 GetSnapshot。
3. 按 Catalog → Runtime → Route → Backup/Restore → Settings/Update/Native 的垂直用例迁移。
4. 每迁移一个完整用例，当次删除对应旧 Wails 方法、页面直接 import、旧状态/测试；不保留长期双写兼容层。
5. 引入事件失效模型，删除页面独立轮询和直接 Wails Events。
6. 最后让 `app.go` 只剩生命周期和委托，增加静态检查禁止 pages/modals 直接 import `wailsjs`。

迁移期间新旧命令不能同时写同一领域；否则 revision、幂等和事件不变量无法证明。必要的短期 feature flag 只能在 ApplicationClient 内单选 Adapter，禁止双写。

#### 测试策略

- application.Service Interface 是主要测试面；使用 in-memory Vault、fake Runtime/Route/Update/Native Adapter，断言可观察结果，不读取内部锁或 map。
- Snapshot 合约测试把已知秘密植入 Vault，并对序列化 JSON 全量搜索，保证零泄漏。
- 命令合约测试覆盖 stale revision、重复 CommandID、同 ID 不同 payload、partial failure、stateMayHaveChanged 和 accepted revision。
- OperationGate 覆盖 maintenance 中 Start 被拒绝、Stop 可用、Shutdown 后 mutation 被拒绝。
- Route/Restore 测试只调用高层命令，证明调用方无需知道内部步骤和补偿顺序。
- 事件测试覆盖“先订阅后 Snapshot”、Snapshot 期间事件、burst 合并、unmount unsubscribe 和迟到刷新。
- 前端 lint/CI 规则只允许 application client/adapter 目录 import `wailsjs`；其他命中直接失败。
- 新 facade 测试覆盖后，删除只验证旧 pass-through 的低价值测试；下层领域不变量测试继续保留。

#### 验收标准

- Vue pages/modals/utils 中除 ApplicationClient Adapter 外没有 `wailsjs` 或 Wails Events import。
- 用户可见用例均由有类型高层命令完成，不存在 Save→Preview→Apply、逐项批量写或 Stage 后前端自行拼 Commit。
- `app.go` 不直接操作 Vault model 字段、不编排领域副作用，只处理生命周期/传输委托。
- GetSnapshot 一次返回首屏所需无秘密 View、各域 revisions、event sequence 和 recovery/capabilities；大数据保持有界查询。
- 相同 CommandID 重试只产生一次 Vault revision/系统副作用/事件；ID 与 payload 不一致明确拒绝。
- stale ExpectedRevision 零 mutation；unknown outcome 设置 stateMayHaveChanged 并驱动 UI-01 stale。
- maintenance 时新 Start/配置 mutation 被拒绝，Stop/退出仍可用且有界。
- 事件在首个 Snapshot 前注册，Snapshot 期间的更新不会丢失；事件风暴不导致并发刷新和旧快照覆盖。
- Wails 响应、事件、错误和诊断预览均不包含 SEC-06 秘密。
- Application Service 的 fake Adapter 集成测试、前端 Client 合约测试、全仓 race/build 和真实 Wails 垂直 smoke 通过。

#### 后续关联

- `SEC-06` DTO 和 SecretAction 是 Snapshot/SaveSSHHost 的强制外部模型。
- `UI-01` AppSnapshotStore、`UI-02/03` Route 状态、`UI-04` 原子批量、`UI-07` Preview 和 `UI-08/09` Update 都通过本项 Interface 实现。
- `DATA-01`、`ROUTE-02` 的 staged token/journal 仍属于各自深 Module；Application Service 只编排其公开 Interface。
- `PERF-01` 日志保持有界 query，`PERF-02` lazy page 只依赖稳定 ApplicationClient。
- `ARCH-02` 的表单 Module 输出有类型命令，不直接调用 Wails Adapter。

### 4.28 ARCH-02：以 SSHHostEditor 和 SSHHostFields 收敛 Host 表单规则

#### 状态与结论

- 状态：已实现并验证
- 确认日期：2026-07-21
- 问题结论：成立。HostsPage 与 ForwardModal 分别维护 Host 默认值、认证字段显隐、验证和 payload 构造；后者已缺少 keepalive、timeout、算法和备注，且直接调用 Wails SaveSSHHost。两套逻辑无法共同落实 SEC-06 SecretAction 和 SSH-03 连接身份变更流程。
- 架构决策：建立领域化 `SSHHostEditor` Module 和无副作用 `SSHHostFields.vue`；两个真实调用方共用同一 seam。拒绝扩张成 Folder/Route/Forward 全部使用的万能 schema 表单框架。
- Modal 决策：HostDialog 复用 UI-06 BaseDialog；Forward 中继续使用内嵌展开区，不打开第二层 Dialog。Modal 生命周期不由本项重复实现。
- 持久化决策：Forward 内嵌“保存主机”是明确、独立、立即持久化的 Host 创建操作。保存成功后 Host 加入主机列表和当前 chain；即使随后取消 Forward，该 Host 仍保留。界面必须在保存前明确说明此行为。

#### SSHHostEditor Interface

建议由 `createSSHHostEditor(options)`/等价 composable 提供一个实例化编辑会话：

```ts
interface SSHHostEditor {
  draft: SSHHostDraft
  visibleFields: SSHHostFieldState
  fieldErrors: Record<string, string>
  dirty: boolean
  busy: boolean
  reset(view?: SSHHostView): void
  setAuthType(authType: SSHAuthType): void
  validate(): boolean
  buildCommand(meta: CommandMeta): SaveSSHHostCommand
  applyServerError(error: AppErrorView): void
  clearTransientSecrets(): void
}
```

- Editor 只管理当前表单会话、规则和命令构造，不读取 Vault、不调用 Wails、不启动/停止 Forward。
- 输入只接受 SEC-06 `SSHHostView` 和后端给出的非敏感 defaults；不接受内部 model.SSHHost 或已保存秘密。
- buildCommand 输出 ARCH-01 ApplicationClient 的有类型 SaveSSHHostCommand。
- 服务端错误通过稳定 field key 映射；未知错误保留为 Dialog 级错误。
- close、cancel、成功和组件卸载都调用 clearTransientSecrets，随后销毁当前 draft。

#### 统一 Draft 与默认值

```ts
interface SSHHostDraft {
  id?: number
  name: string
  host: string
  port: number | string
  user: string
  authType: 'password' | 'ssh_key' | 'ssh_agent'
  keyPath: string
  agentSocketPath: string
  secretAction: 'keep' | 'replace' | 'clear'
  secretInput: string
  hasSecret: boolean
  keepAliveIntervalMs: number | string
  timeoutMs: number | string
  hostKeyAlgorithms: string
  notes: string
}
```

- 新建默认值由 AppSnapshot 的 `SSHHostDefaultsView` 提供：port 22、authType ssh_key、keepalive 5000ms、timeout 5000ms 等；两个入口不能自行复制常量。
- 后端仍应用安全默认并最终验证，不能相信前端 defaults。
- edit draft 从无秘密 SSHHostView 初始化；hasSecret 只表示后端已有秘密，secretInput 始终为空。
- normalize 在 buildCommand 时执行：展示输入不因每次按键被 trim，提交时统一 trim 字符串、解析端口/时间并保留用户可修正错误。
- full/compact 仅改变布局密度与高级区域初始折叠状态，字段能力、默认值和规则完全一致。

#### SecretAction 和认证类型

- 新建 password：必须 `replace` 且 secretInput 非空。
- 编辑 password：hasSecret=true 时默认 keep；replace 必须提供新密码；clear 不允许留下不可用的 password 认证。
- SSH key：keyPath 必填；口令可 keep、replace 或 clear，无口令私钥是合法状态。
- SSH agent：不提交密码/口令，命令使用 clear 清理旧秘密；agentSocketPath 按平台规则验证。
- 认证类型切换只改变 visibleFields 和最终命令，不立即销毁当前未保存字段。用户切回原类型时仍可看到本会话输入。
- 最终保存时后端清理与选中 authType 不兼容的持久字段，并按 SSH-03 规则递增 CredentialRevision/ConnectionIdentity。
- 临时 secretInput 只存在当前 WebView draft，不进入 Snapshot、Store、日志、toast、validation detail 或诊断包。

#### SSHHostFields.vue

- 只渲染 Editor draft/visibleFields/fieldErrors，并发出字段变化；不持有第二份表单对象。
- 每个实例使用稳定唯一 idPrefix/useId，label、input、error aria-describedby 一一对应，不复用 `hostName/newHostName` 等硬编码全局 ID。
- full 与 compact 渲染同一字段集合；compact 可默认折叠高级字段，但用户始终能访问 keepalive、timeout、算法和备注。
- 认证类型切换、secret action 控件和 hasSecret 提示具有明确读屏文本；“已保存密码”不显示内容。
- 字段错误就地显示，Dialog 级错误由 HostDialog/ForwardModal 的稳定 alert region 处理。
- Fields 不包含保存/取消按钮，因此可同时嵌入 HostDialog 和 Forward form。

#### 两个调用流程

**Hosts 页面：**

1. 打开 HostDialog 时以 new defaults 或 SSHHostView 创建 Editor session。
2. 提交时 Editor validate/buildCommand。
3. ApplicationClient 执行 SaveSSHHost；编辑连接身份字段时按 SSH-03 返回影响预览/确认并编排重启。
4. 成功后关闭、清秘密、通过 Snapshot 取得新 View；失败保留 draft 和焦点。

**Forward 内嵌新建：**

1. 在 ForwardModal 内展开 compact SSHHostFields；不打开嵌套 Dialog。
2. 保存按钮文案和说明明确：“主机保存后将加入主机列表；取消 Forward 不会删除它”。
3. 用户点击保存才执行独立 SaveSSHHostCommand；仅展开后取消时 Vault 零变化。
4. 保存成功返回无秘密 SSHHostView，把新 ID 追加到当前 chain，并刷新根 Snapshot；Host 独立持久化。
5. 随后取消 Forward 时不回滚 Host；若用户确实不需要，可从 Hosts 页面按引用规则删除。

这种语义符合 SSH Host 是独立可复用实体的领域模型，避免为 Forward 草稿引入临时 Host ID、跨实体补偿和隐含事务。

#### 表单验证

- 前端 Editor 集中执行 name/host/user 必填、端口范围、auth-specific 字段、timeout/keepalive 范围和安全长度限制，为用户提供即时反馈。
- 后端 Application/Catalog Module仍执行全部最终验证、引用检查、SecretAction 和资源预算；前端通过不等于可信。
- 字段约束使用稳定 error code/key，五种 locale 翻译；不把英文 raw error 写入字段。
- Hosts 与 Forward 两个入口的相同 draft 必须生成字节等价的业务字段命令（CommandID 等元数据除外）。
- 后端新增规则时必须同时更新 Editor 测试；不能继续在两个页面复制条件。

#### Modal/确认状态收敛

- UI-06 BaseDialog 已统一焦点、Escape、busy、背景 inert 和关闭恢复，本项不再创建另一套 Modal Manager。
- HostDialog 只组合 BaseDialog + SSHHostFields + actions；Forward 内嵌区属于当前 Forward Dialog 的内容。
- 删除/重启确认继续使用统一 ConfirmDialog，但业务确认 state 保持有类型数据，不在通用 composable 中存储任意回调闭包。
- 提交 busy 由当前 Editor/ApplicationClient 命令结果驱动，双击只产生一个 CommandID。

#### 预期代码范围

- 新增 `frontend/src/application/forms/sshHostEditor.ts` 或等价领域位置。
- 新增 `frontend/src/components/hosts/SSHHostFields.vue`，重构 HostModal 为 HostDialog + BaseDialog。
- HostsPage 删除 defaultHostForm/buildHostPayload/validateHostPayload 和直接 Wails import。
- ForwardModal 删除 newHostForm、重复 computed/watch/validation/payload 和直接 SaveSSHHost import，改用 compact Editor/Fields。
- AppSnapshot 增加无敏感信息 SSHHostDefaultsView；ApplicationClient 提供 SaveSSHHostCommand。
- 后端 SaveSSHHost 落实 SEC-06/SSH-03 确认方案并返回无秘密 View、revision 和影响结果。
- i18n 增加 SecretAction、已保存秘密、独立保存提示、高级字段和结构化错误文案。

#### 验收标准

- 相同新建 draft 从 Hosts 和 Forward 入口生成相同业务命令及相同字段错误。
- 两个入口均能编辑全部字段；compact 只是布局不同，不再默默使用固定 advanced defaults。
- edit View 的 secretInput 始终为空，keep/replace/clear 在 password/ssh_key/ssh_agent 下符合规则。
- 认证类型 A→B→A 且未保存时，本会话输入仍在；保存 B 后后端清除 A 的不兼容持久秘密。
- close/cancel/success/unmount 后已知测试 secret 不再存在于 Editor state、DOM、Snapshot、日志和诊断包。
- Forward 内嵌只展开后取消：Vault 零变化；点击保存成功后再取消 Forward：Host 仍在 Hosts 列表且提示文案已提前说明。
- 内嵌保存失败保留 draft 和错误，不把 Host ID 加入 chain；成功只追加一次，重复 CommandID 不产生第二个 Host。
- 编辑连接字段触发 SSH-03 影响预览与重启编排；只改 name/notes 遵循已确认 identity 规则。
- DOM 无重复 ID，键盘/读屏可使用 full/compact 全部字段、SecretAction、高级折叠和错误。
- 页面/Modal 不再直接 import Wails；所有保存经 ApplicationClient。
- 删除重复实现后，新增 Host 字段只需修改 Editor、Fields、DTO/后端一次，并有两个入口的合约测试锁定。

#### 后续关联

- `SEC-06` 决定 View、SecretAction 和临时秘密清理；本项不能重新引入 password 字段回填。
- `SSH-03` 决定编辑连接身份后的预览、连接代失效与受影响 Forward 重启。
- `UI-06` 提供 HostDialog 基础设施；本项只负责 Host 领域表单。
- `ARCH-01` ApplicationClient 是唯一保存入口，Editor 输出有类型命令而不接触 Wails Adapter。
- 两个真实调用方构成值得维护的 seam；其他表单只有在出现同一领域规则的第二个真实调用方后再评估抽取。

## 5. 决策变更记录

| 日期 | 问题 | 变更 |
| --- | --- | --- |
| 2026-07-21 | SEC-01 | 放弃持久 Windows 服务方案，确认采用单次应用生命周期内复用的临时高完整性 Helper |
| 2026-07-21 | SEC-02 | 放弃机器级根 CA；确认共享安装、每用户 Caddy 数据和 CurrentUser Root 信任模型 |
| 2026-07-21 | SEC-01 | 受 SEC-02 影响，Helper 常规白名单收紧为 hosts 操作，CA 仅保留无任意参数的一次性旧版迁移 |
| 2026-07-21 | SEC-03 | 固定 TCP Admin API 改为每应用 generation 的权限化 AF_UNIX socket，并以进程句柄建立 Caddy 所有权 |
| 2026-07-21 | RUN-01 | 确认以 `runEntry + generation + starting/running/stopping` 隔离旧 watcher、迟到事件、并发启动与 Shutdown |
| 2026-07-21 | ROUTE-01 | 确认现有提交只修复直接症状；最终由 CaddySupervisor 按自有进程状态统一决定热重载、冷启动和端口冲突 |
| 2026-07-21 | SSH-01 | 确认共享首跳使用有界单飞 keepalive；超时关闭当前连接代，由 Forward Runtime 统一退避重连 |
| 2026-07-21 | SSH-02 | 确认以运行 context、活跃连接 registry 和 5 秒总 deadline 保证停止；极端阻塞时关闭精确共享连接代 |
| 2026-07-21 | RUN-01 | 受 SSH-02 影响，Stop 成功前保留 stopping generation；timeout 时禁止同 ID 新代启动，完成后再落 stopped |
| 2026-07-21 | SEC-04 | 确认 Linux 删除动态 shell、macOS 使用常量 AppleScript + argv；Unix 暂不引入会话级常驻 root Helper |
| 2026-07-21 | REL-01 | 确认 GitHub Actions 是唯一正式构建者；Windows 安装包、macOS 条件交付、Linux 后置，Tag 先建 draft 并经真实 UAC smoke 后发布 |
| 2026-07-21 | SSH-03 | 确认按 ConnectionIdentity 换代；连接字段变化必须预检并确认重启全部受影响 Forward，不允许静默保留旧目标 |
| 2026-07-21 | DATA-01 | 确认完全还原采用零副作用 Stage、事务化 Commit 和持久恢复隔离态；恢复配置在用户再次明确激活前不得产生网络副作用 |
| 2026-07-21 | RUN-01 | 受 DATA-01 影响，永久 `Shutdown` 仅用于应用退出；完全还原改用可恢复的 `SuspendAll/Resume` 和应用 mutation lock |
| 2026-07-21 | SSH-02 | 受 DATA-01 影响，恢复暂停复用同一 `Stop(ctx)` 与 5 秒全局预算，但不把 RuntimeBiz 设为永久 closing |
| 2026-07-21 | SSH-04 | 确认单跳只做首跳池级探活，多跳额外做末跳端到端探活；首跳健康时只重建当前 Forward 的独占尾链 |
| 2026-07-21 | SSH-02 | 受 SSH-04 影响，删除 `shared bool`，将等待失效、局部重建、释放和精确 Abort 收敛到 ChainLease Interface |
| 2026-07-21 | ROUTE-02 | 确认 Route 使用 desired/applied 分离、唯一串行 RouteCoordinator、revision-bound journal 和 generation-safe 补偿；配置保存后应用失败必须如实显示并可重试 |
| 2026-07-21 | SEC-02 | 受 ROUTE-02 影响，现有 `CATrustedSHA256` 从可移植 Vault 迁入每用户 RouteAppliedState，实际 CurrentUser Root 查询仍为事实来源 |
| 2026-07-21 | ROUTE-01 | 受 ROUTE-02 影响，Caddy Apply/补偿由 RouteCoordinator 按 revision 编排，Supervisor 只接受完整目标并拒绝迟到旧 revision |
| 2026-07-21 | DATA-01 | 受 ROUTE-02 影响，恢复的 Neutralize/Activate 直接复用 RouteCoordinator、journal 和 applied revision |
| 2026-07-21 | SEC-05 | 确认备份文件、KDF、实体、字符串和私钥硬预算；所有导入/恢复/私钥提取使用一次解密的 staged token，导入复杂度收敛为 O(n) |
| 2026-07-21 | DATA-01 | 受 SEC-05 影响，StageRestore 直接消费统一 BackupPackage 的 restore-purpose token，不再自行读取或解析备份 |
| 2026-07-21 | SEC-06 | 确认已保存秘密绝不返回 WebView；内部 Vault 模型与 Wails DTO 分离，新秘密仅通过 SecretAction + SecretInput 单向一次性提交 |
| 2026-07-21 | SSH-03 | 受 SEC-06 影响，Host 保存、预览和重启统一使用 SSHHostView/SaveSSHHostCommand，CredentialRevision 只由后端 SecretAction 状态机更新 |
| 2026-07-21 | SEC-05 | 受 SEC-06 影响，Import/Restore/KeyExport Preview 全部改为无秘密 View，备份密码只在 Stage 请求出现一次 |
| 2026-07-21 | PERF-01 | 确认两日志源各 5 MiB 当前文件 + 3 档历史、64 KiB 单行、256 KiB/500 行 Tail，并使用 generation cursor 显式报告轮转和截断 |
| 2026-07-21 | SEC-03 | 受 PERF-01 影响，CaddySupervisor 不再把子进程绑定到永久追加文件，改为有界捕获 stdout/stderr 并交给 LogStore |
| 2026-07-21 | SEC-06 | 受 PERF-01 影响，SafeLogHandler 在所有磁盘、内存环、Tail 和诊断 sink 之前统一执行字段白名单与脱敏 |
| 2026-07-21 | UI-01 | 确认使用 AppSnapshotStore 区分首次 loading/error、真实空数据和保留旧快照的 stale；stale 时暂停配置 mutation 但保留 Stop/退出 |
| 2026-07-21 | ARCH-01 | 受 UI-01 影响，GetSnapshot 必须一次返回无秘密聚合数据及 Vault/Route/Runtime revisions，页面不再并发拼装多个事实源 |
| 2026-07-21 | UI-02 | 确认 Route 开关发送事件真实 checked 和 revision-bound intent；desired 未保存才回滚，已保存但未应用时保持新值并显示结构化状态 |
| 2026-07-21 | ROUTE-02 | 受 UI-02 影响，RouteCoordinator 统一处理 flag 联动、Preview/Commit 和 RouteCommandResult，前端不再提交旧完整 Route 或拼接 Save→Apply |
| 2026-07-21 | UI-03 | 确认 Route desired 与 applied 分开展示；loading/失败/缺失使用 checking/unknown，完整枚举覆盖 hosts_only、conflict、cleanup_pending 和 quarantined |
| 2026-07-21 | ROUTE-02 | 受 UI-03 影响，RouteAppliedState/Status 返回明确枚举与 revisions，并保持只读，不再在状态查询中执行端口诊断或系统副作用 |
| 2026-07-21 | UI-04 | 确认批量移动使用单个 MoveForwards 命令和一次 Vault Update，任一校验或保存失败均零项移动；目标选择与显式执行分开 |
| 2026-07-21 | UI-01 | 受 UI-04 影响，批量命令成功后由 AppSnapshotStore 记录 acceptedRevision 并单次刷新；刷新失败保留成功事实并进入 stale |
| 2026-07-21 | ARCH-01 | 受 UI-04 影响，应用 Module 暴露原子 MoveForwards Interface，前端与单项入口都不得在事务外循环保存 |
| 2026-07-21 | UI-05 | 确认两层文件夹导航使用嵌套列表和独立原生按钮，不引入不完整 ARIA Tree；选择、创建、删除具有确定性键盘和焦点规则 |
| 2026-07-21 | UI-01 | 受 UI-05 影响，stale 时保留文件夹浏览，Snapshot 刷新应保留有效 selectedFolderId，并对已删除项执行确定性回退 |
| 2026-07-21 | ARCH-02 | 受 UI-05 影响，文件夹可访问语义和焦点规则收敛到 FolderNavigation Module，外部只输出选择/创建/删除意图 |
| 2026-07-21 | UI-06 | 确认所有弹窗统一使用 BaseDialog；集中实现语义、Teleport、背景 inert、焦点陷阱、Escape、busy 防重和关闭后焦点恢复，遮罩点击不关闭 |
| 2026-07-21 | UI-02/UI-04/UI-05 | 受 UI-06 影响，Route、批量移动和删除确认统一复用 ConfirmDialog/BaseDialog，不再各自实现 overlay 和焦点状态机 |
| 2026-07-21 | ARCH-02 | 受 UI-06 影响，重复 Modal 生命周期收敛到 BaseDialog seam；领域弹窗只保留内容、校验和意图事件 |
| 2026-07-21 | UI-07 | 确认端口预检使用 Dialog session + 单调 generation 丢弃迟到结果，返回结构化 available/occupied/owned_by_self/unknown 状态，最终真实 bind 始终权威 |
| 2026-07-21 | RUN-01 | 受 UI-07 影响，RuntimeBiz 提供只读监听 ownership 查询，使编辑运行中 Forward 的未变地址返回 owned_by_self 而非外部冲突 |
| 2026-07-21 | ARCH-01 | 受 UI-07 影响，应用 Module 暴露结构化 PreviewLocalListener Interface，前端不再从 CheckLocalPortAvailable 的任意错误字符串推断冲突 |
| 2026-07-21 | UI-08 | 确认新安装显式默认开启自动更新检查；UI 在 loading/error 时显示未勾选且禁用，只有偏好成功读取为 true 才允许 startup 自动联网，Manual 始终由用户明确触发 |
| 2026-07-21 | DATA-01 | 受 UI-08 影响，恢复包内 true 在 quarantine 和 Activate 当下均不触发自动检查；解除隔离后的下一次启动才按成功读取的偏好检查 |
| 2026-07-21 | ARCH-01 | 受 UI-08 影响，Update Module 通过 CheckForUpdates(trigger) 在后端统一处理偏好、恢复隔离、会话单飞和网络门禁，WebView 不再传 currentVersion 或编排隐私顺序 |
| 2026-07-21 | UI-09 | 确认更新提示改为展开/折叠侧栏均可见的原生 button；点击打开应用内详情，关闭后提示持续存在，键盘、读屏和高对比度语义完整 |
| 2026-07-21 | UI-08 | 受 UI-09 影响，startup/manual 结果统一进入根级 UpdateNoticeStore；failed/skipped 不清除此前 available，发行说明按纯文本展示 |
| 2026-07-21 | ARCH-01 | 受 UI-09 影响，Update Module 只允许固定或严格校验的官方 GitHub Releases 目标，不向 WebView 暴露可任意打开的 URL |
| 2026-07-21 | PERF-02 | 确认以 Overview 首包、二级页面和语言真实 seam 动态加载，删除 Bootstrap JS 全量入口；建立 entry 250 KiB/90 KiB gzip、异步 chunk 200 KiB 等 CI 预算 |
| 2026-07-21 | UI-01/UI-09 | 受 PERF-02 影响，AppSnapshotStore 与 UpdateNoticeStore 保持根级，lazy page 不重复读取 Vault、检查更新或创建事件订阅 |
| 2026-07-21 | REL-01 | 受 PERF-02 影响，最终 artifact manifest/self-check 必须覆盖所有动态 page、locale、shared、CSS 和字体资源，缺任一 chunk 即失败 |
| 2026-07-21 | ARCH-01 | 确认采用根级 ApplicationClient + 后端 application.Service + 有类型高层命令；拒绝无类型 Execute(map)，并以 Snapshot/revisions、CommandMeta、OperationGate 和失效事件建立深 Module |
| 2026-07-21 | UI-01 | 受 ARCH-01 影响，ApplicationClient 先订阅失效事件再取 Snapshot，以 sequence/revisions 合并刷新；页面不再直接调用 Wails 或独立拼装状态 |
| 2026-07-21 | SEC-06 | 受 ARCH-01 影响，AppSnapshot、命令结果、事件和 AppError 全部使用无秘密 View；CI 禁止页面绕过 ApplicationClient 导入 Wails binding |
| 2026-07-21 | ARCH-02 | 确认 Hosts/Forward 两个入口共用 SSHHostEditor + SSHHostFields；full/compact 只改变布局，默认值、验证、SecretAction 和命令完全一致，不建立万能表单框架 |
| 2026-07-21 | ARCH-02 | 确认 Forward 内嵌保存是独立立即持久化 Host 的明确动作；保存后取消 Forward 不回滚 Host，界面必须提前说明 |
| 2026-07-21 | SEC-06/SSH-03 | 受 ARCH-02 影响，Editor 只消费无秘密 View 并输出 SaveSSHHostCommand；认证切换、凭据换代和受影响 Forward 重启仍由后端最终执行 |
