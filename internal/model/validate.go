package model

import (
	"errors"
	"fmt"
)

// 引用完整性与结构不变量的哨兵错误，供业务层用 errors.Is 判定并映射为用户可读信息。
var (
	ErrRefMissing  = errors.New("model: reference to missing entity")
	ErrFolderDepth = errors.New("model: folder exceeds two levels")
	ErrInvalidMode = errors.New("model: unsupported forward mode")
	ErrEmptyChain  = errors.New("model: forward chain is empty")

	ErrRouteNeedsLocalForward = errors.New("model: web route requires a local-mode forward")
	ErrRouteNeedsTLSSNI       = errors.New("model: https upstream requires explicit TLS SNI")

	ErrDuplicateID      = errors.New("model: duplicate entity id")
	ErrDuplicateHostKey = errors.New("model: duplicate host key for address and port")
)

// 合法转发模式：本地（-L）、远程（-R）、动态（-D）。
const (
	ModeLocal   = "local"
	ModeRemote  = "remote"
	ModeDynamic = "dynamic"
)

// Validate 校验 Vault 数据图的引用完整性与结构不变量，供写入路径在落盘前调用。
func (d VaultData) Validate() error {
	if err := validateUniqueIDs(d); err != nil {
		return err
	}
	topFolders := make(map[int]bool, len(d.Folders))
	for _, f := range d.Folders {
		if f.ParentID == 0 {
			topFolders[f.ID] = true
		}
	}
	for _, f := range d.Folders {
		if f.ParentID == 0 {
			continue
		}
		if !topFolders[f.ParentID] {
			if folderExists(d.Folders, f.ParentID) {
				return fmt.Errorf("%w: folder %q (%d)", ErrFolderDepth, f.Name, f.ID)
			}
			return fmt.Errorf("%w: folder %q (%d) parent %d", ErrRefMissing, f.Name, f.ID, f.ParentID)
		}
	}

	hostIDs := make(map[int]bool, len(d.SSHHosts))
	for _, h := range d.SSHHosts {
		hostIDs[h.ID] = true
	}
	for _, fw := range d.Forwards {
		switch fw.Mode {
		case ModeLocal, ModeRemote, ModeDynamic:
		default:
			return fmt.Errorf("%w: forward %q (%d) mode %q", ErrInvalidMode, fw.Name, fw.ID, fw.Mode)
		}
		if !folderExists(d.Folders, fw.FolderID) {
			return fmt.Errorf("%w: forward %q (%d) folder %d", ErrRefMissing, fw.Name, fw.ID, fw.FolderID)
		}
		if len(fw.ChainHostIDs) == 0 {
			return fmt.Errorf("%w: forward %q (%d)", ErrEmptyChain, fw.Name, fw.ID)
		}
		for _, hid := range fw.ChainHostIDs {
			if !hostIDs[hid] {
				return fmt.Errorf("%w: forward %q (%d) ssh host %d", ErrRefMissing, fw.Name, fw.ID, hid)
			}
		}
	}

	forwardModes := make(map[int]string, len(d.Forwards))
	for _, fw := range d.Forwards {
		forwardModes[fw.ID] = fw.Mode
	}
	for _, r := range d.WebRoutes {
		mode, ok := forwardModes[r.ForwardID]
		if !ok {
			return fmt.Errorf("%w: web route %q (%d) forward %d", ErrRefMissing, r.Domain, r.ID, r.ForwardID)
		}
		if mode != ModeLocal {
			return fmt.Errorf("%w: web route %q (%d)", ErrRouteNeedsLocalForward, r.Domain, r.ID)
		}
		if r.UpstreamScheme == "https" && r.TLSSNI == "" {
			return fmt.Errorf("%w: web route %q (%d)", ErrRouteNeedsTLSSNI, r.Domain, r.ID)
		}
	}

	type hostPort struct {
		host string
		port int
	}
	seenKeys := make(map[hostPort]bool, len(d.HostKeys))
	for _, k := range d.HostKeys {
		hp := hostPort{k.Host, k.Port}
		if seenKeys[hp] {
			return fmt.Errorf("%w: %s:%d", ErrDuplicateHostKey, k.Host, k.Port)
		}
		seenKeys[hp] = true
	}
	return nil
}

// validateUniqueIDs 断言同类实体 ID 唯一；必须在引用检查之前运行。
func validateUniqueIDs(d VaultData) error {
	check := func(kind string, ids []int) error {
		seen := make(map[int]bool, len(ids))
		for _, id := range ids {
			if seen[id] {
				return fmt.Errorf("%w: %s id %d", ErrDuplicateID, kind, id)
			}
			seen[id] = true
		}
		return nil
	}
	folderIDs := make([]int, 0, len(d.Folders))
	for _, f := range d.Folders {
		folderIDs = append(folderIDs, f.ID)
	}
	hostIDs := make([]int, 0, len(d.SSHHosts))
	for _, h := range d.SSHHosts {
		hostIDs = append(hostIDs, h.ID)
	}
	forwardIDs := make([]int, 0, len(d.Forwards))
	for _, fw := range d.Forwards {
		forwardIDs = append(forwardIDs, fw.ID)
	}
	routeIDs := make([]int, 0, len(d.WebRoutes))
	for _, r := range d.WebRoutes {
		routeIDs = append(routeIDs, r.ID)
	}
	for _, c := range []struct {
		kind string
		ids  []int
	}{
		{"folder", folderIDs},
		{"ssh host", hostIDs},
		{"forward", forwardIDs},
		{"web route", routeIDs},
	} {
		if err := check(c.kind, c.ids); err != nil {
			return err
		}
	}
	return nil
}

func folderExists(folders []Folder, id int) bool {
	for _, f := range folders {
		if f.ID == id {
			return true
		}
	}
	return false
}
