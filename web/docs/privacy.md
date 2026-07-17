# TunnelBoard 隐私说明

更新日期：2026 年 7 月 17 日

TunnelBoard 是在个人电脑本地运行的 SSH Forward 管理工具。本说明描述桌面应用当前版本实际处理的数据和联网行为。

## 1. 本地数据

当前版本把 SSH 主机、Forward、登录启动设置和界面设置保存在当前用户的系统应用数据目录：

- Windows：`%AppData%\TunnelBoard\config.toml`
- macOS：`~/Library/Application Support/TunnelBoard/config.toml`

当前 TOML 配置可能包含 SSH 密码、私钥路径和私钥口令。它不会被 TunnelBoard 上传，但在加密 Vault 完成前仍是本地明文文件，请依赖操作系统账户权限保护该目录，不要随意共享配置文件。

TunnelBoard 默认不把运行日志写入磁盘。日志只保留在本次运行的应用内存中，退出应用后即消失。

## 2. 不收集的数据

桌面应用不包含遥测、广告分析、授权激活、机器标识、使用量心跳或 AI Debug 上报。TunnelBoard 不运行自有数据收集后端，也不会出售个人信息。

## 3. 联网行为

除下列两类行为外，TunnelBoard 不会主动发起网络请求：

1. **更新检查**：启用“自动检查更新”时，请求 GitHub Releases API 获取 TunnelBoard 最新版本、发布说明和下载页面。请求使用普通 HTTPS，不携带机器 ID、SSH 配置或使用统计。关闭该开关后，不会在后台检查更新。
2. **SSH 连接**：用户创建、测试或启动 Forward 时，应用连接用户配置的 SSH 主机和目标地址。流量直接发生在用户设备与用户指定的服务器之间，TunnelBoard 不代理或检查转发内容。

发现新版本后，TunnelBoard 只展示发布说明并打开手动下载页面，不会自动下载或安装更新。

## 4. 第三方

- **GitHub**：仅用于托管源码、Release 和可选更新检查。GitHub 可能按其自身政策记录访问日志。
- **用户配置的 SSH 服务**：连接目标由用户决定，其数据处理规则由对应服务提供方决定。

TunnelBoard 不向 Google Analytics、授权服务或大语言模型提供数据。

## 5. 数据删除

用户可删除系统应用数据目录中的 TunnelBoard 文件来移除当前本地配置。删除前请先停止 Forward 并确认是否需要备份。后续加密 Vault 与备份功能上线后，本说明将同步更新。

## 6. 联系方式

隐私问题和缺陷请通过 TunnelBoard GitHub 仓库提交 Issue。

---

# TunnelBoard Privacy Notice

Last updated: July 17, 2026

TunnelBoard is a local desktop application for managing SSH forwards. This notice describes the desktop application's current data handling and network behavior.

## Local data

The current release stores SSH hosts, forwards, launch-at-login settings, and UI preferences in the current user's system application-data directory:

- Windows: `%AppData%\TunnelBoard\config.toml`
- macOS: `~/Library/Application Support/TunnelBoard/config.toml`

The TOML file may currently contain SSH passwords, private-key paths, and private-key passphrases. TunnelBoard does not upload this file, but it remains local plaintext until the encrypted Vault is delivered. Protect the directory with operating-system account permissions and do not share the file casually.

Runtime logs are not persisted by default. They remain in application memory for the current run and disappear when the app exits.

## Data we do not collect

The desktop app contains no telemetry, advertising analytics, licensing activation, machine identifier, usage heartbeat, or AI Debug reporting. TunnelBoard operates no first-party data-collection backend and does not sell personal information.

## Network behavior

TunnelBoard initiates network traffic only for:

1. **Update checks**: when automatic checks are enabled, the app requests the TunnelBoard GitHub Releases API for the latest version, release notes, and download page. The request does not include a machine ID, SSH configuration, or usage statistics. Disabling the setting stops background update checks.
2. **SSH connections**: when the user tests or starts a Forward, the app connects to user-configured SSH hosts and target addresses. Traffic flows directly between the user's device and the configured servers; TunnelBoard does not proxy or inspect forwarded content.

When an update is available, TunnelBoard only shows release information and opens a manual download page. It does not automatically download or install updates.

GitHub and user-configured SSH services process access data under their own policies. TunnelBoard sends no data to Google Analytics, licensing services, or large-language-model providers.

To remove current local data, stop active Forwards and delete the TunnelBoard system application-data directory after making any backup you need. Privacy questions and defects can be reported through the TunnelBoard GitHub repository.
