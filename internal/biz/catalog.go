package biz

import (
	"errors"
	"fmt"
	"strings"

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
	_, err := b.store.Update(func(d *model.VaultData) error {
		idx := indexForward(d.Forwards, forwardID)
		if idx < 0 {
			return fmt.Errorf("forward %d not found", forwardID)
		}
		d.Forwards[idx].FolderID = targetFolderID
		return d.Validate()
	})
	return err
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
