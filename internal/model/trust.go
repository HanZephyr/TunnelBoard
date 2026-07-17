package model

// TrustStatus 是 SSH 主机指纹的信任判定结果。
type TrustStatus string

const (
	// TrustUnknown 表示该 (Host, Port) 尚无已保存指纹，首次连接必须经用户确认。
	TrustUnknown TrustStatus = "unknown"
	// TrustTrusted 表示指纹与已保存记录一致，放行。
	TrustTrusted TrustStatus = "trusted"
	// TrustMismatch 表示指纹与已保存记录不一致，必须阻断连接并提示用户核验。
	TrustMismatch TrustStatus = "mismatch"
)

// CheckHostKey 按 (Host, Port) 查找指纹库并给出信任判定；
// 命中记录时返回该条目（无论一致与否），未命中返回零值条目与 TrustUnknown。
func (d VaultData) CheckHostKey(host string, port int, fingerprintSHA256 string) (HostKey, TrustStatus) {
	for _, k := range d.HostKeys {
		if k.Host == host && k.Port == port {
			if k.FingerprintSHA256 == fingerprintSHA256 {
				return k, TrustTrusted
			}
			return k, TrustMismatch
		}
	}
	return HostKey{}, TrustUnknown
}
