# TunnelBoard MVP 对抗性审查与整改方案

## 1. 文档目的

本文记录对 Kimi 在 `docs/architecture/mvp-modules-and-delivery-plan.md` 基础上完成的 MVP、前端重构和后续 SSH 连接复用改动的对抗性审查结果，并给出可以直接拆解实施的整改方案。

本次审查结论是：**当前版本不应合并到发布分支，也不应制作正式 Release**。迭代 0～4 的主要业务能力已经基本落地，Windows 本机垂直链路也能够跑通，但仍存在一个可直接导致本机 SYSTEM 权限执行的 P0 问题，以及多项安全边界、运行时一致性、连接生命周期、发布产物和 UI 状态真实性问题。

本文只基于代码、测试、构建和真实 Windows 冒烟结果，不使用 Code Security 或其不可见报告。

## 2. 审查范围与方法

### 2.1 固定范围

- 计划基线：`3dfc328`（`docs: add TunnelBoard MVP delivery plan`）
- 审查终点：`4789a56`（`feat: show ssh connection sharing tree on overview`）
- 代码范围：`3dfc328..4789a56`
- 变化规模：175 个文件，约 19,003 行新增、15,136 行删除
- 需求证据：`docs/architecture/mvp-modules-and-delivery-plan.md`、`logs/kimi-session -record .md` 及相关 ADR、handoff 文档

### 2.2 已执行验证

| 验证 | 结果 | 说明 |
| --- | --- | --- |
| `go test ./...` | 通过 | Go 全量单元测试通过 |
| `go vet ./...` | 通过 | 未发现 vet 阻断项 |
| `go test -race ./internal/vault ./internal/biz ./internal/forward ./internal/helper ./internal/caddy ./internal/diag ./internal/route` | 通过 | 重点模块 race 测试通过，但未覆盖本文列出的故障交错 |
| `pnpm run build` | 通过并有告警 | 单个 JS chunk 约 531.32 KiB，超过 Vite 500 KiB 告警阈值 |
| `uv run scripts/smoke-windows.py` | 通过 | Helper pipe、hosts、Caddy、CA 信任、HTTPS curl 的 Windows 真实链路通过 |
| 五种 locale key 集合核对 | 通过 | `en`、`ru`、`zh-CN`、`zh-HK`、`zh-TW` key 一致 |
| 浏览器视觉验收 | 未完成 | 本地 URL 被浏览器工具的安全策略拦截，UI 结论来自 DOM、组件逻辑和样式源码审查，不冒充截图验收 |

### 2.3 风险等级

- **P0**：可形成明确的本机提权/系统级破坏链，必须立即处置。
- **P1**：发布阻断项；可造成安全边界失效、核心功能错误、永久阻塞或产物不可用。
- **P2**：应在 MVP 正式发布前修复；会造成数据/状态误导、资源耗尽、可访问性或维护性问题。
- **P3**：架构债务；不一定独立阻断发布，但会显著提高后续修改成本和缺陷概率。

## 3. 总体结论

### 3.1 已达到的目标

- 产品身份、商业模块、遥测和 AI Debug 清理基本完成。
- Vault、新领域模型、SSH 主机指纹、三种 Forward、Route、hosts、Caddy、备份与诊断主流程已经实现。
- Windows Helper、hosts、Caddy 和本地 CA 的真实垂直链路可以工作。
- 连接池、首跳复用和概览展示已有实现与单元测试。
- 前端已形成 Overview、Forwards、Hosts、Routes、Logs、Settings 的完整页面结构，多语言 key 保持一致。

### 3.2 未达到的目标

- “受限特权辅助服务”没有形成可信安装边界，当前实际安装方式可把普通用户可写文件作为 SYSTEM 服务执行。
- Helper 的 CA 能力仍然把“安装用户 SID”当作“可信应用”，不符合最小授权原则。
- Caddy 的固定无认证 Admin API 允许本机其他账户控制受害者用户的 Caddy。
- Forward、连接池和 Route 的并发代际、停止、探活与事务模型还不足以保证故障后的状态一致性。
- CI 没有使用完整打包入口，干净环境的正式产物可能缺少 Helper 或 Caddy；跨平台迭代 5 未完成。
- 前端在多个失败路径会把“未知/失败”渲染成“空、关闭或停止”，不满足“界面所见即真实状态”。

## 4. 问题总表

| ID | 级别 | 问题 | 主要位置 |
| --- | --- | --- | --- |
| SEC-01 | P0 | SYSTEM 服务直接注册当前 Helper 路径，普通用户可替换服务二进制 | `internal/helper/service_windows.go:33-52` |
| SEC-02 | P1 | 同 SID 任意进程可让 Helper 安装任意自签根 CA | `internal/helper/pipe_windows.go:20-35`、`internal/helper/protocol.go:86-96`、`internal/helper/ca.go:18` |
| SEC-03 | P1 | 固定无认证 Caddy Admin API 可被其他本机账户控制或冒充 | `internal/route/caddy.go:151`、`internal/caddy/adapter.go:121-204` |
| RUN-01 | P1 | 旧 watcher 可删除同 ID 的新一代 Forward | `internal/biz/runtime.go:256-283` |
| ROUTE-01 | P1 | 已运行的自有 Caddy 被误判为 443 端口冲突 | `internal/biz/router.go:138-193` |
| SSH-01 | P1 | 池级 keepalive 没有超时，黑洞连接可永久阻塞共享链路 | `internal/forward/conn_pool.go:66-89`、`internal/forward/probe.go:18-22` |
| SSH-02 | P1 | Forward Stop 不关闭全部活跃桥接连接且无等待上限 | `internal/forward/port_forward.go:199-229` |
| SEC-04 | P1 | Linux 提权路径使用 `pkexec sh -c` 拼接；macOS 也存在双层转义风险 | `internal/helper/local_linux.go:23-36`、`internal/helper/local_darwin.go:11-34` |
| REL-01 | P1 | CI 裸跑 `wails build`，未调用完整 Windows 打包脚本 | `.github/workflows/build.yml:180-189`、`scripts/package-windows.py:60-86` |
| SSH-03 | P1 | 连接池只按 Host ID 复用，编辑地址/用户/凭据后仍可能走旧连接 | `internal/forward/conn_pool.go:144-181`、`app.go:249-254` |
| DATA-01 | P2 | 完全还原在确认、解密和校验前停止全部 Forward，且未原子清理旧 Route 副作用 | `app.go:417-426`、`internal/biz/backup.go:303` |
| SSH-04 | P2 | 多跳链只探活首跳，末跳静默失效可能不触发重连 | `internal/forward/conn_pool.go:202-219`、`internal/forward/port_forward.go:434-472` |
| ROUTE-02 | P2 | Route/hosts/Caddy 缺少全局事务协调，固定临时文件和旧快照回滚可互相覆盖 | `internal/biz/router.go:126-212`、`internal/helper/hostsfile.go:53-90` |
| SEC-05 | P2 | 备份读取、KDF 参数、实体数和私钥体积缺少合理资源预算 | `app.go:393-434`、`internal/vault/backup.go:39-52`、`internal/biz/backup.go` |
| SEC-06 | P2 | 完整 Vault 和导入冲突对象把密码/口令发送到 WebView | `app.go:225-230`、`internal/biz/backup.go:74-104` |
| PERF-01 | P2 | Caddy 日志不轮转，`GetLogTail` 从 offset 无界读到 EOF | `app.go:600-630`、`internal/caddy/adapter.go` |
| UI-01 | P2 | Vault 加载失败会清空所有数据，错误被伪装成空状态 | `frontend/src/App.vue:87-99` |
| UI-02 | P2 | Route 开关失败不回滚，配置状态与系统应用状态混淆 | `frontend/src/components/pages/RoutesPage.vue:245-267` |
| UI-03 | P2 | Route 状态加载中或失败时显示为“停止/未应用” | `frontend/src/components/pages/RoutesPage.vue:58`、`:383-408` |
| UI-04 | P2 | 批量移动逐项写 Vault，中途失败形成部分成功且 UI 不刷新 | `frontend/src/components/pages/ForwardsPage.vue:403-429` |
| UI-05 | P2 | 文件夹选择使用可点击 `div`，键盘和读屏语义不足 | `frontend/src/components/pages/ForwardsPage.vue:709-770` |
| UI-06 | P2 | 公共确认框及多组 Modal 缺少统一焦点、Escape、焦点恢复和对话框语义 | `frontend/src/components/common/ConfirmDialog.vue:33` 及 `frontend/src/components/modals/` |
| UI-07 | P2 | Forward 端口异步检查存在迟到响应覆盖新输入的竞态 | `frontend/src/components/modals/ForwardModal.vue:184` |
| UI-08 | P2 | 更新设置读取失败时默认联网，隐私策略 fail-open | `frontend/src/App.vue:197-204` |
| UI-09 | P2 | 折叠侧栏隐藏更新提示，展开态 `span @click` 不支持键盘 | `frontend/src/components/layout/AppSidebar.vue:74` |
| PERF-02 | P2 | 前端单 chunk 531.32 KiB，页面未按需加载 | `frontend/src/App.vue` 及页面静态 import |
| ARCH-01 | P3 | Wails 暴露约 40 个细粒度绑定，未收敛到计划中的应用 Module | `app.go:225-731` |
| ARCH-02 | P3 | Host 表单和多组 Modal 状态机重复，规则容易漂移 | `HostsPage.vue`、`ForwardModal.vue`、`ConfirmDialog.vue` |

## 5. 详细整改方案

### 5.1 SEC-01：安全迁移 Windows Helper 服务

#### 风险与证据

服务安装代码把当前运行的 Helper 路径直接传给 SCM，并设置为 `AUTO_START`、`LocalSystem`。真实 Windows 验证中，服务路径位于工作区 `build/bin/tunnelboard-helper.exe`，其父目录和文件对普通用户组具有 `Modify` 权限。这意味着普通用户可以替换二进制，等待服务重启后获得 SYSTEM 执行。

#### 推荐设计

1. 安装阶段先把 Helper 复制到管理员保护目录，例如 `%ProgramFiles%\TunnelBoard\Service\tunnelboard-helper.exe`。不要从下载目录、工作区、用户 AppData 或临时目录直接注册服务。
2. 关闭目标目录 ACL 继承，只保留：
   - `SYSTEM`：读取、执行及服务运行所需权限；
   - `Administrators`：完全控制；
   - 普通 `Users` 和安装用户：不得写入、删除、改名或修改父目录。
3. 复制采用“管理员目录内临时文件 → 校验 SHA-256/签名 → 原子替换”。升级时先停止服务，再替换，再核对 `ImagePath` 和版本握手。
4. 服务启动时校验自身路径必须位于受保护目录，并校验安装清单中的哈希或发布签名；条件不满足时拒绝服务能力并写 Event Log。
5. 增加旧安装迁移：检测用户可写路径的现有服务，停止并删除旧服务，再从受保护目录重新注册。迁移失败时不得继续使用旧服务。

仅在启动时比较一个同样存放于用户可写目录的哈希没有意义；信任根和被校验文件必须都位于管理员保护边界内。

#### 验收

- `sc.exe qc TunnelBoardHelper` 的 `BINARY_PATH_NAME` 必须位于受保护安装目录。
- 标准用户尝试覆盖、删除、改名 Helper 及其父目录必须失败。
- 篡改 Helper 后服务必须拒绝启动；合法升级后 `Ping` 协议版本与主程序完全一致。
- 从旧版用户可写服务路径升级后，不再残留旧服务路径和可启动副本。

### 5.2 SEC-02：收紧 Helper 和 CA 信任边界

#### 风险与根因

管道 DACL 只限制为 SYSTEM 和安装者 SID。同一 SID 下的任意进程都能调用 Helper。`trust_local_ca` 只检查证书为自签 CA 且声明哈希正确，因此同用户恶意进程可把任意根 CA 写入机器 Root store。

#### MVP 推荐方案

**优先把 TunnelBoard CA 改为当前用户证书库信任，并从常驻 SYSTEM Helper 中删除 CA 安装/删除能力。** Helper 只保留严格受限的 managed hosts 操作。这样浏览器信任作用域与运行 TunnelBoard 的用户一致，不把同 SID 进程能力放大到整机。

如果产品必须保留机器级信任，则必须同时满足：

1. CA 由受保护的安装/Helper 流程生成或登记，管理员保护区只保存唯一允许的指纹和轮换状态。
2. `trust_local_ca` 不再接受任意 DER，改成无参数的“信任已登记 CA”；更换 CA 必须重新显式提权确认。
3. Pipe 在 SID DACL 之外核验客户端 PID、受保护安装路径和发布签名。该校验是纵深防御，不能替代固定 CA 身份。
4. hosts 请求继续只允许标记化区块和回环地址，不接受任意目标路径、任意 IP 或 shell 命令。

不要通过检查证书 CN、组织名、主题字段或“首次见到即登记”来修复，这些值都可伪造，首次调用也可被抢占。

#### 验收

- 任意新生成的自签 CA 请求均被拒绝，系统/用户 Root store 零变化。
- 伪造相同 CN、不同指纹、重复轮换和未登记删除全部被拒绝。
- 其他 SID、同 SID 非签名副本均不能调用受保护操作。
- Helper 协议模糊测试不得绕过操作白名单、大小限制和字段组合校验。

### 5.3 SEC-03 与 ROUTE-01：关闭不可信 Admin API并建立 Caddy 所有权模型

#### 风险与根因

固定 `127.0.0.1:2019` 没有调用者认证。本机其他账户可以调用 `/load`、`/stop`，也可以先占用 2019 并对 `/config/` 返回 200，让 Adapter 把外部服务当成自有 Caddy。同时，Router 在自有 Caddy 已占用 443 时仍执行 bind 预检，导致第二条及后续 Route 被错误跳过。

#### MVP 推荐方案

1. 关闭 Caddy Admin API，Adapter 不再用 HTTP 可达性判断“自有进程”。
2. Caddy 作为主程序的受管理子进程：Windows 使用 Job Object，Unix 使用受控进程组；主程序退出或崩溃时子进程被回收。
3. Adapter 持有 `{processHandle, pid, startTime, generation}`。Stop 只允许终止当前 generation 的真实子进程，PID 文件不能单独作为所有权证据。
4. 配置更新采用：在应用数据目录写唯一临时文件 → 运行 Caddy 配置校验 → 原子替换正式配置 → 停止自有旧进程 → 启动新进程 → 等待 readiness。MVP 可以接受极短重启窗口，换取明确的本机信任边界。
5. 仅当不存在自有 Caddy 时预检 443；已有自有进程时走受控重启，不把自身占用识别为外部冲突。真正启动结果仍须兜底预检后的 TOCTOU。

随机 localhost 端口、固定自定义 Header 或仅检查 `/config/` 内容都不能构成安全边界。

#### 验收

- 其他 Windows 用户不能停止或重配 TunnelBoard Caddy。
- 外部进程预占 2019 不影响 TunnelBoard，也不会收到 `/load` 或 `/stop`。
- 自有 Caddy 正在服务 443 时，新增第二条 Route 成功进入新配置。
- 外部进程占用 443 时返回明确冲突，既不杀外部进程，也不假报 Caddy 已启动。
- 配置校验或新进程启动失败时，旧配置应保持或恢复可用，并产生明确诊断。

### 5.4 RUN-01：为 Forward Runtime 增加 generation

#### 推荐设计

- `runs` 从 `map[int]runHandle` 改为 `map[int]runEntry`，其中包含 `generation` 和 `run`。
- 每次 Start 为同一 Forward ID 分配单调递增 generation。
- watcher、事件处理和终态状态写入都携带 `{id, generation, run}`；只有与当前 entry 完全匹配才允许修改或删除。
- Stop 在锁内摘除指定 generation，在锁外执行实际 Stop，避免旧 watcher 影响后来启动的新代。
- Runtime 状态也记录 generation，旧代的 `disconnected/error/Done` 事件不得覆盖新代的 `running`。

#### 验收

- Stop A 后立即 Start B，再关闭 A.Done，B 仍在运行表且状态为 running。
- B 启动后 A 再发送 error/reconnecting，B 状态不变。
- `go test -race` 下同 ID 并发 Start/Stop 数百轮，无旧事件覆盖和数据竞争。

### 5.5 SSH-01、SSH-02、SSH-04：统一连接生命周期、探活和停止语义

#### 推荐设计

1. 建立 `probeSSH(ctx, client, timeout)`：`SendRequest` 在独立受控 goroutine 中执行，调用方同时监听 timeout、stop 和 client.Done。超时后主动关闭对应 transport，使阻塞调用退出。
2. interval 与 timeout 分离，例如 timeout 取 `min(max(interval/2, 3s), 10s)`，并保证同一连接同一时刻最多一个 probe。
3. `DialChain` 不再返回含义过宽的 `shared bool`，改为结构化结果，例如：
   - `TerminalClient`
   - `FirstHopLease`
   - `PooledPrefixLen`
   - `CloseTail`
4. 首跳由连接池探活；多跳末端只要不同于首跳，就由对应 Forward 探活。末跳失败只重建该 Forward 的尾链；首跳失败由池单飞重拨并通知租户。
5. 每个 LocalForward 建立生命周期 context 和活跃连接 registry。Stop 顺序固定为：标记 stopping → cancel → 关闭 listener → 释放 SSH lease/尾链 → 关闭全部活跃本地和远端连接 → 有界等待。
6. bridge 收到任一方向结束或 context 取消时关闭连接对。Stop 使用 3～5 秒 deadline；超时返回结构化 `ErrStopTimeout`，不得永久卡住 UI、恢复或应用退出。

#### 验收

- fake `SendRequest` 永不返回时，timeout 后 transport 被关闭，probe goroutine 退出。
- H1→H2 中 H1 正常、H2 黑洞时，Forward 进入 reconnecting，只重建 H2 尾链。
- H1 失败时，多条共享 Forward 并发重连只发生一次首跳拨号。
- 活跃 TCP 会话中 Stop，客户端及时收到 EOF，goroutine 数回落。
- 多条 Forward Shutdown 总耗时受全局 deadline 约束，不按单条无限累加。

### 5.6 SSH-03：让连接池复用键反映真实连接身份

#### 推荐设计

- 为 SSHHost 计算不含明文秘密的 `ConnectionIdentity`，至少包含规范化 Host、Port、User、AuthType、凭据版本、代理/超时等影响连接的字段。
- Pool entry 以 `(hostID, identity)` 或 `(hostID, generation)` 为键。ID 相同但 identity 变化时，新 Acquire 必须创建新连接；旧连接只服务已有 lease，引用归零后关闭。
- 增加 `InvalidateHost(id)` 供删除、强制断开和密钥轮换使用。
- 保存正在被运行 Forward 引用的主机时，后端返回结构化 `HostInUse`，UI 明确让用户选择“仅影响新连接”或“停止并重启受影响 Forward”。
- 如果选择重启，执行一个后端编排命令：收集运行集合 → 有界停止 → 保存 → 失效旧代 → 重启原集合，并返回逐项结果。

这种按 identity 换代的方式比“编辑后粗暴 CloseAll”更稳妥，不会无故打断不相关 Forward。

#### 验收

- 修改地址、端口、用户、认证方式或凭据后，新 Forward 必须拨新目标。
- 只改显示名称/备注是否复用应明确写成规则并由测试锁定。
- 旧 lease 未释放时新旧两代可并存；最后一个旧 lease 释放后旧连接关闭。

### 5.7 ROUTE-02：把 Route 系统副作用收敛为串行事务

#### 推荐设计

1. RouterBiz 增加单一 reconcile coordinator，Apply、Remove、Resume、Restore 清理等所有系统副作用入口串行执行。
2. Route 保存与系统应用不要继续由前端拼成两个无事务调用；收敛成一个后端命令，并携带 `desiredRevision`。
3. hosts、Caddy、CA 都从同一 Vault revision 编译目标状态。
4. 建立轻量 journal：
   - 写 `pending {txID, beforeRevision, desiredRevision}`；
   - 串行应用 hosts、Caddy、CA；
   - 成功记录 `appliedRevision` 并清 pending；
   - 失败按本事务 before snapshot 逆序补偿。
5. 补偿前检查 txID/revision，旧事务不得覆盖更新事务。
6. 临时文件使用同目录随机唯一名称，完成写入、flush 和必要的目录同步后再原子 rename；不要并发复用固定 `.tmp/.bak`。
7. RouteStatus 只读取最后 applied 状态和错误，不在查询路径产生探测副作用。

#### 验收

- Apply+Apply、Apply+Remove、Resume+Apply 强制交错后，最终 hosts/Caddy/CA 与最高 revision 完全一致。
- 旧事务迟到回滚不得覆盖新事务成功结果。
- 在每个副作用步骤注入失败或模拟崩溃，重启后能根据 journal 收敛到已提交目标。

### 5.8 DATA-01 与 SEC-05：重做备份 Stage/Commit 和资源预算

#### 推荐设计

把完全还原拆为两个真正的业务阶段：

1. `StageRestore`：确认文件大小 → 限制读取 → 校验 KDF 参数 → 解密 → 校验 schema、引用完整性和数量预算 → 生成预览。此阶段不得 Shutdown、写 Vault、改 hosts、改 CA 或触碰 Caddy。
2. `CommitRestore`：验证用户已确认且 staged 文件摘要未变化 → 获取应用级 mutation lock → 保存旧 Vault 和运行集合 → 有界停止 Runtime → 把旧 Route 收敛到安全中性态 → 原子替换 Vault → 清 applied Route 状态。
3. 恢复后的 AutoStart、hosts、Caddy 默认不自动生效；由用户在预览中显式选择，避免恢复操作隐式改变网络行为。
4. Vault 写入失败时恢复旧 Vault，并 reconcile 旧 Route；补偿失败必须以复合错误和 recoverable pending 状态暴露，不能只返回最后一个错误。

统一资源预算建议：

- 备份密文总大小使用 `Stat` 加 `io.LimitReader(max+1)` 限制，Preview、Import、Restore 共用入口。
- Argon2 默认保持 64 MiB；解析上限建议 memory ≤ 256 MiB、time ≤ 6、parallelism ≤ `min(8, CPU)`。
- 单进程同一时刻只允许一个 KDF/导入任务。
- 限制实体总数、单字符串长度、单私钥文件和私钥总大小。
- 导入前一次扫描每类实体最大 ID，之后递增分配，消除逐项 `nextID` 的 O(n²) 扫描。

#### 验收

- 错密码、损坏包、未确认、非法引用均不得调用 Shutdown、Helper、Caddy 或 Vault Update。
- 1 GiB KDF 包头必须在 Argon2 分配前拒绝；超大文件最多读取 `max+1` 字节。
- 成功恢复后旧 Forward 停止、旧 managed hosts/Caddy/CA 清理、新 Vault 原子落盘且默认不自动联网。
- 恢复各步骤故障注入后，要么完整回到旧状态，要么留下可恢复且可诊断的 pending 状态。

### 5.9 SEC-04：移除提权命令字符串拼接

#### 推荐设计

- Linux 完全移除 `pkexec sh -c`，分别调用固定可执行文件和 argv，例如 `pkexec cp -- src fixedDst`、`pkexec rm -f -- fixedDst`、`pkexec update-ca-certificates`。
- 如果确实需要复合事务，安装一个内容固定、root 所有且普通用户不可写的专用 helper，不动态生成 shell 字符串。
- macOS 通过 `osascript` 的 argv 传路径，并在 AppleScript 内使用 `quoted form of item n of argv`；不要把路径插入 AppleScript 源码中的双引号字符串。
- CA 删除必须依据受保护状态中登记的实际证书身份，而不是只相信调用方传入哈希。

#### 验收

- `TMPDIR`/用户路径包含空格、分号、`$()`、反引号、单双引号和换行时，外部进程收到的 argv 与原路径完全一致。
- 授权取消、命令失败和中断后临时文件全部清理，注入标记文件绝不能产生。
- Linux/macOS 真机分别完成 hosts、信任、撤销和重复撤销回归。

### 5.10 SEC-06：禁止秘密进入 WebView

#### 推荐设计

- 新增 `VaultView`/`SSHHostView` DTO。响应中删除 Password、私钥口令等秘密，只返回 `hasSecret`、认证类型和非敏感展示字段。
- 保存请求显式使用 `secretAction=keep|replace|clear`，由后端合并旧秘密；不能再用空字符串同时表示“保留”和“清空”。
- 导入冲突预览只返回 name、host、port、user、authType 等比较字段，不返回完整 SSHHost。
- 前端提交新秘密后立即清空局部状态；日志、错误详情、诊断包和持久化 store 都禁止包含秘密。

#### 验收

- Wails 响应 JSON、导入预览、运行快照、诊断包和日志中均搜索不到已知测试秘密字节。
- 编辑时 keep/replace/clear 三种动作分别正确，且后端接口测试覆盖空值语义。

### 5.11 PERF-01：日志轮转与有界 tail

#### 推荐设计

- TunnelBoard 和 Caddy 日志统一采用大小轮转，例如单文件 10 MiB、保留 5 个、旧文件压缩；具体预算应形成配置常量。
- `GetLogTail` 增加单次最大返回字节数，不再 `io.ReadAll` 到 EOF；使用文件大小和 `ReadAt` 分段读取。
- 返回 `{generation, nextOffset, truncated, rotated}`。文件轮转或缩短时前端能重置 offset，而不是静默漏日志。
- 日志内容继续经过脱敏层，限制超长单行，避免攻击者构造一行撑满前端内存。

#### 验收

- 连续写入超过轮转阈值时磁盘占用保持在预算内。
- 100 MiB 日志的单次 tail 内存和返回体不超过设定上限。
- 轮转后前端能够继续读取，不重复、不无限增长、不白屏。

### 5.12 UI-01～UI-09：以“真实状态”重构前端异步交互

#### Vault 加载

- `loadVault` 使用 `idle/loading/ready/refreshing/error` 状态机。
- 首次失败显示错误页和重试按钮；刷新失败保留旧快照并显示“数据可能不是最新”，绝不清空数组后展示“暂无数据”。
- 增加请求序号，迟到响应不得覆盖更新快照。

#### Route 开关与状态

- 不再通过取反当前对象推导目标值，直接使用事件的 `checked`。
- 每条 Route 保存 previous、desired、persisted、applied 四类状态。保存失败时回滚；保存成功但系统应用失败时显示“已启用但未应用”，并提供重试应用。
- 每条 Route 操作期间禁用重复点击；`finally` 重新拉取 Vault 和 RouteStatus。
- 状态使用 `loading/unknown/applied/notApplied/conflict/error`，加载失败不能显示成 stopped。

#### 批量移动

- 首选后端提供原子 `MoveForwards(ids, targetFolderID)`，在一次 Vault Update 中验证并提交。
- 返回成功/失败摘要；若因业务要求允许部分成功，UI 必须在 finally 刷新，只保留失败项选中并支持重试，禁止每项弹一个 toast。

#### 端口预检

- 使用递增 request token；响应落地前核对 token、弹窗可见性、mode、host 和 port 快照。
- 关闭弹窗立即使 token 失效。预检只能是提示，最终仍以真实 bind 结果为准。

#### 可访问性

- 文件夹节点优先改成原生 button；当前节点提供 `aria-current` 或完整 tree/treeitem 键盘语义。
- 抽取 `BaseDialog.vue`：`role=dialog`、`aria-modal`、`aria-labelledby`、初始焦点、Tab 陷阱、Escape、关闭后焦点恢复、背景 inert、滚动锁。
- 危险确认默认聚焦取消按钮，busy 时禁止重复提交。
- 更新徽标改成可聚焦 button，折叠侧栏仍显示可发现的更新入口。

#### 更新检查隐私

- `GetUpdateCheckEnabled` 读取失败时默认不联网并显示设置读取错误。
- 只有后端明确返回 true 才执行启动时检查；用户手动点击检查更新仍可联网。

#### 前端验收

- 所有空状态只能在数据成功加载且确实为空时显示。
- 异步开关覆盖保存失败、应用失败、超时和快速连点。
- 模态框可用 Tab、Shift+Tab、Enter、Escape 完成操作，关闭后焦点回到触发元素。
- 200% 缩放和最小窗口下无关键控件裁切，所有语言下无硬编码英文 fallback。

### 5.13 PERF-02 与 ARCH-02：拆首包并收敛重复表单

#### 推荐设计

- 用 `defineAsyncComponent(() => import(...))` 按页延迟加载 Forwards、Hosts、Routes、Logs、Settings，Overview 保留在首包。
- 异步页面加载时显示轻量骨架，加载失败显示可重试错误，不出现白屏。
- 先按页面拆包，再根据构建分析决定是否拆 Vue/vendor、按需引入 Bootstrap；不要单纯提高 Vite 告警阈值。
- 抽取 `createDefaultSSHHost`、`normalizeSSHHostPayload`、`validateSSHHost` 和可复用 `SSHHostFields.vue`，保证 Hosts 页面与 Forward 内嵌建主机使用同一规则。
- 抽取 `useConfirmDialog` 和 `BaseDialog`，消除页面各自维护相似确认状态机。

#### 验收

- 构建不再出现单 chunk 超 500 KiB，首包 gzip 明显低于当前约 154 KiB。
- 同一 Host 输入在两个入口生成相同 payload 和错误；新增认证字段只修改一套领域规则。
- 冷启动和首次进入异步页面无空白，弹窗暴露方法和页面状态仍正常。

### 5.14 REL-01：建立唯一打包入口和产物清单

#### 推荐设计

1. 暂停当前自动正式 Release，直到产物清单门禁完成。
2. 每个平台只有一个打包入口，GitHub Actions 必须调用它，而不是裸跑 `wails build`。
3. 生成 manifest，记录版本、GOOS/GOARCH、文件清单、SHA-256、Go/Wails 版本和 Caddy 版本。
4. Windows 产物必须包含主程序、Helper、`caddy/caddy.exe`；CI 直接调用并适配 `scripts/package-windows.py`。
5. macOS App bundle 必须包含对应架构的 Caddy；Linux 必须有真实构建和产物任务。Caddy 版本与 SHA 按平台/架构映射。
6. 每个 artifact 解包后运行无特权 self-check：文件存在、SHA 正确、Caddy 可在高端口启动、配置可加载。
7. Windows 再运行现有 Helper/hosts/Caddy 真实 smoke；macOS/Linux 特权操作必须在真机门禁中逐项通过，未验证时不得宣称跨平台完整交付。

#### 验收

- 从干净 checkout 生成 artifact，删除 Helper/Caddy 或替换任一字节，CI 必须失败。
- Release 门禁验证下载后的 artifact，而不是工作区可能残留的 `build/bin`。
- Windows、macOS、Linux 的 manifest 与运行时定位规则一致。

### 5.15 ARCH-01：逐步收敛应用 Module

不建议一次性把全部 Wails 绑定改成一个无类型大 `Execute(map[string]any)`。应保持类型安全，分三步迁移：

1. 新增聚合 `GetSnapshot`，一次返回前端首屏需要的非敏感 VaultView、Runtime、Route 和设置状态。
2. 按领域定义有类型 command DTO，并通过一个应用服务统一执行、校验、加 mutation lock 和返回结构化错误。
3. Runtime 状态改为事件推送，页面轮询只保留断线兜底；迁移完成后删除旧细粒度绑定。

应用 Module 只做用例编排和 DTO 转换，Vault、Router、Runtime 的业务不变量仍留在各自深 Module 中。

## 6. 建议实施顺序与提交边界

每一项使用独立 Conventional Commit，先写故障复现测试，再改实现：

1. `fix(helper): install windows service from protected directory`
2. `fix(helper): remove arbitrary machine root trust capability`
3. `fix(caddy): replace unauthenticated admin api with owned process lifecycle`
4. `fix(runtime): isolate forward generations from stale watchers`
5. `fix(forward): bound keepalive and stop lifecycles`
6. `fix(forward): version pooled connections by host identity`
7. `fix(route): serialize route reconciliation by revision`
8. `fix(backup): stage restore before runtime side effects`
9. `fix(backup): enforce import resource budgets`
10. `fix(helper): remove shell command interpolation on unix`
11. `fix(app): keep ssh secrets out of webview snapshots`
12. `fix(ui): preserve backend state on refresh failures`
13. `fix(ui): reconcile route toggles with applied state`
14. `fix(ui): make batch moves atomic`
15. `fix(ui): add accessible modal and navigation primitives`
16. `perf(ui): lazy load secondary application pages`
17. `fix(logs): rotate files and bound incremental reads`
18. `ci(release): package and verify complete platform artifacts`
19. `refactor(app): introduce typed snapshot and command facade`

依赖关系上，SEC-01～SEC-03 必须最先完成；RUN/SSH/ROUTE 可靠性必须在备份 Restore 事务之前完成；UI 必须建立在后端结构化状态和原子命令之上；发布流水线最后验证真正完成的产物，而不是提前掩盖缺失文件。

## 7. 发布门禁

以下条件全部满足前，不应解除“禁止正式发布”：

- SEC-01 的旧服务已经从实际测试机卸载或安全迁移，服务路径和 ACL 验证通过。
- SEC-02、SEC-03、SEC-04 的攻击路径均有负向自动化测试，Windows/Linux/macOS 对应真机项有证据。
- Runtime generation、Stop deadline、keepalive timeout、多跳尾链和池换代测试在 `-race` 下通过。
- Route 并发交错、故障补偿和崩溃恢复测试通过。
- 错密码/损坏备份零副作用，恶意 KDF/大文件/大实体集在预算前拒绝。
- Wails 响应、日志和诊断包中不存在 SSH 密码或口令。
- 前端失败状态不再伪装成空、关闭或停止；键盘与焦点验收通过。
- 干净 checkout 到 Release artifact 的完整链路验证 Helper、Caddy、manifest 和 self-check。
- Windows 真实 smoke 重跑通过；macOS/Linux 未完成的真机项必须明确标为未交付，不能用编译通过代替验收。

## 8. 当前机器的立即处置建议

本次 Windows smoke 安装的 `TunnelBoardHelper` 是自动启动的 LocalSystem 服务，且审查时确认其二进制位于普通用户可修改的工作区。继续保留该状态本身就是风险。

在 SEC-01 修复完成前，应由管理员明确执行以下二选一操作：

1. 暂时停止并卸载 `TunnelBoardHelper`；或
2. 将其迁移到管理员保护目录，收紧 ACL，重新注册并核验 `ImagePath`。

不要只把当前文件设为只读，也不要只修改单个文件 ACL而保留可写父目录；攻击者仍可能通过替换、改名或目录权限完成劫持。
