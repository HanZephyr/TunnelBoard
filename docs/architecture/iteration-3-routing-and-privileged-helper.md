# 迭代 3 设计：本地路由、受限特权辅助服务与内置 Caddy

本文定义迭代 3 的技术设计，决策依据为 `CONTEXT.md`、[ADR 0002](../adr/0002-local-domain-routing-and-tls.md)、[MVP 计划](mvp-modules-and-delivery-plan.md) 与交接文档。首发平台 Windows x64；所有特权组件先定跨平台 Interface，macOS/Linux Adapter 后置。

## 1. 模块总览与数据流

```
UI → 应用 Module → 本地路由 Module（biz/router）
                      ├─ Vault（WebRoute/Forward 持久态）
                      ├─ hosts 规划器（纯函数：routes → 区块内容）
                      ├─ Caddy 编译器（纯函数：routes → caddy.json）
                      ├─ 特权辅助服务 Client（命名管道 IPC）
                      │     └─ tunnelboard-helper.exe（Windows 服务，SYSTEM）
                      │           ├─ 受托管 hosts 区块读写/回滚
                      │           └─ 本地 CA 信任/撤销（certutil）
                      └─ Caddy Adapter（进程管理：定位/校验/启停/重载/443 诊断）
```

解耦不变量（计划文档）：Caddy 与 hosts 不知道 SSH；路由模块只引用 Forward 的本地端口数字；特权服务不接收任意命令。

## 2. 受限特权辅助服务（Windows）

**形态**：独立二进制 `tunnelboard-helper.exe`（`cmd/helper`，与主程序同模块构建），安装为 Windows 服务（自动启动，SYSTEM）。首次使用特权功能时由主程序触发安装：以 `runas` 提升运行 `tunnelboard-helper.exe -install`（一次 UAC）。主程序任何时刻不以管理员运行。

**操作白名单**（与计划文档 Interface 对齐）：

| 操作 | 参数 | 语义 |
| --- | --- | --- |
| `EnsureInstalled` | — | 服务存在且版本匹配；否则安装/更新 |
| `ApplyManagedHosts` | `entries[]{domain, ip}` | 原子重写受托管区块；写前备份，失败自动回滚 |
| `RemoveManagedHosts` | — | 清空受托管区块（保留标记外内容） |
| `TrustLocalCA` | `certDer[]byte, sha256` | 仅当证书 SHA-256 与声明值一致且 Subject 含 `TunnelBoard` 才写入 Root 存储 |
| `UntrustLocalCA` | `sha256` | 仅删除指纹匹配的该 CA |

**红线**：helper 拒绝执行白名单外请求；hosts 只触碰标记区块；CA 操作只认 TunnelBoard 本地 CA 指纹；不接受路径参数（hosts 路径写死 `%SystemRoot%\System32\drivers\etc\hosts`）。

**IPC**：命名管道 `\\.\pipe\tunnelboard-helper`，换行分隔 JSON 请求/响应。安全：管道 DACL 仅允许当前交互用户 SID（安装时由服务取安装用户）；helper 对请求做 schema 校验（域名/idna 合法性、IP 必须为 `127.0.0.1`、条目数上限 256、证书 DER 上限 16 KiB）。

**退化路径**：服务安装/更新失败时，`EnsureInstalled` 返回结构化错误；首发版本 UI 提示“需要安装受限辅助服务”并给出重试。每次操作单独 UAC 的退化模式列为后续增强（macOS 段落同样要求，届时跨平台统一）。

**受托管区块格式**（幂等、可识别、可回滚）：

```
# >>> TunnelBoard Managed (do not edit) >>>
127.0.0.1 db.test
127.0.0.1 grafana.example.com
# <<< TunnelBoard Managed <<<
```

写入算法：读全文 → 定位标记区块（无则追加）→ 替换区块内容 → 写 `<hosts>.tunnelboard.tmp` → 原子替换；写前把当前区块内容存入 `<hosts>.tunnelboard.bak`；新内容校验失败或写盘失败时从 .bak 恢复并返回错误。回滚是纯文件操作，不依赖 helper 常驻状态。

## 3. hosts 规划（biz，纯逻辑）

- 每条 `WebRoute.HostsEnabled=true` 且所属 Forward 为 local 模式的 Route 产生一行 `127.0.0.1 <domain>`；规划器输入 Vault 快照，输出排序后的条目集（确定性输出便于 diff 与测试）。
- 非 `.test`/`.localhost` 后缀域名：规划器将其标记为 `requiresConfirmation`，ApplyRoute 必须携带对应确认令牌（`confirmedDomains[]`），否则返回 `ErrDomainConfirmationRequired` 并附将写入的记录（CONTEXT.md:63）。
- `hosts 记录仅在用户明确移除它或删除对应 Forward 时撤销`（CONTEXT.md:67）：Route 停用 hosts 开关或 Route/Forward 删除时，下一次 Apply 自动从区块剔除；停止 Forward 不触发任何 hosts 变更。

## 4. 内置 Caddy Adapter

**交付与定位**：固定版本 Caddy 随安装包内置（打包流水线下载钉版二进制并生成 SHA-256 清单，代码内置清单校验）。运行时查找顺序：`TUNNELBOARD_CADDY_PATH`（开发/测试）→ 可执行文件同级 `caddy/caddy.exe` → 数据目录 `caddy/caddy.exe`；找不到或哈希不匹配返回结构化错误，绝不在首次使用时下载。

**运行模型**：全局单进程 `caddy run --config <数据目录>/caddy.json`，admin API 仅 `127.0.0.1:2019`（配置内关闭默认监听其他地址）。启动/停止/重载经 Adapter；重载优先 admin API `/load`，进程不在则冷启动。

**配置编译（纯函数，routes+forwards → JSON）**：

- 无启用 Caddy 的 Route → 不生成配置也不启动进程。
- 每个启用 Route 一个 server 块：listen `[127.0.0.1:443]`，host matcher = 域名；`tls internal`（Caddy 本地 CA，ADR 0002 禁 ACME——admin 配置同时关闭全局 ACME automation）。
- HTTP 上游：`reverse_proxy http://127.0.0.1:<forwardPort>`。
- HTTPS 上游：`reverse_proxy https://127.0.0.1:<port>`，transport `tls.server_name = <TLSSNI>`；上游 `Host` 可独立配置，未填写时兼容旧 Route 回退为 `<TLSSNI>`（ADR 0002）；不写任何 `insecure_skip_verify`，默认严格校验。
- 443 冲突（`DiagnosePort`）：启用首个 Route 前实际绑定 `127.0.0.1:443` 预检；冲突则不启动 Caddy，Route 保持 hosts-only（“域名 + Forward 端口”访问），返回占用错误（占用进程识别列为后续增强）。

**生命周期与 CA 信任编排**：

- 首个 Route 启用 Caddy：启动进程 → 从 admin API 取本地 CA 根证书 → `TrustLocalCA`（helper；服务已装，用户无感）。
- 最后一个启用 Caddy 的 Route 关闭/删除：停进程 → `UntrustLocalCA`（CONTEXT.md:65）。
- Caddy 崩溃/退出：RouteStatus 报告；不自动重启进程（保持行为可解释，重启由用户重新应用触发）。

## 5. 本地路由 Module（biz/router）

对齐计划接口：

- `PreviewRoute(routeID)` → 将写入的 hosts 记录、Caddy 配置摘要、需要的确认项（非本地域名、443 冲突、CA 信任）。
- `ApplyRoute(routeID, confirmedDomains[])` → 校验（Vault Validate + 确认令牌 + 443 预检）→ hosts Apply → Caddy 编译/重载 → CA 信任编排；任一步失败按逆序回滚并报告失败点。
- `RemoveRoute(routeID)` → 停用 hosts+Caddy：更新 Vault → 重算并 Apply hosts → 必要时停 Caddy/撤 CA。
- `RouteStatus()` → 每 Route 的 hosts 生效、Caddy 生效、端口冲突、证书信任状态。

Route 的创建/编辑沿用 `CatalogBiz`（WebRoute 模型已在 Vault，迭代 1 校验已强制 local-only 与 SNI 必填）。

## 6. 切片与验收对应

1. 本设计文档（`docs:`）。
2. hosts 规划器 + Caddy 配置编译器（纯函数 TDD：确定性输出、独立 SNI/Host 头、ACME 关闭、确认标记）。
3. 特权 helper：协议 + 白名单 + hosts 区块读写/回滚 + CA 信任（文件操作全部 TDD，管道/服务安装手工验证）。
4. Caddy Adapter：二进制定位/校验、进程管理、admin API 重载、443 诊断（用 `TUNNELBOARD_CADDY_PATH` 指到假二进制桩测试）。
5. biz/router：Preview/Apply/Remove/Status 编排（fake helper/caddy 接缝 TDD）+ app 绑定。
6. UI：Route 管理（域名、hosts/Caddy 开关、HTTPS SNI、上游 HTTP Host）+ 非本地域名确认对话框 + RouteStatus 展示。

迭代验收：同机多域名访问不同 Forward；hosts/Caddy 失败可解释、可精确回滚。端到端验证（真实浏览器访问 `https://db.test`）在 Windows 桌面会话手工完成并记录。
