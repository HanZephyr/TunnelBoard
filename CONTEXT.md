# 本地 SSH 隧道管理

本上下文描述一款个人电脑本地运行的 SSH 隧道管理工具。它将 SSH 转发、本地域名和可选本地 HTTPS 组织为可管理的访问入口。

## Language

**文件夹**：
用户自定义的最多两层分组，用于整理 Forward；不承载组织、环境或其他预设业务含义，也不继承 SSH 或启动配置。
_Avoid_: 组织, 环境

**SSH 主机**：
可复用的 SSH 服务器或跳板服务器配置，保存地址、认证和已确认的主机指纹。认证可为密码、私钥文件（含私钥口令）或 SSH Agent。Forward 可引用一个或多个 SSH 主机组成跳板链；多个 Forward 仍各自建立独立 SSH 连接。
_Avoid_: Jumper, Profile

**Forward**：
一条独立的 SSH 端口转发规则，归属一个文件夹，引用 SSH 主机链，并保存本地监听地址和远端目标地址。默认不自动启动，但可由用户逐条开启自动启动。
_Avoid_: Tunnel

**转发模式**：
Forward 支持本地转发（`-L`）、远程转发（`-R`）与动态转发（`-D`）。只有本地转发可以关联 hosts 与 Caddy Web Route。

**Web Route**：
将一个用户自定义的完整域名映射到本地转发端口的规则。可独立创建 hosts 映射；启用 Caddy 后以本地 HTTPS 入口提供访问。上游可为 HTTP 或 HTTPS；使用 HTTPS 时必须配置远端证书对应的 TLS SNI 名称，并可选择上游 `Host` 使用原始请求、继承 TLS SNI 或自定义值，默认使用原始请求 `Host`。
_Avoid_: 域名转发

**特权适配器**：
通过操作系统授权执行受托管 hosts 写入、回滚和本地根证书信任等受限操作的组件边界；Linux 使用包内的非持久化特权程序与专属 polkit action，临时授权绑定主程序进程并以五分钟为上限，主程序始终以普通权限运行。
_Avoid_: 始终管理员运行的主程序, 通用 root 命令执行器

**本地 CA**：
由内置 Caddy 以 `tls internal` 使用的本地证书机构。它为所有 Web Route 的本地 HTTPS 入口签发证书，不申请公网证书。
_Avoid_: 公网 ACME 证书

**Linux 系统级本地 CA 信任**：
Linux 正式支持平台将本地 CA 写入系统信任库，以保证主流客户端可验证 Web Route 证书；该信任影响同机其他用户，且仅在用户确认指纹并完成系统授权后建立。
_Avoid_: 通用用户级根证书库, 自动修改浏览器 Profile

**正式支持的 Linux 平台**：
以 Debian 12+、Ubuntu 24.04 LTS+ 与 RHEL 兼容系 9+ 桌面发行版为正式交付对象，并分别以原生 `.deb` 与 `.rpm` 包发布；每条发行版线均支持 `amd64` 与 `arm64`，不支持 32 位 ARM。
_Avoid_: 将 `x86_64` 与 `amd64` 视为不同架构, 已停止维护的 CentOS Linux, 未经验证的全 Linux 发行版承诺, 首发 AppImage

**Linux 图形桌面会话**：
具有已登录普通用户、图形显示与可用系统授权代理的 Linux 会话；它是 Linux 正式支持运行 TunnelBoard 的必要环境，不包含 SSH 登录或无头服务器。
_Avoid_: 无头守护进程, 脱离用户会话的常驻隧道服务

**无托盘关窗确认**：
在没有可用系统托盘的 Linux 图形桌面会话中，每次关闭主窗口都由用户选择退出应用或隐藏窗口并继续在后台运行。
_Avoid_: 静默退出, 无法恢复的后台运行

**Linux 登录自启动**：
由用户设置的 XDG Autostart 用户级启动行为；有托盘时静默启动，无托盘时显示主窗口，不创建 systemd 服务。
_Avoid_: 系统级开机启动, 无界面的隐藏启动

**Linux 签名发布资产**：
GitHub Release 中可由用户手动下载并验证的 `.deb`、`.rpm`、manifest 与校验资产；它们由 TunnelBoard 专用 GPG 密钥签名，主密钥离线保存，CI 只使用签名子密钥；不依赖首发 APT/DNF 软件源或应用内自更新。
_Avoid_: 未验证的安装包, 首发软件源运维, 应用内包管理器调用

**Linux 正式浏览器覆盖**：
在正式支持发行版中以原生包安装的 Firefox 与 Chromium；Snap、Flatpak 等沙箱浏览器不保证自动继承系统级本地 CA 信任。
_Avoid_: 修改浏览器 Profile, 将沙箱浏览器视为已验证覆盖

**Linux 普通卸载清理**：
卸载包时自动撤销只属于 TunnelBoard 的系统级副作用，包括受托管 hosts 区块和唯一的本地 CA；当前用户的 Vault、密钥、备份和日志默认保留。
_Avoid_: 遗留系统级信任, 普通卸载静默删除用户凭据

**Linux 正式桌面验收**：
GNOME 与 KDE Plasma 的 Wayland 图形会话；X11 仅验证基础启动和退出兼容性，不承诺完整托盘体验。
_Avoid_: 单一桌面环境代表所有 Linux 桌面, X11 完整托盘承诺

**Linux 发布门禁**：
所有四个 Linux 原生包通过自动化构建和完整性校验后，仍须在正式桌面验收环境获得权限、CA、浏览器、托盘、自启、升级与卸载证据；缺少该证据的候选只能保持 draft。
_Avoid_: 交叉编译成功即正式发布, 无真机证据的 Linux 正式支持

**Vault**：
保存文件夹、Forward、Web Route 和认证秘密的本地加密数据集。
_Avoid_: 配置文件, 数据库

**备份包**：
由用户指定密码独立加密、可跨设备导入的 Vault 导出物；不携带任何设备本地密钥。默认包含 Vault 中的密码和私钥口令，但不包含外部私钥文件本体；用户可明确选择将私钥文件纳入。
_Avoid_: 配置导出

**SSH 主机指纹**：
SSH 服务器身份公钥的指纹。首次连接必须经用户确认后保存；已保存指纹发生变化时阻断连接并提示用户核验。
_Avoid_: 可忽略的连接告警

## 首版范围

- Forward 保持任意 TCP 的 SSH 转发能力。
- 文件夹最多两层，仅用于整理 Forward，不继承任何连接或运行配置。
- 保留 SSH 本地（`-L`）、远程（`-R`）和动态（`-D`）三种转发模式；hosts 与 Caddy 仅作用于本地转发。
- 不引入 Profile 等固定中间层；每条 Forward 独立配置，文件夹是唯一的用户自定义分组机制。
- 保留可复用 SSH 主机资料库与多跳链；同一 SSH 主机首跳的 Forward 复用同一 SSH 连接（连接池协调单飞拨号/重连与引用计数，临时主机不入池）。
- SSH 主机支持密码、私钥文件和 SSH Agent；密码与私钥口令保存在 Vault，Agent 模式不存私钥材料。
- 已启动的 Forward 默认自动重连并采用最长 1 分钟间隔的指数退避；认证失败、主机指纹变化或用户手动停止时不再重连。
- 在具有系统托盘的平台，关闭主窗口始终隐藏到托盘并保持运行，与是否存在运行中的 Forward 无关；只有显式退出才会真正退出，退出时停止全部 Forward 与 Caddy。Linux 无托盘环境每次关窗均由用户选择退出或隐藏到后台。
- Caddy 仅为 HTTP(S) 上游的本地转发提供可选的本地域名与 HTTPS 入口；其他协议直接使用其本地监听地址和端口。
- 本地监听端口在启动与连接测试时必须由实际绑定结果兜底；编辑时应尽早显示冲突预警。
- SSH 主机指纹校验默认开启：首次显式信任，变化即阻断；不提供默认绕过。
- Web Route 的 hosts 映射和 Caddy 代理为可选功能；Caddy 生效的前提是 hosts 启用（开 Caddy 联动开 hosts，关 hosts 联动关 Caddy）；仅映射 hosts 时使用“域名 + 端口”访问。
- 首次使用特权功能时安装受限辅助服务；主程序不以管理员权限常驻运行。
- Web Route 允许用户配置完整域名；Caddy 对所有本地入口强制使用本地 CA，不申请公网证书。
- Web Route 支持 HTTP 和 HTTPS 上游；HTTPS 上游由用户显式指定 TLS SNI 名称并严格校验证书。
- HTTPS 上游的 `Host` 默认透传浏览器访问本地域名，可选继承 TLS SNI 或填写自定义 `Host`；TLS SNI 与 HTTP `Host` 独立配置。
- 对非 `.test`、`.localhost` 的域名，受托管 hosts 写入前必须明确提示其仅在本机覆盖正常 DNS 解析，并展示将写入的记录。
- Caddy 必须独占本机 443；启用 Web Route 前预检端口冲突，冲突时不启动 Caddy 并保留“域名 + Forward 端口”的访问方式。首版不支持 Caddy 自定义监听端口。
- 当最后一个启用 Caddy 的 Web Route 被关闭或删除时，自动从系统信任库撤销本地 CA；再次启用时由受限辅助服务恢复信任，无需重复 UAC。
- 不允许直接删除非空文件夹：用户可将其内容移动到父文件夹后删除，或在显示数量并二次确认后连同内容删除。
- hosts 记录仅在用户明确移除它或删除对应 Forward 时撤销；停止 Forward 不改变 hosts 或已配置的 Caddy Route。
- Forward 默认不自动启动，用户可按条目开启自动启动；Web Route 不会因访问而自动建立 SSH 连接。
- 备份包默认不复制外部私钥文件；用户可显式选择包含私钥文件，并在导出时获得风险提示。
- 不提供旧版 Loris Tunnel 配置的自动检测或导入迁移；新产品从独立 Vault 开始。
- 备份默认追加导入到新的顶层文件夹，冲突项由用户改名或跳过；完全还原替换 Vault 必须二次确认。
- 运行日志持久化到数据目录 `logs/`（滚动截断），同时保留内存日志供手动导出脱敏诊断包；helper 服务日志写入 ProgramData 日志文件与 Windows 事件日志。
- 导入备份不自动启动 Forward、不写入 hosts、不启动或重载 Caddy；用户必须在导入结果中明确应用这些副作用。
- 被 Forward 引用的 SSH 主机不可删除，必须先改绑或移除引用；Forward 列表支持多选批量删除。
- 移除遥测、授权和 AI Debug 等非必要联网能力；仅保留自动更新检查，并允许用户在设置中关闭。
- Caddy 以随应用安装包内置的固定版本交付并进行完整性校验；仅在用户启用 Web Route 时运行，不在首次使用时下载。
- Linux 正式支持 Debian 12+、Ubuntu 24.04 LTS+ 与 RHEL 兼容系 9+ 桌面发行版，并分别交付 `.deb` 与 `.rpm` 原生包；每条发行版线均交付 `amd64` 与 `arm64`，不交付 32 位 ARM、已停止维护的 CentOS Linux 或首发 AppImage。
- Linux 的本地 CA 使用系统级信任库；写入前必须展示完整指纹并获得用户确认及系统授权，不自动修改各浏览器的用户 Profile。
- Linux 正式支持仅限 Linux 图形桌面会话；不支持无头服务器，也不增加脱离用户会话运行的 systemd 系统服务。
- Linux 的特权适配器使用包内非持久化特权程序和专属 polkit action，只允许受托管 hosts 与唯一 TunnelBoard 本地 CA 的高层操作，不允许任意 root 命令。
- Linux 的临时特权授权在主程序正常退出时按授权 ID 撤销；异常退出时不得被新进程复用，并以五分钟有效期兜底。
- Linux 登录自启动只使用 XDG Autostart；有托盘时静默启动，无托盘时显示主窗口，不使用 systemd 服务。
- Linux 发布资产包含 `.deb`、`.rpm`、manifest、`SHA256SUMS` 与 GPG 签名；首发仅通过 GitHub Release 手动分发和升级，不提供 APT/DNF 软件源或应用内自更新。
- Linux 发布使用 TunnelBoard 专用 GPG 签名主密钥；主密钥离线保存，GitHub Actions 仅使用受限签名子密钥，发布页固定公布公钥与指纹。
- Linux Web Route 的无证书告警验收覆盖原生安装的 Firefox 与 Chromium；Snap、Flatpak 等沙箱浏览器仅尽力兼容，不自动修改其 Profile。
- Linux `.deb/.rpm` 普通卸载会停止 TunnelBoard 并删除其受托管 hosts 区块和唯一系统级本地 CA；Vault、密钥、备份和日志默认保留，彻底清除必须由用户明确执行。
- Linux 正式 UI 验收覆盖 GNOME 与 KDE Plasma 的 Wayland 会话；X11 仅做基础启动和退出 smoke，不承诺完整托盘体验。
- Linux 正式发布前，Debian 系/RHEL 系与 `amd64`/`arm64` 的四个原生包均须通过自动化构建和完整性校验，并在 GNOME/KDE Wayland 真机或图形 VM 完成权限、CA、浏览器、托盘、自启、升级与卸载验收；否则候选只能保持 draft。
