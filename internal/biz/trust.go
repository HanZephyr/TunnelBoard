package biz

import (
	"fmt"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

// HostKeyStatus 返回指定端点指纹的信任判定（只读，不产生副作用）。
func (b *CatalogBiz) HostKeyStatus(host string, port int, fingerprintSHA256 string) (model.HostKey, model.TrustStatus, error) {
	data, err := b.store.Load()
	if err != nil {
		return model.HostKey{}, model.TrustUnknown, err
	}
	entry, status := data.CheckHostKey(host, port, fingerprintSHA256)
	return entry, status, nil
}

// EnrollHostKey 在用户首次确认后保存端点指纹；端点已有记录时拒绝（须走 ReplaceHostKey）。
func (b *CatalogBiz) EnrollHostKey(host string, port int, keyType, fingerprintSHA256 string) (model.HostKey, error) {
	var enrolled model.HostKey
	_, err := b.store.Update(func(d *model.VaultData) error {
		if _, status := d.CheckHostKey(host, port, fingerprintSHA256); status != model.TrustUnknown {
			return fmt.Errorf("%w: %s:%d already enrolled", model.ErrDuplicateHostKey, host, port)
		}
		now := time.Now()
		enrolled = model.HostKey{
			ID:                nextID(len(d.HostKeys), func(i int) int { return d.HostKeys[i].ID }),
			Host:              host,
			Port:              port,
			KeyType:           keyType,
			FingerprintSHA256: fingerprintSHA256,
			FirstSeenAt:       now,
			LastSeenAt:        now,
		}
		d.HostKeys = append(d.HostKeys, enrolled)
		return d.Validate()
	})
	if err != nil {
		return model.HostKey{}, err
	}
	return enrolled, nil
}

// ReplaceHostKey 在用户显式确认变更后替换端点指纹；保留首次信任时间。
// 端点尚无记录时等同于 EnrollHostKey（同一确认对话框覆盖两种场景）。
func (b *CatalogBiz) ReplaceHostKey(host string, port int, keyType, fingerprintSHA256 string) (model.HostKey, error) {
	var replaced model.HostKey
	_, err := b.store.Update(func(d *model.VaultData) error {
		now := time.Now()
		for i, k := range d.HostKeys {
			if k.Host == host && k.Port == port {
				d.HostKeys[i].KeyType = keyType
				d.HostKeys[i].FingerprintSHA256 = fingerprintSHA256
				d.HostKeys[i].LastSeenAt = now
				replaced = d.HostKeys[i]
				return d.Validate()
			}
		}
		replaced = model.HostKey{
			ID:                nextID(len(d.HostKeys), func(i int) int { return d.HostKeys[i].ID }),
			Host:              host,
			Port:              port,
			KeyType:           keyType,
			FingerprintSHA256: fingerprintSHA256,
			FirstSeenAt:       now,
			LastSeenAt:        now,
		}
		d.HostKeys = append(d.HostKeys, replaced)
		return d.Validate()
	})
	if err != nil {
		return model.HostKey{}, err
	}
	return replaced, nil
}
