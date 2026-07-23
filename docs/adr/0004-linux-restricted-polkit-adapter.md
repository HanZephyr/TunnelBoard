# Linux 使用受限 polkit 特权适配器

Linux 的系统级 hosts 与本地 CA 操作由安装包内的非持久化特权程序执行，并以 TunnelBoard 专属 polkit action 请求系统授权。该程序只接受受托管 hosts 更新、安装或撤销唯一 TunnelBoard 本地 CA、刷新系统信任库等高层操作；不安装 systemd 服务、不复用 Windows 会话 Helper、也不执行 UI 或调用方传入的任意 root 命令。临时授权绑定主程序进程，最长五分钟；正常退出按授权 ID 精确撤销，异常退出时授权不得被新进程复用。这样保留 Linux 桌面原生授权体验，同时把系统级权限面限制在产品已确认的副作用内。
