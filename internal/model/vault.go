package model

import "time"

// VaultData 是 Vault 加密 payload 的顶层结构；Version 是格式演进钩子（当前为 1）。
// 运行时状态（Status/LastError/Latency）不属于本结构，由 Forward 运行时 Module 在内存维护。
type VaultData struct {
	Version   int        `json:"version"`
	Folders   []Folder   `json:"folders"`
	SSHHosts  []SSHHost  `json:"sshHosts"`
	Forwards  []Forward  `json:"forwards"`
	WebRoutes []WebRoute `json:"webRoutes"`
	HostKeys  []HostKey  `json:"hostKeys"`
	Prefs     Prefs      `json:"prefs"`
}

// Folder 是用户自定义的分组，最多两层；ParentID 为 0 表示顶层。
type Folder struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	ParentID int    `json:"parentId"`
	Sort     int    `json:"sort"`
}

// SSHHost 是可复用的 SSH 服务器/跳板配置；Password 一字段两用：AuthType 为 password 时是
// SSH 密码，为 ssh_key 时是私钥口令；该秘密只存在于加密 Vault 内。
type SSHHost struct {
	ID                  int    `json:"id"`
	Name                string `json:"name"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	User                string `json:"user"`
	AuthType            string `json:"authType"` // password | ssh_key | ssh_agent
	KeyPath             string `json:"keyPath,omitempty"`
	AgentSocketPath     string `json:"agentSocketPath,omitempty"`
	Password            string `json:"password,omitempty"`
	KeepAliveIntervalMs int    `json:"keepAliveIntervalMs,omitempty"`
	TimeoutMs           int    `json:"timeoutMs,omitempty"`
	HostKeyAlgorithms   string `json:"hostKeyAlgorithms,omitempty"`
	Notes               string `json:"notes,omitempty"`
	// CredentialRevision 仅在持久秘密实际 replace/clear 时递增；连接池据此换代，
	// 不把秘密明文或可离线猜测的秘密哈希纳入连接身份。
	CredentialRevision uint64 `json:"credentialRevision,omitempty"`
}

// Forward 是一条独立的 SSH 端口转发规则。
type Forward struct {
	ID           int    `json:"id"`
	FolderID     int    `json:"folderId"`
	Name         string `json:"name"`
	Mode         string `json:"mode"` // local | remote | dynamic
	ChainHostIDs []int  `json:"chainHostIds"`
	LocalHost    string `json:"localHost"`
	LocalPort    int    `json:"localPort"`
	RemoteHost   string `json:"remoteHost"`
	RemotePort   int    `json:"remotePort"`
	AutoStart    bool   `json:"autoStart"`
	Description  string `json:"description,omitempty"`
}

// WebRoute 将完整域名映射到本地转发端口；仅允许引用 Mode 为 local 的 Forward。
type WebRoute struct {
	ID             int    `json:"id"`
	ForwardID      int    `json:"forwardId"`
	Domain         string `json:"domain"`
	HostsEnabled   bool   `json:"hostsEnabled"`
	CaddyEnabled   bool   `json:"caddyEnabled"`
	UpstreamScheme string `json:"upstreamScheme"` // http | https
	TLSSNI         string `json:"tlsSni,omitempty"`
	// UpstreamHost 是 HTTPS 上游请求的 HTTP Host 头。为空时兼容旧路由，回退使用 TLSSNI。
	UpstreamHost string `json:"upstreamHost,omitempty"`
}

// HostKey 是 TOFU 指纹库条目；同一 (Host, Port) 仅一条。
type HostKey struct {
	ID                int       `json:"id"`
	Host              string    `json:"host"`
	Port              int       `json:"port"`
	KeyType           string    `json:"keyType"`
	FingerprintSHA256 string    `json:"fingerprintSha256"`
	FirstSeenAt       time.Time `json:"firstSeenAt"`
	LastSeenAt        time.Time `json:"lastSeenAt"`
}

// Prefs 是应用偏好；ui.locale 伴随文件并入此处。
type Prefs struct {
	AutoRun            bool   `json:"autoRun"`
	UpdateCheckEnabled bool   `json:"updateCheckEnabled"`
	UILocale           string `json:"uiLocale,omitempty"`
	// Deprecated: 仅保留源码迁移兼容；机器本地 CA 事实不得序列化进 Vault/备份。
	CATrustedSHA256 string `json:"-"`
}
