# 端到端回归清单（迭代 5）

按 MVP 计划迭代 5 整理的发布前手工回归项。标记说明：`[W]` Windows 本机可验；`[M]` 需 macOS 真机；`[L]` 需 Linux 真机；`[A]` 已有自动化测试覆盖。

## 环境准备

- `uv run scripts/fetch-caddy.py` 获取钉版 Caddy（哈希须与 `caddy_bundle.go` 一致）。
- `uv run scripts/package-windows.py` 产出 `build/bin/`（主程序、helper、Caddy）。
- 准备一台可 SSH 的测试主机（密码 + 私钥各一），一个可占用 443 的占位程序（如 `python -m http.server 443` 需提权或改用其他占用方式）。

## SSH 主机指纹

- `[A]` 指纹库模型（首次确认、变化阻断、显式替换）：`internal/biz/trust_test.go`。
- `[W]` 首次启动 Forward 弹指纹确认 → 信任后启动成功；再次启动不再弹。
- `[W]` 在测试主机上更换 host key（或改指另一台同 IP 机器）→ 启动 Forward 弹“指纹已变化”警告并展示新旧指纹 → 取消则阻断；确认替换后启动成功。
- `[W]` 指纹变化后已断线的 Forward 不自动重连（`[A]` `internal/forward/errors_test.go` 已覆盖终态短路）。

## 端口与 Caddy

- `[A]` 编译器输出（SNI/Host 头/禁 ACME）：`internal/route/caddy_test.go`。
- `[W]` 占用 443 后启用 Caddy Route：应用不报错，Route 保持 hosts-only，状态列显示冲突；释放 443 后重新应用可启动。
- `[W]` 浏览器访问 `https://db.test`（hosts+Caddy 全开）：首次需信任本地 CA（helper 自动完成，观察系统证书库）；页面经 SSH Forward 到达上游。
- `[W]` 关闭最后一条 Caddy Route：Caddy 进程退出，系统信任库中的 TunnelBoard CA 被移除；再次启用时自动恢复信任且**不重复 UAC**（服务已在）。
- `[W]` Caddy 二进制被替换为其他文件：启动报完整性校验失败。

## hosts 托管与回滚

- `[A]` 区块渲染/回滚：`internal/helper/hostsfile_test.go`。
- `[W]` 手动编辑 hosts 文件的非 TunnelBoard 区块内容 → 任意应用操作后，自定义内容原样保留。
- `[W]` 写入 hosts 中途制造失败（如 hosts 文件只读）→ 报错且区块恢复为写前内容（`.tunnelboard.bak` 回滚）。
- `[W]` 非 `.test` 域名（如 `grafana.example.com`）：应用前必须出现 DNS 覆盖确认框，列出将写入的记录；取消则零副作用。

## 断线重连与生命周期

- `[A]` 终态错误不重连、退避序列：`internal/forward/*_test.go`。
- `[W]` 运行中断网/杀 SSH 进程：状态变 reconnecting 并自动恢复；超过 15 分钟未恢复变 error。
- `[W]` 认证失败（改错密码）启动：直接 error，不进入重连循环。
- `[W]` 关窗：有/无运行 Forward 都最小化到托盘，Forward 继续运行；托盘菜单退出才真正停止全部 Forward 并退出。
- `[M][L]` 托盘行为与“系统登录时启动”开关逐项验证（macOS launchd / Linux autostart 路径）。

## 备份与导入

- `[A]` 备份包格式、追加导入、完全还原：`internal/vault/backup_test.go`、`internal/biz/backup_test.go`。
- `[W]` 在有数据的机器上追加导入：所有导入内容进入新顶层文件夹；hosts/Caddy 开关全部关闭；无任何网络行为变化；手动开启 Route 后生效。
- `[W]` 导入中断（错误密码、损坏文件）：报错且 Vault 无变化。
- `[W]` 完全还原：未勾选确认无法执行；执行后 Vault 被替换、运行中 Forward 全部停止。
- `[W]` 含私钥文件的备份：导入后私钥不自动写盘，可在导入结果中逐项另存。

## 隐私与诊断

- `[A]` 遥测/商业模块不存在、HTTP 客户端白名单：`privacy_contract_test.go`。
- `[W]` 设置中关闭更新检查后：重启应用无 GitHub 请求（可用代理/抓包确认）。
- `[W]` 导出诊断包：不含密码/口令/私钥内容；用户目录路径已归一为 `~`。

## 跨平台打包

- `[M]` macOS universal DMG（无 Apple Developer 账号：ad-hoc 签名 + 首次手动放行）；hosts/CA 每次操作请求管理员授权（退化路径）。
- `[L]` Linux 打包后置（MVP 不阻塞）。
- `[W]` 升级兼容：安装新版本后 Vault 正常打开（密钥文件沿用）；`payload version` 未变无需迁移。

## 后续增强评估（计划文档迭代 5 末项）

- SSH config 导入：`internal/sshconfig` 解析器已保留并移除绕过语义，重接 UI 入口即可（绑定 + 导入模态框），工作量小。
- 每次操作单独 UAC 的 Windows 退化模式：仅当服务模式在目标机器无法安装时再做。
- macOS 常驻 Helper（一次授权）：待 macOS 真机 POC（handoff 决策）。
