# TunnelBoard

<div align="center">

[English](README.md)

**快速稳定的 SSH 隧道管理工具**

**在 macOS 和 Windows 上轻松管理 SSH 隧道，自动重连、集中整理，日常使用更省心。**

![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows-blue)
![License](https://img.shields.io/badge/license-Apache%202.0-green)
![Built with Wails](https://img.shields.io/badge/built%20with-Wails-informational)

</div>

---

## 项目简介

**TunnelBoard** 是一款跨平台桌面应用，用图形界面帮你创建、管理和监控 SSH 隧道。它把常用的 SSH 端口转发能力整理成易用的桌面界面，并内置自动重连机制，网络波动时也能尽量保持连接稳定。

如果你经常需要访问远程服务器、数据库，或防火墙后的内网服务，TunnelBoard 可以把这些隧道集中管理起来，减少反复敲命令和手动排查连接状态的麻烦。

![总览](screenshots/screenshot-overview.png)

---

## 功能特性

- 🖥️ **图形化隧道管理** — 通过简洁的桌面 UI 创建、编辑、启动、停止和监控所有 SSH 隧道，日常使用无需命令行
- 🔄 **深度优化的自动重连机制** — 采用智能指数退避算法，确保隧道在网络波动或 SSH 断连后能够自动静默恢复，重连状态实时可见
- ⚡ **实时延迟监测与最优线路选择** — 持续探测并展示各线路 SSH 延迟，支持按响应时间排序并一键切换到最快线路
- 📥 **从 SSH 命令批量导入** — 粘贴任意 `ssh` 命令，自动解析所有 `-L`/`-R`/`-D` 参数，一次性创建多条隧道
- 📋 **一键复制隧道** — 克隆现有隧道快速创建变体，无需重复填写所有字段
- ⛓️ **多跳跳板机链** — 为单条隧道配置多个 SSH 跳板机，支持深层嵌套网络（如 堡垒机 → 内网主机）
- 🔀 **本地、远程与动态（SOCKS5）端口转发** — 支持所有标准 SSH 隧道模式
- ✅ **内置连接测试** — 在创建/编辑对话框中直接测试隧道是否可达，保存前即可验证
- ▶️ **启动时自动开启** — 将隧道标记为自动启动，应用打开后立即连接
- 🌍 **跨平台** — 支持 macOS 和 Windows
- 💬 **多语言界面** — 支持英文和简体中文

---

## 截图

**从 SSH 命令导入隧道：**

![从 SSH 命令导入](screenshots/screenshot-create-tunnels-from-ssh-command.png)

**查看 SSH 连接延迟：**

![SSH 延迟](screenshots/screenshot-show-ssh-latency.png)

---

## 快速开始

### 下载安装

前往 [Releases](../../releases) 页面下载最新版本。

- **macOS**：`.dmg` 安装包
- **Windows**：`.exe` 安装包

### macOS 安全提示处理

首次运行 macOS 版本时，系统可能会提示无法打开应用。可以通过下面两种方式放行。

**方法一：系统设置放行**

1. 打开应用并看到安全提示后，点击“取消”
2. 打开“系统设置” -> “隐私与安全性”
3. 在安全性区域找到 TunnelBoard，点击“仍要打开”

**方法二：通过终端解除限制**

打开终端（Terminal），执行下面的命令。系统可能会要求输入电脑密码。

```bash
sudo xattr -rd com.apple.quarantine /Applications/tunnelboard.app
```

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

# 构建生产版本
wails build
```

---

## 配置文件

TunnelBoard 当前使用 TOML 文件保存配置，统一放在操作系统的当前用户应用数据目录：

- **Windows：** `%AppData%\TunnelBoard\config.toml`
- **macOS：** `~/Library/Application Support/TunnelBoard/config.toml`

如果目标配置文件不存在或内容为空，TunnelBoard 会在首次运行时自动创建默认配置。

配置示例：

```toml
[[jumpers]]
name = "my-server"
host = "example.com"
port = 22
user = "ubuntu"
auth_method = "agent"      # 或 "key" / "password"
# identity = "~/.ssh/id_rsa"

[[tunnels]]
name = "本地数据库"
jumper = "my-server"
mode = "local"             # local | remote | socks5
local_port = 5432
remote_host = "127.0.0.1"
remote_port = 5432
```

---

## 隧道模式说明

| 模式 | 说明 |
|------|------|
| `local` | 通过 SSH 服务器把本地端口转发到远程地址 |
| `remote` | 把 SSH 服务器上的远程端口转发到本地地址 |
| `socks5` | 将 SSH 服务器作为 SOCKS5 代理使用，也就是动态转发 |

---

## 技术栈

| 层 | 技术 |
|----|------|
| 桌面框架 | [Wails](https://wails.io/) v2 |
| 后端 | Go |
| 前端 | Vue 3 + Vite |
| SSH | Go `golang.org/x/crypto/ssh` |

---

## 社区交流

如需反馈问题或提出功能建议，请使用 [GitHub Issues](https://github.com/HanZephyr/TunnelBoard/issues)。

---

## 开源协议

Apache License 2.0。
