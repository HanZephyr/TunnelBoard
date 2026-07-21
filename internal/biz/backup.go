package biz

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

// ErrRestoreNotConfirmed 表示完全还原未经显式二次确认（CONTEXT.md:71）。
var ErrRestoreNotConfirmed = errors.New("biz: full restore requires explicit confirmation")

// BackupBiz 是备份 Module：密码加密备份、导入预览与冲突处理、追加导入与完全还原。
// 所有导入路径都不产生系统副作用（不启动 Forward、不写 hosts、不动 Caddy、不写私钥文件）。
type BackupBiz struct {
	store    VaultStore
	readFile func(path string) ([]byte, error)
}

// NewBackupBiz 组装备份 Module；readFile 默认 os.ReadFile（测试可注入）。
func NewBackupBiz(store VaultStore) *BackupBiz {
	return &BackupBiz{store: store, readFile: os.ReadFile}
}

// SetReadFile 替换私钥文件读取接缝（测试用）。
func (b *BackupBiz) SetReadFile(f func(path string) ([]byte, error)) {
	b.readFile = f
}

// CreateBackup 导出密码加密备份包。includeKeyFiles 为 true 时打包各 SSH 主机引用的
// 私钥文件本体；warnings 报告被打包的文件与读取失败的文件（导出前供用户知悉风险）。
func (b *BackupBiz) CreateBackup(password string, includeKeyFiles bool) ([]byte, []string, error) {
	data, err := b.store.Load()
	if err != nil {
		return nil, nil, err
	}
	var keyFiles map[string][]byte
	var warnings []string
	if includeKeyFiles {
		keyFiles = map[string][]byte{}
		for _, h := range data.SSHHosts {
			if h.KeyPath == "" {
				continue
			}
			content, err := b.readFile(h.KeyPath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("无法读取私钥文件 %s：%v", h.KeyPath, err))
				continue
			}
			keyFiles[h.KeyPath] = content
			warnings = append(warnings, fmt.Sprintf("备份将包含私钥文件 %s 的内容", h.KeyPath))
		}
	}
	raw, err := vault.ExportBackup(data, keyFiles, password, vault.DefaultBackupKDF())
	if err != nil {
		return nil, nil, err
	}
	slog.Info("backup created", "include_key_files", includeKeyFiles, "key_files", len(keyFiles), "warnings", len(warnings))
	return raw, warnings, nil
}

// HostConflict 是导入主机与现有主机的冲突项（同 endpoint：host+port+user）。
type HostConflict struct {
	Imported   ImportSSHHostView `json:"imported"`
	ExistingID int               `json:"existingId"`
	Reason     string            `json:"reason"`
}

type ImportSSHHostView struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	AuthType  string `json:"authType"`
	HasSecret bool   `json:"hasSecret"`
}

// ImportPreview 是导入前预览：实体计数、建议的新顶层文件夹名、冲突清单与私钥文件清单。
type ImportPreview struct {
	Counts        map[string]int `json:"counts"`
	FolderName    string         `json:"folderName"`
	HostConflicts []HostConflict `json:"hostConflicts"`
	KeyFiles      []string       `json:"keyFiles"`
}

// PreviewImport 解密备份包并对照当前 Vault 生成预览；不写入任何数据。
func (b *BackupBiz) PreviewImport(raw []byte, password string) (ImportPreview, error) {
	imported, keyFiles, err := vault.ParseBackup(raw, password)
	if err != nil {
		return ImportPreview{}, err
	}
	staged := StagedBackup{Vault: imported, KeyFiles: keyFiles}
	defer staged.Destroy()
	return b.PreviewStagedImport(staged)
}

// PreviewStagedImport 只消费已由 BackupPackage 有界解密的数据，不再读取路径或口令。
func (b *BackupBiz) PreviewStagedImport(staged StagedBackup) (ImportPreview, error) {
	imported, keyFiles := staged.Vault, staged.KeyFiles
	current, err := b.store.Load()
	if err != nil {
		return ImportPreview{}, err
	}

	preview := ImportPreview{
		Counts: map[string]int{
			"folders": len(imported.Folders), "sshHosts": len(imported.SSHHosts),
			"forwards": len(imported.Forwards), "webRoutes": len(imported.WebRoutes),
			"hostKeys": len(imported.HostKeys),
		},
		FolderName: "导入备份 " + time.Now().Format("2006-01-02 15:04"),
	}
	for path := range keyFiles {
		preview.KeyFiles = append(preview.KeyFiles, path)
	}
	for _, ih := range imported.SSHHosts {
		for _, eh := range current.SSHHosts {
			if sameEndpoint(ih, eh) {
				preview.HostConflicts = append(preview.HostConflicts, HostConflict{
					Imported: importSSHHostView(ih), ExistingID: eh.ID, Reason: "相同地址/端口/用户的主机已存在",
				})
				break
			}
		}
	}
	return preview, nil
}

// HostResolution 是单个冲突主机的处理决定：rename（加后缀导入）或 skip。
type HostResolution struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	User   string `json:"user"`
	Action string `json:"action"` // rename | skip
}

// ImportPlan 是用户对预览的确认结果。
type ImportPlan struct {
	FolderName      string           `json:"folderName"`
	HostResolutions []HostResolution `json:"hostResolutions"`
}

// ImportSummary 报告导入结果；KeyFiles 为备份包内的私钥文件（由 UI 引导用户另存，不自动写盘）。
type ImportSummary struct {
	Imported          map[string]int    `json:"imported"`
	SkippedHosts      int               `json:"skippedHosts"`
	FlattenedFolders  int               `json:"flattenedFolders"`
	RoutesDeactivated int               `json:"routesDeactivated"`
	KeyFiles          map[string][]byte `json:"-"`
	KeyFilePaths      []string          `json:"keyFilePaths"`
}

// ApplyImport 追加导入：全部内容包装到新顶层文件夹；ID 全部重映射并修正引用；
// Route 的 hosts/Caddy 开关强制关闭（用户显式应用后才恢复，CONTEXT.md:72）；
// 两层深度上限使导入的第二层文件夹被压平（Forward 并入其父文件夹）。
func (b *BackupBiz) ApplyImport(raw []byte, password string, plan ImportPlan) (ImportSummary, error) {
	imported, keyFiles, err := vault.ParseBackup(raw, password)
	if err != nil {
		return ImportSummary{}, err
	}
	return b.ApplyStagedImport(StagedBackup{Vault: imported, KeyFiles: keyFiles}, plan)
}

// ApplyStagedImport 在单次 Vault Update 中消费 staged 数据；调用方负责 token 生命周期。
func (b *BackupBiz) ApplyStagedImport(staged StagedBackup, plan ImportPlan) (ImportSummary, error) {
	imported, keyFiles := staged.Vault, staged.KeyFiles
	folderName := strings.TrimSpace(plan.FolderName)
	if folderName == "" {
		return ImportSummary{}, fmt.Errorf("folder name is required")
	}

	skip := map[string]bool{}
	rename := map[string]bool{}
	for _, r := range plan.HostResolutions {
		switch r.Action {
		case "skip":
			skip[endpointKey(r.Host, r.Port, r.User)] = true
		case "rename":
			rename[endpointKey(r.Host, r.Port, r.User)] = true
		}
	}

	summary := ImportSummary{Imported: map[string]int{}, KeyFiles: keyFiles}
	for path := range keyFiles {
		summary.KeyFilePaths = append(summary.KeyFilePaths, path)
	}

	_, err := b.store.Update(func(d *model.VaultData) error {
		// 每类 ID 只扫描一次既有数据，随后单调分配；导入复杂度与实体数线性相关。
		folderIDs := newImportIDSequence(len(d.Folders), func(i int) int { return d.Folders[i].ID })
		hostIDs := newImportIDSequence(len(d.SSHHosts), func(i int) int { return d.SSHHosts[i].ID })
		forwardIDs := newImportIDSequence(len(d.Forwards), func(i int) int { return d.Forwards[i].ID })
		routeIDs := newImportIDSequence(len(d.WebRoutes), func(i int) int { return d.WebRoutes[i].ID })
		hostKeyIDs := newImportIDSequence(len(d.HostKeys), func(i int) int { return d.HostKeys[i].ID })

		wrapper := model.Folder{ID: folderIDs.Next(), Name: folderName}
		d.Folders = append(d.Folders, wrapper)

		// 文件夹：顶层挂到 wrapper 下；原第二层压平（其 Forward 并入父文件夹）。
		folderNewID := map[int]int{} // 原顶层 ID → 新 ID
		flattenInto := map[int]int{} // 原第二层 ID → 其父的原 ID（稍后换为新 ID）
		for _, f := range imported.Folders {
			if f.ParentID == 0 {
				nf := f
				nf.ID = folderIDs.Next()
				nf.ParentID = wrapper.ID
				folderNewID[f.ID] = nf.ID
				d.Folders = append(d.Folders, nf)
				summary.Imported["folders"]++
			} else {
				flattenInto[f.ID] = f.ParentID
				summary.FlattenedFolders++
			}
		}
		flattenFolderNewID := map[int]int{}
		for origID, origParent := range flattenInto {
			flattenFolderNewID[origID] = folderNewID[origParent]
		}

		// SSH 主机：冲突且 skip 的不导入；与现有主机同 endpoint 的改名导入（默认或显式 rename）。
		existingEndpoints := map[string]bool{}
		for _, h := range d.SSHHosts {
			existingEndpoints[endpointKey(h.Host, h.Port, h.User)] = true
		}
		hostNewID := map[int]int{}
		for _, h := range imported.SSHHosts {
			key := endpointKey(h.Host, h.Port, h.User)
			if skip[key] {
				summary.SkippedHosts++
				continue
			}
			nh := h
			nh.ID = hostIDs.Next()
			if existingEndpoints[key] || rename[key] {
				nh.Name = h.Name + "-imported"
			}
			hostNewID[h.ID] = nh.ID
			d.SSHHosts = append(d.SSHHosts, nh)
			summary.Imported["sshHosts"]++
		}

		// Forward：引用重映射；被压平文件夹的 Forward 并入其父；跳过主机的链剔除该跳，
		// 链因此为空或引用未知主机时该 Forward 不导入。
		forwardNewID := map[int]int{}
		for _, fw := range imported.Forwards {
			nf := fw
			nf.ID = forwardIDs.Next()
			if mapped, ok := folderNewID[fw.FolderID]; ok {
				nf.FolderID = mapped
			} else if mapped, ok := flattenFolderNewID[fw.FolderID]; ok {
				nf.FolderID = mapped
			} else {
				continue
			}
			chain := make([]int, 0, len(fw.ChainHostIDs))
			broken := false
			for _, hid := range fw.ChainHostIDs {
				if mapped, ok := hostNewID[hid]; ok {
					chain = append(chain, mapped)
				} else if skippedHost(imported.SSHHosts, hid, skip) {
					continue
				} else {
					broken = true
					break
				}
			}
			if broken || len(chain) == 0 {
				continue
			}
			nf.ChainHostIDs = chain
			forwardNewID[fw.ID] = nf.ID
			d.Forwards = append(d.Forwards, nf)
			summary.Imported["forwards"]++
		}

		// Web Route：开关强制关闭；引用重映射。
		for _, r := range imported.WebRoutes {
			fwID, ok := forwardNewID[r.ForwardID]
			if !ok {
				continue
			}
			nr := r
			nr.ID = routeIDs.Next()
			nr.ForwardID = fwID
			if nr.HostsEnabled || nr.CaddyEnabled {
				nr.HostsEnabled = false
				nr.CaddyEnabled = false
				summary.RoutesDeactivated++
			}
			d.WebRoutes = append(d.WebRoutes, nr)
			summary.Imported["webRoutes"]++
		}

		// 指纹库：端点已存在则跳过（不覆盖既有信任）。
		for _, k := range imported.HostKeys {
			if hasHostKey(d.HostKeys, k.Host, k.Port) {
				continue
			}
			nk := k
			nk.ID = hostKeyIDs.Next()
			d.HostKeys = append(d.HostKeys, nk)
			summary.Imported["hostKeys"]++
		}

		return d.Validate()
	})
	if err != nil {
		return ImportSummary{}, err
	}
	slog.Info("backup import applied",
		"folder", folderName,
		"skipped_hosts", summary.SkippedHosts,
		"flattened_folders", summary.FlattenedFolders,
		"routes_deactivated", summary.RoutesDeactivated,
		"imported", summary.Imported)
	return summary, nil
}

func importSSHHostView(host model.SSHHost) ImportSSHHostView {
	return ImportSSHHostView{Name: host.Name, Host: host.Host, Port: host.Port, User: host.User, AuthType: host.AuthType, HasSecret: host.Password != ""}
}

type importIDSequence struct{ next int }

func newImportIDSequence(size int, idAt func(int) int) *importIDSequence {
	maxID := 0
	for i := 0; i < size; i++ {
		if id := idAt(i); id > maxID {
			maxID = id
		}
	}
	return &importIDSequence{next: maxID + 1}
}

func (s *importIDSequence) Next() int {
	id := s.next
	s.next++
	return id
}

// RestoreBackup 完全还原：用备份内容整体替换 Vault（含偏好），必须显式二次确认。
// 调用方负责先停止全部 Forward（网络行为不变直到用户显式应用）。
// Deprecated: application wiring must migrate to RestoreCoordinator StageRestore/CommitRestore;
// this compatibility method does not coordinate Runtime, Route effects, journal or quarantine.
func (b *BackupBiz) RestoreBackup(raw []byte, password string, confirmed bool) error {
	if !confirmed {
		return ErrRestoreNotConfirmed
	}
	imported, _, err := vault.ParseBackup(raw, password)
	if err != nil {
		return err
	}
	if err := imported.Validate(); err != nil {
		return fmt.Errorf("backup content invalid: %w", err)
	}
	_, err = b.store.Update(func(d *model.VaultData) error {
		*d = imported
		return nil
	})
	if err == nil {
		slog.Warn("vault fully restored from backup")
	}
	return err
}

func endpointKey(host string, port int, user string) string {
	return strings.ToLower(strings.TrimSpace(host)) + "|" + fmt.Sprint(port) + "|" + strings.TrimSpace(user)
}

func sameEndpoint(a, b model.SSHHost) bool {
	return endpointKey(a.Host, a.Port, a.User) == endpointKey(b.Host, b.Port, b.User)
}

func skippedHost(hosts []model.SSHHost, id int, skip map[string]bool) bool {
	for _, h := range hosts {
		if h.ID == id {
			return skip[endpointKey(h.Host, h.Port, h.User)]
		}
	}
	return false
}

func hasHostKey(keys []model.HostKey, host string, port int) bool {
	for _, k := range keys {
		if k.Host == host && k.Port == port {
			return true
		}
	}
	return false
}
