# TunnelBoard

<div align="center">

[English](README.md)

**快速稳定的 SSH 隧道管理工具**

**在 macOS 和 Windows 上轻松管理 SSH 隧道，自动重连、集中整理、本地域名与可选的本地 HTTPS 入口，日常使用更省心。**

![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows-blue)
![License](https://img.shields.io/badge/license-Apache%202.0-green)
![Built with Wails](https://img.shields.io/badge/built%20with-Wails-informational)

</div>

---

## 项目简介

**TunnelBoard** 基于 Loris Tunnel 演进，是一款在个人电脑本地运行的 SSH Forward 管理工具。它在保留 SSH 本地、远程和动态转发能力的基础上，重新设计了数据模型、运行时、备份恢复和系统权限边界。

除了访问数据库、远程服务和内网端口，TunnelBoard 还可以把本地 Forward 组织为易记的域名入口，并通过内置 Caddy 提供可选的本地 HTTPS，减少重复维护 SSH 命令、hosts 和反向代理配置的成本。

如果你经常需要访问远程服务器、数据库，或防火墙后的内网服务，TunnelBoard 可以把这些隧道集中管理起来，减少反复敲命令和手动排查连接状态的麻烦。同时还能通过本地的域名解析和 HTTPS 反代来实现便捷管理、方便记忆。

![总览](screenshots/screenshot-overview.png)

---

## 功能特性

- 🖥️ **图形化隧道管理** — 通过简洁的桌面 UI 创建、编辑、启动、停止和监控所有 SSH 隧道，日常使用无需命令行
- ⛓️ **多跳跳板机链** — 为单条隧道配置多个 SSH 跳板机，支持深层嵌套网络（如 堡垒机 → 内网主机）
- 🗂️ **文件夹与独立资源模型** — 使用最多两层文件夹整理 Forward；SSH 主机、Forward 和 Web Route 分开管理并保持引用完整性
- 🔐 **可复用 SSH 主机** — 支持密码、私钥文件与 SSH Agent 认证，可配置多跳主机链，并复用符合相同连接身份的首跳连接
- 🔀 **完整 SSH 转发模式** — 支持本地转发（`-L`）、远程转发（`-R`）和动态转发（`-D`/SOCKS5）
- 🔄 **可靠的运行时与自动重连** — 支持批量启停、按条目自动启动、端口冲突预检和指数退避重连；认证失败、主机指纹变化或手动停止时不会盲目重试
- 🌐 **Web Route 与本地域名** — 可把完整域名映射到本地 Forward，按需写入受托管 hosts 记录；不启用 Caddy 时仍可使用“域名 + Forward 端口”访问
- 🔒 **内置 Caddy 本地 HTTPS** — 为 Web Route 提供 `tls internal` HTTPS 入口；支持 HTTP/HTTPS 上游，HTTPS 上游必须显式配置 TLS SNI 并严格校验证书
- 🧰 **加密 Vault** — 文件夹、SSH 主机、Forward、Web Route、认证秘密和应用偏好统一保存在设备本地加密 Vault 中
- 📦 **加密备份与安全恢复** — 使用独立密码导出可迁移备份，支持追加导入与完全还原；可选择是否把外部私钥文件包含进备份包
- 🛡️ **SSH 主机身份校验** — 首次连接展示 SHA-256 指纹并要求确认，已保存的主机密钥发生变化时阻断连接
- 📊 **状态与诊断** — 提供运行概览、Forward/Route 明确状态、应用与 Caddy 日志，以及不包含认证秘密的脱敏诊断包
- 🖥️ **桌面与托盘体验** — 关闭主窗口后继续在系统托盘运行，只有显式退出才停止 Forward 与 Caddy；支持登录时启动
- 🌍 **多语言界面** — 提供简体中文、繁体中文、英文和俄文界面
- ▶️ **启动时自动开启** — 将隧道标记为自动启动，应用打开后立即连接
- 🌍 **跨平台** — 支持 macOS 和 Windows
- ⬆️ **可控的更新检查** — 仅通过 GitHub Releases 检查更新，用户可以关闭自动检查；偏好读取失败时默认不联网

---

## 相比原项目的主要改动

TunnelBoard 不是只更换名称和界面的简单 Fork。当前版本围绕本地访问入口、安全存储和可恢复运行时进行了较大范围重构。

| 方面 | loris-tunnel-app（原项目） | TunnelBoard |
| --- | --- | --- |
| 管理模型 | 以跳板机和 Tunnel 列表为主 | 拆分为文件夹、SSH 主机、Forward 与 Web Route，支持引用校验和批量操作 |
| 本地访问入口 | 主要提供 SSH 端口转发 | 新增受托管 hosts、本地域名、内置 Caddy 与本地 HTTPS |
| 数据存储 | TOML 配置文件 | 设备本地密钥保护的加密 Vault，秘密不以明文配置保存 |
| 备份恢复 | 配置文件级导入导出 | 独立密码加密的备份包、追加导入、预览确认、完全还原与恢复隔离 |
| SSH 信任 | 侧重连接可用性 | 首次确认主机指纹，指纹变化默认阻断，并区分可重试与安全错误，避免中间人攻击 |
| 运行时 | 单条隧道启停与重连 | 增加连接池、generation 隔离、有界停止、批量操作和更明确的运行状态 |
| 系统权限 | 没有完整的受限特权边界 | 普通权限主程序配合会话级 Helper，只开放 hosts 与本地 CA 等白名单操作 |
| 联网与隐私 | 包含授权、遥测、Analytics 和 AI Debug 等模块 | 移除非必要联网能力，仅保留可关闭的 GitHub 更新检查和用户主动建立的 SSH 连接 |
| 运维能力 | 基础连接状态 | 增加日志轮转、脱敏诊断、Route 状态、端口冲突提示和事务恢复状态 |

由于数据模型和安全存储已经变化，TunnelBoard **不会自动读取或迁移旧版 Loris Tunnel 配置**。首次使用会创建独立 Vault；如需迁移，应人工核对后在 TunnelBoard 中重新建立配置。

---

## 安全与隐私加固

### 本地秘密与 WebView 隔离

- Vault 使用首次初始化生成的设备本地随机密钥加密，备份包使用用户提供的独立密码加密，两者不共享密钥边界
- 已保存的密码和私钥口令不会返回前端 WebView；界面只获得“是否已保存秘密”的状态，新秘密通过一次性提交边界写入
- 备份包、实体数量、字符串长度、私钥与日志读取均设置资源预算，降低恶意或损坏输入造成资源耗尽的风险

### SSH 与本地 HTTPS 信任

- SSH 主机首次连接必须确认指纹，指纹变化会阻断连接，避免把身份异常当作普通断线自动重试
- Caddy 的本地 CA 仅加入当前 Windows 用户的信任库，不写入整台机器的根证书信任
- 本地入口固定使用 Caddy 内部 CA，不向公网 ACME 申请证书；HTTPS 上游必须提供 TLS SNI，并保持严格证书校验
- 正式构建锁定内置 Caddy 的版本与摘要；Caddy 控制面使用应用私有端点并校验自有进程，避免暴露固定管理端口

### 最小权限的系统修改

- 主程序保持普通用户权限，仅在首次需要特权操作时通过 UAC 启动本次应用会话使用的受限 Helper，退出应用后关闭
- Helper 不接受任意命令、脚本或文件路径，只能操作 TunnelBoard 管理的 hosts 区块和指定的本地 CA
- hosts 更新采用受托管标记区块、写前备份与失败回滚；覆盖非 `.test`/`.localhost` 域名前会展示实际记录并要求确认

### 事务、恢复与真实状态

- Route 的 Vault 目标、hosts、Caddy、CA 和实际状态通过 revision 与持久化 journal 协调，失败时保留可恢复线索
- 完全还原采用“校验预览 → 替换数据 → 网络隔离 → 用户确认激活”的流程；导入备份不会自动启动 Forward、修改 hosts 或启动 Caddy
- 配置读取或状态刷新失败时，界面保留上次成功读取的事实并标记为 stale/error，不把未知状态伪装成“已停止”或空数据
- 日志采用有界读取、滚动保存和统一脱敏；诊断包不包含密码、私钥口令等认证秘密

### 联网边界

除用户主动建立的 SSH 连接、打开下载页面和 GitHub Releases 更新检查外，TunnelBoard 不需要授权验证、遥测、Analytics 或 AI Debug 网络请求。关闭自动更新检查后，应用不会在后台执行该请求。

---

## 截图

以下截图来自原项目，仅保留作为界面演进参考。TunnelBoard 新界面截图将在正式发布前更新。

**原项目的 SSH 命令导入界面：**

![从 SSH 命令导入](screenshots/screenshot-create-tunnels-from-ssh-command.png)

**原项目的 SSH 延迟界面：**

![SSH 延迟](screenshots/screenshot-show-ssh-latency.png)

---

## 快速开始

### 下载安装

正式版本发布后，可前往 [Releases](../../releases) 页面下载经过发布流程验证的安装包。

当前本地开发构建不等同于正式发布产物。Windows 正式候选包还必须通过 Authenticode 签名、产物清单校验和真机 smoke；macOS 与 Linux 也需完成各自的签名、权限与真机门禁。

### 从源码构建

**前置依赖：**

- [Go](https://golang.org/dl/) ≥ 1.21
- [Node.js](https://nodejs.org/) ≥ 18 + [pnpm](https://pnpm.io/)
- [Wails CLI](https://wails.io/docs/gettingstarted/installation) v2

```bash
git clone https://github.com/HanZephyr/TunnelBoard.git
cd TunnelBoard

# 安装前端依赖
cd frontend && pnpm install && cd ..

# 开发模式运行
wails dev

# 构建本地开发版本
wails build
```

---

## 数据目录

TunnelBoard 不再使用明文 `config.toml`。应用会在当前用户的系统配置目录下创建 `TunnelBoard` 数据目录，其中主要包含加密数据 `vault.dat`、设备本地密钥 `vault.key`、运行日志和恢复状态。

常见默认位置：

- **Windows：** `%AppData%\TunnelBoard`
- **macOS：** `~/Library/Application Support/TunnelBoard`
- **Linux：** `$XDG_CONFIG_HOME/TunnelBoard`，未设置时通常为 `~/.config/TunnelBoard`

设置页会显示当前实际使用的数据目录。请把 `vault.dat` 与 `vault.key` 视为同一设备上的完整本地数据边界；跨设备迁移应使用应用生成的密码加密备份包，而不是直接复制其中单个文件。

---

## 转发模式说明

| 模式 | SSH 参数 | 说明 |
| --- | --- | --- |
| 本地转发 | `-L` | 通过 SSH 主机把本地监听地址和端口转发到远端目标 |
| 远程转发 | `-R` | 在 SSH 服务器侧监听端口，并把流量转发到本地目标 |
| 动态转发 | `-D` | 在本地提供 SOCKS5 代理 |

只有本地转发可以关联 Web Route。远程转发和动态转发继续通过各自的监听地址与端口使用。

---

## 技术栈

| 层 | 技术 |
| --- | --- |
| 桌面框架 | [Wails](https://wails.io/) v2 |
| 后端 | Go |
| 前端 | Vue 3 + Vite |
| SSH | Go `golang.org/x/crypto/ssh` |
| 本地 HTTPS | 内置 Caddy |
| 本地数据 | 加密 Vault |

---

## 社区交流

如需反馈问题或提出功能建议，请使用 [GitHub Issues](https://github.com/HanZephyr/TunnelBoard/issues)。

---

## 开源协议

Apache License 2.0。
