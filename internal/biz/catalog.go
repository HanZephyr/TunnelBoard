package biz

import (
	"errors"
	"fmt"
	"strings"

	"github.com/HanZephyr/TunnelBoard/internal/forward"
	"github.com/HanZephyr/TunnelBoard/internal/model"
)

// VaultStore 是目录与配置 Module 依赖的唯一存储接口，由 internal/vault 的 Store 实现。
// Update 语义：mutate 返回错误则不落盘。
type VaultStore interface {
	Load() (model.VaultData, error)
	Update(mutate func(*model.VaultData) error) (model.VaultData, error)
}

// CatalogBiz 维护文件夹、SSH 主机、Forward、Web Route 之间的引用完整性，
// 是计划文档中的目录与配置 Module。
type CatalogBiz struct {
	store VaultStore
}

type SecretAction string

const (
	SecretKeep    SecretAction = "keep"
	SecretReplace SecretAction = "replace"
	SecretClear   SecretAction = "clear"
)

type SaveSSHHostRequest struct {
	Host         model.SSHHost
	SecretAction SecretAction
	SecretInput  string
}

const MaxMoveForwardsBatch = 5000

type MoveForwardsReport struct {
	ChangedIDs   []int
	UnchangedIDs []int
}

func NewCatalogBiz(store VaultStore) *CatalogBiz {
	return &CatalogBiz{store: store}
}

// Data 返回当前 Vault 数据。
func (b *CatalogBiz) Data() (model.VaultData, error) {
	return b.store.Load()
}

// CreateFolder 在 parentID（0 表示顶层）下新建文件夹；最多两层由 Validate 兜底。
func (b *CatalogBiz) CreateFolder(name string, parentID int) (model.Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Folder{}, fmt.Errorf("folder name is required")
	}
	var created model.Folder
	_, err := b.store.Update(func(d *model.VaultData) error {
		created = model.Folder{
			ID:       nextID(len(d.Folders), func(i int) int { return d.Folders[i].ID }),
			Name:     name,
			ParentID: parentID,
			Sort:     len(d.Folders),
		}
		d.Folders = append(d.Folders, created)
		return d.Validate()
	})
	if err != nil {
		return model.Folder{}, err
	}
	return created, nil
}

// SaveSSHHost 新建（ID 为 0）或更新 SSH 主机，写入前做规范化与校验。
func (b *CatalogBiz) SaveSSHHost(host model.SSHHost) (model.SSHHost, error) {
	host = normalizeSSHHost(host)
	var saved model.SSHHost
	_, err := b.store.Update(func(d *model.VaultData) error {
		if host.ID == 0 {
			host.ID = nextID(len(d.SSHHosts), func(i int) int { return d.SSHHosts[i].ID })
			d.SSHHosts = append(d.SSHHosts, host)
		} else {
			idx := indexSSHHost(d.SSHHosts, host.ID)
			if idx < 0 {
				return fmt.Errorf("ssh host %d not found", host.ID)
			}
			d.SSHHosts[idx] = host
		}
		if err := d.Validate(); err != nil {
			return err
		}
		saved = host
		return nil
	})
	if err != nil {
		return model.SSHHost{}, err
	}
	return saved, nil
}

// SaveSSHHostSecure 是 WebView 写入主机的唯一领域入口：已保存秘密只从 Vault
// 合并，绝不相信请求 Host.Password；实际 replace/clear 才递增 CredentialRevision。
func (b *CatalogBiz) SaveSSHHostSecure(request SaveSSHHostRequest) (model.SSHHost, bool, error) {
	var saved model.SSHHost
	connectionChanged := false
	_, err := b.store.Update(func(d *model.VaultData) error {
		host, previous, changed, err := prepareSSHHostSecure(*d, request)
		if err != nil {
			return err
		}
		connectionChanged = changed
		if previous == nil {
			host.ID = nextID(len(d.SSHHosts), func(i int) int { return d.SSHHosts[i].ID })
			d.SSHHosts = append(d.SSHHosts, host)
		} else {
			d.SSHHosts[indexSSHHost(d.SSHHosts, host.ID)] = host
		}
		if err := d.Validate(); err != nil {
			return err
		}
		saved = host
		return nil
	})
	if err != nil {
		return model.SSHHost{}, false, err
	}
	return saved, connectionChanged, nil
}

func (b *CatalogBiz) PreviewSSHHostSecure(request SaveSSHHostRequest) (model.SSHHost, bool, error) {
	data, err := b.store.Load()
	if err != nil {
		return model.SSHHost{}, false, err
	}
	host, _, changed, err := prepareSSHHostSecure(data, request)
	return host, changed, err
}

func prepareSSHHostSecure(data model.VaultData, request SaveSSHHostRequest) (model.SSHHost, *model.SSHHost, bool, error) {
	host := normalizeSSHHost(request.Host)
	host.Password = ""
	var previous *model.SSHHost
	if host.ID != 0 {
		idx := indexSSHHost(data.SSHHosts, host.ID)
		if idx < 0 {
			return model.SSHHost{}, nil, false, fmt.Errorf("ssh host %d not found", host.ID)
		}
		copy := data.SSHHosts[idx]
		previous = &copy
		host.CredentialRevision = copy.CredentialRevision
	}
	oldSecret := ""
	if previous != nil {
		oldSecret = previous.Password
		if request.SecretAction == SecretKeep && previous.AuthType != host.AuthType {
			return model.SSHHost{}, nil, false, errors.New("secret action must be explicit when authentication type changes")
		}
	}
	switch host.AuthType {
	case "ssh_agent":
		host.Password = ""
	case "password", "ssh_key":
		switch request.SecretAction {
		case SecretKeep:
			if previous == nil {
				return model.SSHHost{}, nil, false, errors.New("new ssh host cannot keep a missing secret")
			}
			host.Password = oldSecret
		case SecretReplace:
			if request.SecretInput == "" {
				return model.SSHHost{}, nil, false, errors.New("replacement secret is required")
			}
			host.Password = request.SecretInput
		case SecretClear:
			host.Password = ""
		default:
			return model.SSHHost{}, nil, false, fmt.Errorf("unknown secret action %q", request.SecretAction)
		}
		if host.AuthType == "password" && host.Password == "" {
			return model.SSHHost{}, nil, false, errors.New("password authentication requires a secret")
		}
	default:
		return model.SSHHost{}, nil, false, fmt.Errorf("unsupported auth type %q", host.AuthType)
	}
	if host.Password != oldSecret {
		host.CredentialRevision++
	}
	changed := previous != nil && forward.SSHConnectionIdentity(*previous) != forward.SSHConnectionIdentity(host)
	return host, previous, changed, nil
}

// normalizeSSHHost 规范化 SSH 主机资料：默认端口 22、默认超时 5000ms、
// 默认认证 ssh_key；非 ssh_key 清空 KeyPath，ssh_agent 不保存任何秘密材料。
func normalizeSSHHost(h model.SSHHost) model.SSHHost {
	h.Name = strings.TrimSpace(h.Name)
	h.Host = strings.TrimSpace(h.Host)
	h.User = strings.TrimSpace(h.User)
	if h.Port == 0 {
		h.Port = 22
	}
	if h.TimeoutMs == 0 {
		h.TimeoutMs = 5000
	}
	if h.AuthType == "" {
		h.AuthType = "ssh_key"
	}
	if h.AuthType != "ssh_key" {
		h.KeyPath = ""
	}
	if h.AuthType == "ssh_agent" {
		h.Password = ""
	}
	return h
}

func indexSSHHost(hosts []model.SSHHost, id int) int {
	for i, h := range hosts {
		if h.ID == id {
			return i
		}
	}
	return -1
}

// SaveWebRoute 新建（ID 为 0）或更新 Web Route；引用与模式规则由 Validate 兜底。
// 硬性不变量：Caddy 生效的前提是 hosts 启用——hosts 关闭时强制 Caddy 关闭
// （Caddy 依赖域名解析到回环，缺了 hosts 映射非本地域名还会漏到真实公网 IP）。
// 反向联动（开 Caddy 时顺带开 hosts）是交互层便利，由前端在保存前表达，后端不做。
func (b *CatalogBiz) SaveWebRoute(r model.WebRoute) (model.WebRoute, error) {
	r.Domain = strings.TrimSpace(strings.ToLower(r.Domain))
	r.TLSSNI = strings.TrimSpace(r.TLSSNI)
	if r.UpstreamScheme == "" {
		r.UpstreamScheme = "http"
	}
	if !r.HostsEnabled {
		r.CaddyEnabled = false
	}
	var saved model.WebRoute
	_, err := b.store.Update(func(d *model.VaultData) error {
		if r.ID == 0 {
			r.ID = nextID(len(d.WebRoutes), func(i int) int { return d.WebRoutes[i].ID })
			d.WebRoutes = append(d.WebRoutes, r)
		} else {
			idx := indexWebRoute(d.WebRoutes, r.ID)
			if idx < 0 {
				return fmt.Errorf("web route %d not found", r.ID)
			}
			d.WebRoutes[idx] = r
		}
		if err := d.Validate(); err != nil {
			return err
		}
		saved = r
		return nil
	})
	if err != nil {
		return model.WebRoute{}, err
	}
	return saved, nil
}

// DeleteWebRoute 删除指定 Web Route；删除 Forward 时的级联清理由 DeleteSelection 负责。
func (b *CatalogBiz) DeleteWebRoute(id int) error {
	_, err := b.store.Update(func(d *model.VaultData) error {
		idx := indexWebRoute(d.WebRoutes, id)
		if idx < 0 {
			return fmt.Errorf("web route %d not found", id)
		}
		d.WebRoutes = append(d.WebRoutes[:idx], d.WebRoutes[idx+1:]...)
		return d.Validate()
	})
	return err
}

func indexWebRoute(routes []model.WebRoute, id int) int {
	for i, r := range routes {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// SaveForward 新建（ID 为 0）或更新 Forward，引用完整性由 Validate 兜底。
func (b *CatalogBiz) SaveForward(fw model.Forward) (model.Forward, error) {
	fw.Name = strings.TrimSpace(fw.Name)
	var saved model.Forward
	_, err := b.store.Update(func(d *model.VaultData) error {
		if fw.ID == 0 {
			fw.ID = nextID(len(d.Forwards), func(i int) int { return d.Forwards[i].ID })
			d.Forwards = append(d.Forwards, fw)
		} else {
			idx := indexForward(d.Forwards, fw.ID)
			if idx < 0 {
				return fmt.Errorf("forward %d not found", fw.ID)
			}
			d.Forwards[idx] = fw
		}
		if err := d.Validate(); err != nil {
			return err
		}
		saved = fw
		return nil
	})
	if err != nil {
		return model.Forward{}, err
	}
	return saved, nil
}

func indexForward(forwards []model.Forward, id int) int {
	for i, fw := range forwards {
		if fw.ID == id {
			return i
		}
	}
	return -1
}

// MoveForward 把指定 Forward 移到目标文件夹（0 无意义：Forward 必须归属文件夹）。
func (b *CatalogBiz) MoveForward(forwardID, targetFolderID int) error {
	_, err := b.MoveForwards([]int{forwardID}, targetFolderID)
	return err
}

// MoveForwards 在一次 Vault Update 中移动全部 Forward。任何 ID 或目标文件夹
// 无效都会使 mutate 返回错误，因此不会出现部分移动。
func (b *CatalogBiz) MoveForwards(forwardIDs []int, targetFolderID int) (MoveForwardsReport, error) {
	if len(forwardIDs) == 0 {
		return MoveForwardsReport{}, fmt.Errorf("at least one forward is required")
	}
	if len(forwardIDs) > MaxMoveForwardsBatch {
		return MoveForwardsReport{}, fmt.Errorf("at most %d forwards may be moved", MaxMoveForwardsBatch)
	}
	seen := make(map[int]struct{}, len(forwardIDs))
	for _, id := range forwardIDs {
		if _, exists := seen[id]; exists {
			return MoveForwardsReport{}, fmt.Errorf("forward %d is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	report := MoveForwardsReport{
		ChangedIDs:   make([]int, 0, len(forwardIDs)),
		UnchangedIDs: make([]int, 0, len(forwardIDs)),
	}
	var noChange = errors.New("move forwards: no change")
	_, err := b.store.Update(func(d *model.VaultData) error {
		if indexFolder(d.Folders, targetFolderID) < 0 {
			return fmt.Errorf("%w: folder %d", model.ErrRefMissing, targetFolderID)
		}
		indexes := make([]int, 0, len(forwardIDs))
		for _, id := range forwardIDs {
			idx := indexForward(d.Forwards, id)
			if idx < 0 {
				return fmt.Errorf("forward %d not found", id)
			}
			if d.Forwards[idx].FolderID == targetFolderID {
				report.UnchangedIDs = append(report.UnchangedIDs, id)
			} else {
				report.ChangedIDs = append(report.ChangedIDs, id)
				indexes = append(indexes, idx)
			}
		}
		if len(indexes) == 0 {
			return noChange
		}
		for _, idx := range indexes {
			d.Forwards[idx].FolderID = targetFolderID
		}
		return d.Validate()
	})
	if errors.Is(err, noChange) {
		return report, nil
	}
	if err != nil {
		return MoveForwardsReport{}, err
	}
	return report, nil
}

// ResolveChain 按 fw.ChainHostIDs 顺序从 Vault 解析 SSH 主机链；缺 ID 以
// model.ErrRefMissing 包装报错，供运行时 Module 在拨号前取得完整主机配置。
func (b *CatalogBiz) ResolveChain(fw model.Forward) ([]model.SSHHost, error) {
	data, err := b.store.Load()
	if err != nil {
		return nil, err
	}
	byID := make(map[int]model.SSHHost, len(data.SSHHosts))
	for _, h := range data.SSHHosts {
		byID[h.ID] = h
	}
	hosts := make([]model.SSHHost, 0, len(fw.ChainHostIDs))
	for _, id := range fw.ChainHostIDs {
		h, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%w: forward %q (%d) ssh host %d", model.ErrRefMissing, fw.Name, fw.ID, id)
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

// 删除规则的哨兵错误，供应用层用 errors.Is 映射为确认流程或提示。
var (
	ErrHostInUse      = errors.New("biz: ssh host is referenced by forwards")
	ErrFolderNotEmpty = errors.New("biz: folder is not empty")
)

// DeleteSelection 是一次批量删除选择；CascadeFolders 表示用户已二次确认级联删除非空文件夹。
type DeleteSelection struct {
	FolderIDs      []int
	SSHHostIDs     []int
	ForwardIDs     []int
	CascadeFolders bool
}

// DeleteSelection 应用一次批量删除：
// Forward 直接删除并清理其 WebRoute；被剩余 Forward 引用的 SSH 主机拒绝删除；
// 非空文件夹（含子文件夹或 Forward）必须 CascadeFolders 才连同内容删除。
func (b *CatalogBiz) DeleteSelection(sel DeleteSelection) error {
	_, err := b.store.Update(func(d *model.VaultData) error {
		deletedFolders := map[int]bool{}
		for _, id := range sel.FolderIDs {
			if indexFolder(d.Folders, id) < 0 {
				return fmt.Errorf("folder %d not found", id)
			}
			deletedFolders[id] = true
		}
		// 展开子文件夹（最多两层，一遍即可覆盖）。
		for _, f := range d.Folders {
			if f.ParentID != 0 && deletedFolders[f.ParentID] && !deletedFolders[f.ID] {
				if !sel.CascadeFolders {
					return fmt.Errorf("%w: folder %d contains subfolder %d", ErrFolderNotEmpty, f.ParentID, f.ID)
				}
				deletedFolders[f.ID] = true
			}
		}
		deletedForwards := map[int]bool{}
		for _, id := range sel.ForwardIDs {
			deletedForwards[id] = true
		}
		for _, fw := range d.Forwards {
			if deletedFolders[fw.FolderID] && !deletedForwards[fw.ID] {
				if !sel.CascadeFolders {
					return fmt.Errorf("%w: folder %d contains forward %d", ErrFolderNotEmpty, fw.FolderID, fw.ID)
				}
				deletedForwards[fw.ID] = true
			}
		}
		// 主机引用按删除后的剩余 Forward 计算。
		remainingRefs := map[int]bool{}
		for _, fw := range d.Forwards {
			if deletedForwards[fw.ID] {
				continue
			}
			for _, hid := range fw.ChainHostIDs {
				remainingRefs[hid] = true
			}
		}
		for _, hid := range sel.SSHHostIDs {
			if remainingRefs[hid] {
				return fmt.Errorf("%w: ssh host %d", ErrHostInUse, hid)
			}
		}

		d.Folders = filterFolders(d.Folders, deletedFolders)
		d.Forwards = filterForwards(d.Forwards, deletedForwards)
		d.SSHHosts = filterSSHHosts(d.SSHHosts, sel.SSHHostIDs)
		d.WebRoutes = filterWebRoutes(d.WebRoutes, deletedForwards)
		return d.Validate()
	})
	return err
}

func indexFolder(folders []model.Folder, id int) int {
	for i, f := range folders {
		if f.ID == id {
			return i
		}
	}
	return -1
}

func filterFolders(items []model.Folder, deleted map[int]bool) []model.Folder {
	out := items[:0]
	for _, f := range items {
		if !deleted[f.ID] {
			out = append(out, f)
		}
	}
	return out
}

func filterForwards(items []model.Forward, deleted map[int]bool) []model.Forward {
	out := items[:0]
	for _, fw := range items {
		if !deleted[fw.ID] {
			out = append(out, fw)
		}
	}
	return out
}

func filterSSHHosts(items []model.SSHHost, ids []int) []model.SSHHost {
	deleted := map[int]bool{}
	for _, id := range ids {
		deleted[id] = true
	}
	out := items[:0]
	for _, h := range items {
		if !deleted[h.ID] {
			out = append(out, h)
		}
	}
	return out
}

func filterWebRoutes(items []model.WebRoute, deletedForwards map[int]bool) []model.WebRoute {
	out := items[:0]
	for _, r := range items {
		if !deletedForwards[r.ForwardID] {
			out = append(out, r)
		}
	}
	return out
}

// nextID 返回 max+1；items 为空时为 1。size/accessor 形式避免反射。
func nextID(size int, idAt func(i int) int) int {
	max := 0
	for i := 0; i < size; i++ {
		if id := idAt(i); id > max {
			max = id
		}
	}
	return max + 1
}
