# TunnelBoard MVP 问题决策与修复方案记录

## 1. 文档定位

本文是 `2026-07-19-mvp-adversarial-review-and-remediation-plan.md` 的持续决策账本，用于逐项确认问题是否成立、最终修复方案、实现范围与验收标准。

原始审查报告保留发现时的证据和初始建议；本文记录讨论后正式确认的决策。两者冲突时，以本文中状态为“已确认”的最新条目为准。

维护规则：

1. 每次只讨论一个问题。
2. 问题经确认后，立即更新本文并单独提交。
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

原始审查表实际包含 28 项问题，而不是旧总结中提到的 27 项。本文以以下 28 项为完整清单。

| ID | 级别 | 问题摘要 | 当前状态 |
| --- | --- | --- | --- |
| SEC-01 | P0 | SYSTEM 服务直接注册普通用户可写的 Helper | 已确认 |
| SEC-02 | P1 | 同 SID 任意进程可让 Helper 安装任意自签根 CA | 讨论中 |
| SEC-03 | P1 | 固定无认证 Caddy Admin API 可被其他本机账户控制或冒充 | 待讨论 |
| RUN-01 | P1 | 旧 watcher 可删除同 ID 的新一代 Forward | 待讨论 |
| ROUTE-01 | P1 | 已运行的自有 Caddy 被误判为 443 端口冲突 | 待复核，已有实现改动 |
| SSH-01 | P1 | 池级 keepalive 没有超时 | 待讨论 |
| SSH-02 | P1 | Forward Stop 不关闭全部活跃桥接连接且无等待上限 | 待讨论 |
| SEC-04 | P1 | Unix 提权命令存在字符串插值风险 | 待讨论 |
| REL-01 | P1 | CI 未使用完整平台打包入口 | 待讨论 |
| SSH-03 | P1 | 连接池只按 Host ID 复用旧连接 | 待讨论 |
| DATA-01 | P2 | 完全还原在校验前停止运行时且未原子清理 Route 副作用 | 待讨论 |
| SSH-04 | P2 | 多跳链只探活首跳 | 待讨论 |
| ROUTE-02 | P2 | Route 系统副作用缺少串行事务协调 | 待讨论 |
| SEC-05 | P2 | 备份和导入缺少资源预算 | 待讨论 |
| SEC-06 | P2 | SSH 密码或口令可能进入 WebView | 待讨论 |
| PERF-01 | P2 | Caddy 日志不轮转且日志 tail 无界 | 待讨论 |
| UI-01 | P2 | Vault 加载失败被伪装成空状态 | 待讨论 |
| UI-02 | P2 | Route 开关失败不回滚 | 待讨论 |
| UI-03 | P2 | Route 未知或失败状态被显示为停止 | 待讨论 |
| UI-04 | P2 | 批量移动可能部分成功且界面不刷新 | 待讨论 |
| UI-05 | P2 | 文件夹选择缺少键盘和读屏语义 | 待讨论 |
| UI-06 | P2 | Modal 缺少统一焦点和对话框语义 | 待讨论 |
| UI-07 | P2 | Forward 端口异步预检存在迟到响应竞态 | 待讨论 |
| UI-08 | P2 | 更新设置读取失败时隐私策略 fail-open | 待讨论 |
| UI-09 | P2 | 侧栏更新入口的可发现性和键盘操作不足 | 待讨论 |
| PERF-02 | P2 | 前端单 chunk 过大且页面未按需加载 | 待讨论 |
| ARCH-01 | P3 | Wails 绑定面未收敛到应用 Module | 待讨论 |
| ARCH-02 | P3 | Host 表单和 Modal 状态机重复 | 待讨论 |

## 4. 已确认方案

### 4.1 SEC-01：将常驻 SYSTEM 服务改为应用会话级临时提权 Helper

#### 状态与结论

- 状态：已确认
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
- `Call`：只发送结构化白名单请求；禁止任意命令、任意目标路径和未建模的系统修改。
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

#### 后续关联

- `SEC-02` 将决定 Helper 是否继续拥有 CA 信任能力，以及该能力接受什么参数。若其结论改变 Helper 操作白名单或身份绑定，本节必须同步更新。
- `SEC-04` 将决定 macOS/Linux Adapter 是否采用同样的会话级提权模型，但不改变 Windows 不注册常驻服务的决策。
- `REL-01` 必须把 Authenticode 签名、Helper 完整性和“不产生持久服务”加入正式产物门禁。

## 5. 决策变更记录

| 日期 | 问题 | 变更 |
| --- | --- | --- |
| 2026-07-21 | SEC-01 | 放弃持久 Windows 服务方案，确认采用单次应用生命周期内复用的临时高完整性 Helper |
