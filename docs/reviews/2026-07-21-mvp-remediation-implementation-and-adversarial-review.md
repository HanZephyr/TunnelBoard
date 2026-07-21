# TunnelBoard MVP 整改实施与第二轮对抗性复核报告

## 1. 结论

本轮已完成 `docs/reviews/2026-07-21-mvp-remediation-decision-log.md` 中 28 项已确认方案的实现、集成和第二轮对抗性复核。

当前结论分为两层：

- **开发分支验收：通过。** 原审查中的 P0/P1 攻击路径和主要 P2 状态一致性问题均已修复；Go 全量测试、重点 race、前端测试与生产构建、Release verifier 和本地 Windows bundle 构建通过。
- **正式 Release：有条件通过。** 必须由 GitHub Actions 使用正式 Authenticode 证书生成候选产物，并对下载后的签名 bundle 运行 Windows 真机 smoke。当前本地 bundle 未签名，Helper 在触发 UAC 前按设计拒绝运行，因此不能用本地无签名产物替代这道门禁。

本轮审查和复核均直接基于代码、测试、构建产物与本地运行证据，**未使用 Codex Security 插件或相关技能**。

## 2. 范围

- 原始审查范围：`3dfc328..4789a56`
- 整改决策提交：`1add5d9 docs: finalize MVP remediation decisions`
- 开发分支：`codex/mvp-remediation`
- 第二轮复核前实现终点：`182c455 test(caddy): isolate runtime directories`
- 第二轮修复终点：`0ed2cfe fix(release): smoke test packaged Windows bundle`
- 实施方式：多个独立 Git worktree 并行开发，逐项 Conventional Commit 合并回主修复分支；完成后再由 Runtime/事务、平台/发布、UI/标准三路独立复核。

## 3. 28 项问题实施状态

| ID | 最终状态 | 关键结果 |
| --- | --- | --- |
| SEC-01 | 已实现，本地构建通过，签名 smoke 待 CI | 常驻 SYSTEM 服务改为应用生命周期级临时提权 Helper；本次应用内首次 UAC 后复用，应用退出即关闭 |
| SEC-02 | 已实现，本地构建通过，签名 smoke 待 CI | CA 只进入当前 Windows 用户信任库；不同用户首次使用 Route 时各自授权，不依赖安装目录 |
| SEC-03 | 已验证 | Caddy Admin 从固定 TCP 2019 改为每应用 generation 的权限化 AF_UNIX socket，并校验自有进程 |
| RUN-01 | 已验证 | Forward 的 watcher、事件和终态写入均绑定 generation，旧实例不能删除或覆盖新实例 |
| ROUTE-01 | 已验证 | CaddySupervisor 区分自有热重载与外部 443 冲突，不再把自己的监听误判为冲突 |
| SSH-01 | 已通过 race | 共享首跳 keepalive 有界、单飞，超时主动关闭失效 transport |
| SSH-02 | 已通过 race | Forward 使用 context、活跃连接 registry 和全局 deadline，Stop 不再无限等待 |
| SEC-04 | 代码与测试通过，真机待 Runner | 移除 Linux/macOS 动态命令源码拼接，改用固定程序和参数边界 |
| REL-01 | 本地产物链通过，远端签名待 CI | GitHub Actions 调用唯一 Release Module，生成并验证 bundle、manifest、SHA256SUMS，Release 默认 draft |
| SSH-03 | 已验证 | 连接池按真实连接身份换代；Host 连接字段变化时预览并受控重启受影响 Forward |
| DATA-01 | 已通过故障注入 | Restore 改为 Stage/Commit/Activate，带持久 journal、隔离态和补偿恢复 |
| SSH-04 | 已验证 | 首跳池级探活，末跳端到端探活；多跳尾链失效只重建必要部分 |
| ROUTE-02 | 已验证 | Route 副作用由 revision、desired/applied 状态和串行 journal 协调 |
| SEC-05 | 已验证 | 备份文件、KDF、实体、字符串和私钥均有统一资源预算 |
| SEC-06 | 已验证 | 已保存秘密不返回 WebView；新秘密通过一次性 token/明确动作单向提交 |
| PERF-01 | 已验证 | 日志轮转、有界读取、generation cursor、行数上限和脱敏已落地 |
| UI-01 | 已验证 | Snapshot 区分 loading、ready、stale、error；刷新失败保留旧事实 |
| UI-02 | 已验证 | Route 开关提交精确意图并区分 desired、persisted、applied |
| UI-03 | 已验证 | Route 状态使用明确枚举，未知或错误不再显示为 stopped |
| UI-04 | 已验证 | 批量移动改为一次 Vault 原子事务并返回汇总结果 |
| UI-05 | 已验证 | 文件夹导航使用原生按钮和嵌套列表语义，支持键盘操作 |
| UI-06 | 已验证 | BaseDialog 统一焦点、Escape、busy、防重复提交和焦点恢复 |
| UI-07 | 已验证 | 端口预检使用会话 generation，迟到响应不能覆盖新输入 |
| UI-08 | 已验证 | 新安装默认开启；偏好读取前、失败或明确关闭时均不自动联网，成功读取 true 后才检查 |
| UI-09 | 已验证 | 更新入口在折叠/展开态均可发现，支持键盘、版本化 aria-label 和去重 aria-live |
| PERF-02 | 已验证 | 页面与语言按 seam 拆包，CI 检查 bundle 预算；页面加载失败提供重试和诊断入口 |
| ARCH-01 | 部分完成，保留 P3 | 高风险写入已迁移到根级 ApplicationClient 和有类型命令；旧只读绑定与页面轮询尚未全部移除 |
| ARCH-02 | 已验证 | SSHHostFields 和共享表单领域规则统一 Hosts 页面与 Forward 内嵌入口 |

各项最终状态已同步回决策记录，不再把“讨论已确认”与“代码已验证”混为一谈。

## 4. 关键方案落地

### 4.1 会话级 Helper 与当前用户 CA

Windows Helper 不再安装为自动启动的常驻 SYSTEM 服务。应用首次需要修改受控 hosts 等特权能力时：

1. 主应用创建随机命名、仅当前用户可连接的会话管道；
2. 校验 bundle 内 Helper 的摘要、Authenticode 签名和与主应用相同的发布者；
3. 通过 UAC 启动本次会话 Helper；
4. 双方校验 PID、父进程、协议版本和管道端点；
5. 本次应用生命周期内复用连接，应用退出时有界关闭 Helper。

这正好满足“每次打开应用最多授权一次”，不会把授权持久化到下次启动。

Caddy CA 改为当前用户信任。应用位于 `Program Files` 不影响这一模型：程序文件可供所有用户读取，但 CA 信任状态属于各自用户；每个用户第一次启用需要 HTTPS Route 时，在自己的会话中查看指纹并确认。按讨论结论，不支持同一台机器多个用户同时运行 TunnelBoard。

旧版本机器级 CA 的迁移只删除能用证书与私钥精确证明归属的 Caddy CA，并支持旧 `config.root`。在代输 UAC 场景中，会话 Helper 根据已经验证的父进程 PID 定位原标准用户配置，避免误用管理员账户的 `%APPDATA%`。

### 4.2 Caddy 所有权与控制面

- 关闭固定 `127.0.0.1:2019` Admin API；其他本机应用不能再直接调用 `/load` 或 `/stop`。
- 每个应用 generation 使用独立 AF_UNIX socket 和受限 ACL。
- Windows 使用 Job Object 绑定 Caddy 生命周期；主应用持有真实进程句柄、PID、启动时间和 generation。
- 热重载只针对当前自有 generation；冷启动前才判断 443 是否被外部进程占用。
- 正式构建配置了 Caddy SHA 后，即使设置 `TUNNELBOARD_CADDY_PATH` 也必须验证字节摘要，不能借环境变量绕过供应链锁。

### 4.3 Runtime、SSH 与停止语义

- 同一 Forward ID 每次启动获得单调 generation；所有异步回调必须同时匹配 ID、generation 和实例。
- 首跳连接池按连接身份而非只按 Host ID 复用，凭据或地址变化后新旧 generation 可安全并存并最终回收。
- keepalive、端到端 probe、SSH 拨号和 active bridge 都有 timeout/cancel 边界。
- 多跳链中，首跳失败由连接池单飞重拨，末跳失败只重建该 Forward 的尾链。
- Stop 先取消 context，再关闭 listener、SSH lease 和活跃连接，最后在全局 deadline 内等待。

### 4.4 Route 与 Restore 事务

Route 的 Vault 目标、hosts、Caddy、CA 和 applied 状态由同一 revision 串行协调。未经用户确认的 CA 指纹会在任何 hosts/Caddy/CA 副作用前失败；删除联动和 Restore 激活也不能走旧入口绕过确认。

Restore 使用持久状态机：

```text
Stage（只读验证）
  -> candidate_prepared
  -> runtime_suspending（副作用前 write-ahead）
  -> runtime_suspended（持久化实际暂停集合）
  -> vault_replaced
  -> network_neutralized / quarantined
  -> 用户二次确认 Activate
```

补偿失败会保留 journal 并返回 `JournalPending`，不会删除唯一恢复线索。应用启动发现 quarantine 时会先幂等中性化，避免“仍标记隔离但 hosts/CA 已生效”。Host、Route、Legacy mutation 和更新 HTTP 都在取得锁后再次检查 recovery gate，关闭 TOCTOU 窗口。

### 4.5 应用契约、秘密与 UI 真实性

- 根级 Snapshot 返回非敏感 Catalog、Runtime、Route、Recovery、Capabilities 和 Preferences。
- Host、Route、Import、Restore、更新检查等高风险操作通过有类型高层命令提交 revision、command ID 和精确意图。
- WebView 只看到 `hasSecret` 和非敏感 DTO，不接收持久密码或私钥口令。
- UI 只在成功加载且集合确实为空时显示空状态；刷新失败保留旧状态并标记 stale。
- Restore pending/quarantine 在全局可见；激活恢复网络前再次展示 Forward 数量、Route/CA 影响并确认。
- 异步页面提供 loading、error、retry 和诊断入口，不再因 chunk 加载失败显示空白正文。

### 4.6 GitHub Actions 与发布产物

GitHub Actions 是正式产物的唯一构建者。Windows 工作流调用统一 Release Module，并验证：

- 主程序、Helper、Caddy、许可证和 manifest 完整存在；
- manifest 路径不能逃逸 bundle 根，bundle 根外的同名可执行文件不能被接受；
- 文件 SHA、目标平台、最低系统、PE amd64 架构与 Authenticode 发布者；
- Caddy 下载归档和最终二进制均匹配平台/架构锁定摘要；
- Release 只创建 draft，未通过门禁不能自动公开。

Linux 当前明确标记为未发布，不能用交叉编译成功冒充完整交付。

## 5. 第二轮对抗性复核发现与修复

| 级别 | 第二轮发现 | 处理结果 |
| --- | --- | --- |
| P1 | Restore 补偿失败仍删除唯一 journal | 失败时保留 journal，返回 `JournalPending`，新增同进程补偿失败测试 |
| P1 | `SuspendAll` 成功到 journal checkpoint 间有崩溃窗口 | 新增 `runtime_suspending` write-ahead，并持久化实际暂停集合 |
| P1 | Restore 激活无持久事务，可能 quarantine 与网络副作用并存 | 启动时对 quarantine 幂等中性化；激活继续受 coordinator/gate 管理 |
| P1 | Host/Route 在锁外检查 gate，存在 TOCTOU | 获取 mutation lock 后复核完整 recovery gate，并加确定性交错测试 |
| P1 | LegacyMutation 在 journal pending 时仍可写 Vault | Legacy 写入口和 Capability 统一阻断 pending/quarantine/maintenance |
| P1 | 旧 Route、删除联动和 Restore 激活可绕过 CA 指纹确认 | Router 与 Application 两层强制确认，副作用前失败 |
| P1 | 旧机器 CA 迁移遗漏自定义 `config.root` 和代输 UAC 原用户 | 支持旧配置根；通过父进程身份解析原用户 profile |
| P2 | 更新 HTTP 可与 Restore 交错，启动触发可顺序重复 | 纳入统一网络 gate；每应用生命周期缓存 startup 结果，至多一次 |
| P2 | Snapshot 丢 Runtime/Recovery/Capabilities，查询失败显示 stopped | 根 Snapshot 透传事实；失败保留旧事实并显示错误 |
| P2 | Restore 隔离和激活影响仅 Settings 可见，且缺二次确认 | 新增全局提示、影响摘要与激活确认 |
| P2 | available 后成功确认无更新仍保留旧徽标 | reducer 在 `up_to_date` 时清除旧版本，保留失败时的已知更新 |
| P2 | 异步页面 chunk 失败时正文空白 | 新增 PageLoader 的错误、重试与诊断状态 |
| P2 | 正式 Caddy SHA 可被路径环境变量绕过 | 正式 pin 存在时环境变量目标同样强制哈希验证 |
| P2 | Artifact verifier 接受 bundle 根外文件且缺架构/签名约束 | 收紧根边界、PE 架构、目标平台、最低系统和正式签名发布者验证 |
| P2 | Windows smoke 仍读取 `build/bin` 和工作区 Caddy | 改为解包最新真实 bundle，并只使用 bundle 内主程序、Helper 与 Caddy |

第二轮未发现新的 P0。上述 P1 均已修复并增加相应的故障注入、负向或交错测试。

## 6. 最终验证证据

| 验证 | 结果 |
| --- | --- |
| `pnpm test` | 通过，41/41 |
| `pnpm run build` | 通过；入口 JS 207.81 KiB，全部 JS 436.49 KiB，预算通过 |
| `go test -count=1 ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race -vet=off -p 1`（application、biz、forward、helper、caddy、route、vault） | 通过 |
| `uv run -m unittest scripts.tests.test_release -v` | 通过，12/12 |
| `uv run scripts/package-windows.py --version 0.0.0-verification --skip-installer` | 通过，生成 Windows bundle、manifest、SHA256SUMS |
| `uv run -m py_compile scripts/smoke-windows.py` | 通过 |
| Windows bundle 真机 smoke | 本地未签名 Helper 在 UAC 前被 Authenticode 门禁拒绝；无系统副作用，必须改由正式签名 CI 产物执行 |

本地生成的候选文件：

- `TunnelBoard-0.0.0-verification-windows-x64.bundle.zip`
- `TunnelBoard-0.0.0-verification-windows-x64.manifest.json`
- `SHA256SUMS`

Wails 生成绑定包含新的 Restore 激活预览/确认命令；Wails 仍输出既有的 `time.Time` 类型生成警告，但绑定生成和完整 Windows 应用编译成功。

## 7. Browser/UI 验证说明

安装内置 Browser 插件后，插件连接和标签页管理已恢复，但 Browser 对 `http://127.0.0.1:4173/` 的自动化页面读取仍被本地 URL 安全策略拒绝。遵循工具策略，本轮没有绕过限制，也没有把源码或 DOM 推断描述成真实截图验收。

因此 UI 证据来自：

- 41 个前端状态机、契约、竞态和可访问性测试；
- 生产构建和真实拆包预算；
- Vue 组件与五种语言资源的直接复核；
- Wails 完整 Windows 构建。

正式候选发布前仍应由人工或允许访问本地 Wails WebView 的 Runner 补一轮最小窗口、200% 缩放、键盘焦点和五语言视觉检查。

## 8. 剩余项与发布门禁

### 8.1 仍保留的 P3

ARCH-01 尚未完全结束：六个页面/根组件仍存在旧 Wails 只读绑定或轮询，ApplicationClient 还不是唯一事实入口。当前高风险写入已迁移并受 revision/gate 保护，因此不再按发布阻断处理；后续应逐页迁移查询和 Runtime 事件订阅，再删除旧绑定。

### 8.2 正式发布前必须完成

1. 在 GitHub Actions 配置正式 Windows Authenticode 证书和可信发布者门禁。
2. 从 Actions artifact 下载候选 bundle，运行 verifier，而不是验证工作区残留文件。
3. 对该签名 bundle 运行 `scripts/smoke-windows.py`，确认同一应用生命周期仅首次触发 UAC、hosts/CurrentUser CA/Caddy/HTTPS 闭环通过且清理完整。
4. 在真实进程 kill/断电场景抽查 Restore journal 恢复；自动化故障注入已经覆盖状态窗口，但不能完全替代操作系统级中断。
5. macOS/Linux 只有在对应真机授权、撤销和打包门禁完成后才能宣称交付；Linux 当前保持“不发布”。

### 8.3 已知非阻断限制

- 代输 UAC 的旧 CA 迁移发生在用户首次触发会话 Helper 时；如果升级后从未使用任何 Helper 能力，安装阶段无法可靠知道原标准用户配置。
- 安装器签名由外部 Authenticode 门禁验证，不把安装器自身签名状态写回其内嵌 manifest，避免形成自引用摘要循环。
- 本地未签名开发包不能运行正式 Helper，这是一项有意的 fail-closed 约束，不应增加开发绕过开关。

## 9. 最终建议

当前分支可以进入代码合并和签名 CI 验证阶段，但不要直接公开 Release。最合理的下一步是推送 `codex/mvp-remediation`，让 GitHub Actions 生成一个 draft 候选；只有签名 bundle verifier、Windows 真机 smoke 和人工 UI 检查全部通过后，才将 draft 转为正式发布。
