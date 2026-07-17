# TunnelBoard 实现交接

## 下一会话目标

开始实现 TunnelBoard，先完成迭代 0 的产品基线与隐私清理；不要在本会话重新设计已确认的产品边界。

完整模块边界与交付顺序见：[MVP 模块划分与迭代计划](../architecture/mvp-modules-and-delivery-plan.md)。领域术语与已确认规则见仓库根目录的 [CONTEXT.md](../../CONTEXT.md)，加密和本地域名决策分别见：

- [ADR 0001：本地 Vault 与备份包加密边界](../adr/0001-separate-local-vault-and-portable-backups.md)
- [ADR 0002：自定义本地域名与本地 CA](../adr/0002-local-domain-routing-and-tls.md)

不要复制或推翻以上文档；以它们为准。

## 仓库状态

- 工作目录：`D:\Projects\GithubProjects\loris-tunnel-app`
- 当前分支：`main`
- 新远端：`git@github.com:HanZephyr/TunnelBoard.git`
- Fork：<https://github.com/HanZephyr/TunnelBoard>
- GitHub SSH 已在当前 PowerShell 环境验证可用。
- 本地领先 `origin/main` 两个尚未推送的文档提交：
  - `52998ba docs: define tunnel product decisions`
  - `3dfc328 docs: add TunnelBoard MVP delivery plan`
- 工作区存在未跟踪的 `.codegraph/`，属于用户已有工具索引；不得删除、修改或提交。

## 已确认且尚未写入现有决策文档的补充

这些是后续实现必须遵循的最新决定：

- 默认使用系统应用数据目录，不使用当前工作目录作为日常数据目录。
- 正式首发支持 Windows x64；macOS 同时目标 arm64 与 amd64。Linux 后置。
- 没有 Apple Developer Program：macOS 沿用上游的 universal DMG + ad-hoc 签名 + 用户首次手动放行模式。
- macOS 的 hosts/本地 CA 特权能力应先做 POC。优先尝试一次授权的受限 Helper；若某版本无法可靠安装/更新该 Helper，则退化为每次相关操作请求系统管理员授权。不能因此阻塞 Windows 的完整体验。
- 应用只自动检查更新，用户可在设置关闭检查；发现更新后只提供版本说明与手动下载，不实现自更新。
- 备份密码不可找回；本机 Vault 密钥遗失时不自动覆盖数据，只能导入备份或初始化空 Vault。
- 默认不持久化运行日志；仅保留本次运行的内存日志，排障时手动导出脱敏诊断包。
- “Forward 自动启动”和“系统登录时启动 TunnelBoard”是两个独立开关，均默认关闭，且不做联动提示。
- 撤销此前的局域网访问设想：所有 Forward 与 Caddy 都只允许回环监听；不支持 LAN 或公网访问，也不提供 LAN DNS、跨设备 hosts 或根证书分发。

## 当前代码基线

可复用：

- `internal/forward/`：SSH 本地、远程、动态转发，SSH chain，Agent，重连基础实现。
- `main.go`、`internal/traytext/`：Wails 托盘和显式退出生命周期。
- `internal/updater/`：GitHub Release 更新检查，可保留但须加开关并断开授权/遥测关联。

必须替换或移除：

- `internal/conf/`：当前为明文 TOML，并使用旧 `.loris-tunnel`/当前目录路径逻辑；改为系统目录中的加密 Vault。
- `internal/model/tunnel.go`、`internal/biz/jumper.go`、`internal/biz/tunnel.go`：演进为文件夹、SSH 主机、Forward、Web Route 新模型。
- `internal/license/`、`internal/aidebug/`、`internal/device/`、前端 `analytics.js` 与相关 `app.go` 绑定、页面/文案：移除。
- `app.go`：目前绑定面过大，逐步收敛为应用 Module；不要把 hosts、Caddy 或特权命令暴露给 Vue。

## 实施顺序

按计划文档执行，首个代码任务是迭代 0：

1. 产品身份更名：Go module、Wails title/output、配置目录、日志名、单实例 ID、前端文案与图标引用。
2. 删除授权、AI Debug、机器 ID 与遥测的代码、UI、配置和主动网络调用。
3. 保留更新检查，增加设置开关；只支持手动下载更新。
4. 保持现有托盘行为：运行中关闭主窗口最小化，显式退出才停止 Forward/Caddy。
5. 为删除行为与“无非必要网络请求”添加或更新测试。

迭代 0 完成后再进入 Vault 和新模型；不要提前在明文 TOML 上叠加新 Caddy、hosts 或备份功能。

## 实施注意事项

- 所有 SSH 主机指纹默认严格校验：首次由用户确认保存，变化阻断；不得保留上游默认绕过。
- Web Route 允许完整自定义域名；对非 `.test`/`.localhost` 的 hosts 覆盖必须明确确认。Caddy 总是 `tls internal`，HTTPS 上游需显式 SNI 并严格校验。
- Caddy 只能绑定回环 443；冲突时不启动，保留“域名 + Forward 端口”访问。Caddy 是随应用内置的固定版本，不在首次使用时下载。
- 主程序始终普通权限。Windows 特权辅助服务只允许受托管 hosts 区块与本地 CA 信任操作，绝不执行 UI 传来的任意命令。
- 导入备份默认追加到新顶层文件夹，不产生启动、hosts 或 Caddy 副作用；完全还原需要二次确认。

## 建议技能

- `implement`：执行每个已拆分迭代时使用，先以迭代 0 为范围。
- `tdd`：为 Vault、领域引用规则、主机指纹和 Caddy 配置编译等高风险逻辑先写测试。
- `codebase-design`：在收敛 `app.go`、设计 Vault 与特权辅助服务 Interface 时使用。

## 新会话建议起手式

1. 阅读本交接文档、`CONTEXT.md`、两份 ADR 和 MVP 计划。
2. 执行 `git status -sb`，确认 `.codegraph/` 仍未被触碰。
3. 先确认是否将已有两笔文档提交推送到 `origin/main`；不要把它们与迭代 0 的代码改动混为一次提交。
4. 开始迭代 0，保持一个提交只完成一项职责。
