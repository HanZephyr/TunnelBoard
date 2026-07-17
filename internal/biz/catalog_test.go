package biz_test

import (
	"errors"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
)

// fakeStore 是 VaultStore 接口的内存实现，语义与 vault.Store 对齐：mutate 失败则不落盘。
type fakeStore struct {
	data model.VaultData
}

func (f *fakeStore) Load() (model.VaultData, error) { return f.data, nil }

func (f *fakeStore) Update(mutate func(*model.VaultData) error) (model.VaultData, error) {
	d := f.data
	if err := mutate(&d); err != nil {
		return model.VaultData{}, err
	}
	f.data = d
	return d, nil
}

func newCatalog() *biz.CatalogBiz {
	return biz.NewCatalogBiz(&fakeStore{data: model.VaultData{Version: 1}})
}

// DeleteSelection 的删除与引用规则：Forward 批量删除并清理其 WebRoute；
// 被引用 SSH 主机禁删（与引用者同选删除除外）；非空文件夹须显式级联。
func TestDeleteSelectionRules(t *testing.T) {
	setup := func(t *testing.T) (*biz.CatalogBiz, model.Folder, model.SSHHost) {
		t.Helper()
		c := newCatalog()
		folder, err := c.CreateFolder("工作", 0)
		if err != nil {
			t.Fatal(err)
		}
		host, err := c.SaveSSHHost(model.SSHHost{Name: "h", Host: "10.0.0.1", AuthType: "password", Password: "x"})
		if err != nil {
			t.Fatal(err)
		}
		return c, folder, host
	}
	newForward := func(t *testing.T, c *biz.CatalogBiz, folderID, hostID int, name string) model.Forward {
		t.Helper()
		fw, err := c.SaveForward(model.Forward{FolderID: folderID, Name: name, Mode: "local", ChainHostIDs: []int{hostID},
			LocalHost: "127.0.0.1", LocalPort: 5000, RemoteHost: "x", RemotePort: 5000})
		if err != nil {
			t.Fatal(err)
		}
		return fw
	}

	t.Run("批量删除 Forward", func(t *testing.T) {
		c, folder, host := setup(t)
		f1 := newForward(t, c, folder.ID, host.ID, "a")
		f2 := newForward(t, c, folder.ID, host.ID, "b")
		f3 := newForward(t, c, folder.ID, host.ID, "c")
		sel := biz.DeleteSelection{ForwardIDs: []int{f1.ID, f3.ID}}
		if err := c.DeleteSelection(sel); err != nil {
			t.Fatalf("DeleteSelection: %v", err)
		}
		data, _ := c.Data()
		if len(data.Forwards) != 1 || data.Forwards[0].ID != f2.ID {
			t.Fatalf("batch delete wrong result: %+v", data.Forwards)
		}
	})

	t.Run("被引用的 SSH 主机不可删除", func(t *testing.T) {
		c, folder, host := setup(t)
		newForward(t, c, folder.ID, host.ID, "a")
		err := c.DeleteSelection(biz.DeleteSelection{SSHHostIDs: []int{host.ID}})
		if !errors.Is(err, biz.ErrHostInUse) {
			t.Fatalf("err = %v, want ErrHostInUse", err)
		}
		data, _ := c.Data()
		if len(data.SSHHosts) != 1 {
			t.Fatal("host must survive rejected delete")
		}
	})

	t.Run("主机与引用它的 Forward 同选删除", func(t *testing.T) {
		c, folder, host := setup(t)
		fw := newForward(t, c, folder.ID, host.ID, "a")
		sel := biz.DeleteSelection{SSHHostIDs: []int{host.ID}, ForwardIDs: []int{fw.ID}}
		if err := c.DeleteSelection(sel); err != nil {
			t.Fatalf("DeleteSelection: %v", err)
		}
		data, _ := c.Data()
		if len(data.SSHHosts) != 0 || len(data.Forwards) != 0 {
			t.Fatalf("expected both gone: %+v", data)
		}
	})

	t.Run("非空文件夹拒绝直接删除", func(t *testing.T) {
		c, folder, host := setup(t)
		newForward(t, c, folder.ID, host.ID, "a")
		err := c.DeleteSelection(biz.DeleteSelection{FolderIDs: []int{folder.ID}})
		if !errors.Is(err, biz.ErrFolderNotEmpty) {
			t.Fatalf("err = %v, want ErrFolderNotEmpty", err)
		}
		data, _ := c.Data()
		if len(data.Folders) != 1 || len(data.Forwards) != 1 {
			t.Fatal("rejected delete must not persist")
		}
	})

	t.Run("级联删除清空文件夹内容", func(t *testing.T) {
		c, folder, host := setup(t)
		child, err := c.CreateFolder("子", folder.ID)
		if err != nil {
			t.Fatal(err)
		}
		newForward(t, c, folder.ID, host.ID, "a")
		newForward(t, c, child.ID, host.ID, "b")
		sel := biz.DeleteSelection{FolderIDs: []int{folder.ID}, CascadeFolders: true}
		if err := c.DeleteSelection(sel); err != nil {
			t.Fatalf("DeleteSelection: %v", err)
		}
		data, _ := c.Data()
		if len(data.Folders) != 0 || len(data.Forwards) != 0 {
			t.Fatalf("cascade should clear folder tree: %+v", data)
		}
		if len(data.SSHHosts) != 1 {
			t.Fatal("cascade must not delete unselected hosts")
		}
	})

	t.Run("空文件夹可直接删除", func(t *testing.T) {
		c, folder, _ := setup(t)
		if err := c.DeleteSelection(biz.DeleteSelection{FolderIDs: []int{folder.ID}}); err != nil {
			t.Fatalf("DeleteSelection: %v", err)
		}
		data, _ := c.Data()
		if len(data.Folders) != 0 {
			t.Fatal("empty folder should be deleted")
		}
	})
}

// MoveForward 把 Forward 移到目标文件夹；目标必须存在。
func TestMoveForward(t *testing.T) {
	c := newCatalog()
	a, _ := c.CreateFolder("A", 0)
	bFolder, _ := c.CreateFolder("B", a.ID)
	host, _ := c.SaveSSHHost(model.SSHHost{Name: "h", Host: "10.0.0.1", AuthType: "password", Password: "x"})
	fw, _ := c.SaveForward(model.Forward{FolderID: a.ID, Name: "db", Mode: "local", ChainHostIDs: []int{host.ID},
		LocalHost: "127.0.0.1", LocalPort: 5432, RemoteHost: "db", RemotePort: 5432})

	if err := c.MoveForward(fw.ID, bFolder.ID); err != nil {
		t.Fatalf("MoveForward: %v", err)
	}
	data, _ := c.Data()
	if data.Forwards[0].FolderID != bFolder.ID {
		t.Fatalf("forward not moved, folderId = %d", data.Forwards[0].FolderID)
	}
	if err := c.MoveForward(fw.ID, 99); !errors.Is(err, model.ErrRefMissing) {
		t.Fatalf("err = %v, want ErrRefMissing", err)
	}
}

// SaveForward 新建或更新 Forward：归属文件夹、模式与主机链经 Validate 校验，
// 非法数据不得落盘。
func TestSaveForwardValidatesReferences(t *testing.T) {
	c := newCatalog()
	folder, _ := c.CreateFolder("工作", 0)
	host, _ := c.SaveSSHHost(model.SSHHost{Name: "h", Host: "10.0.0.1", AuthType: "password", Password: "x"})

	base := model.Forward{FolderID: folder.ID, Name: "db", Mode: "local", ChainHostIDs: []int{host.ID},
		LocalHost: "127.0.0.1", LocalPort: 5432, RemoteHost: "db.internal", RemotePort: 5432}
	created, err := c.SaveForward(base)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("create should assign id")
	}

	bad := base
	bad.FolderID = 99
	if _, err := c.SaveForward(bad); !errors.Is(err, model.ErrRefMissing) {
		t.Fatalf("err = %v, want ErrRefMissing", err)
	}
	bad = base
	bad.ChainHostIDs = nil
	if _, err := c.SaveForward(bad); !errors.Is(err, model.ErrEmptyChain) {
		t.Fatalf("err = %v, want ErrEmptyChain", err)
	}

	data, _ := c.Data()
	if len(data.Forwards) != 1 {
		t.Fatalf("rejected forwards must not persist, got %d", len(data.Forwards))
	}

	created.Description = "主库只读"
	updated, err := c.SaveForward(created)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Description != "主库只读" {
		t.Fatalf("update lost fields: %+v", updated)
	}
}

// SaveSSHHost 新建（ID=0）或更新 SSH 主机：端口默认 22、超时默认 5000ms、
// AuthType 默认 ssh_key；非 ssh_key 清空 KeyPath，ssh_agent 清空 Password。
func TestSaveSSHHostNormalizesAndPersists(t *testing.T) {
	c := newCatalog()

	created, err := c.SaveSSHHost(model.SSHHost{Name: "跳板", Host: " 10.0.0.1 ", User: "ops",
		AuthType: "ssh_key", KeyPath: "C:/keys/id_ed25519", Password: "key-passphrase"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("create should assign id")
	}
	if created.Port != 22 || created.TimeoutMs != 5000 {
		t.Fatalf("defaults not applied: %+v", created)
	}
	if created.Host != "10.0.0.1" {
		t.Fatalf("host should be trimmed, got %q", created.Host)
	}

	created.Notes = "生产跳板"
	updated, err := c.SaveSSHHost(created)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Password != "key-passphrase" || updated.Notes != "生产跳板" {
		t.Fatalf("update lost fields: %+v", updated)
	}

	if _, err := c.SaveSSHHost(model.SSHHost{Name: "x", AuthType: "ssh_agent", Password: "should-be-cleared"}); err != nil {
		t.Fatalf("create agent host: %v", err)
	}
	data, _ := c.Data()
	for _, h := range data.SSHHosts {
		if h.AuthType == "ssh_agent" && h.Password != "" {
			t.Fatalf("agent host must not keep password: %+v", h)
		}
	}
}

// 文件夹可建到两层，第三层被拒绝且不落盘。
func TestCreateFolderRespectsTwoLevelLimit(t *testing.T) {
	c := newCatalog()

	root, err := c.CreateFolder("工作", 0)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	if root.ID == 0 {
		t.Fatal("root folder should get an assigned id")
	}
	child, err := c.CreateFolder("生产", root.ID)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := c.CreateFolder("超层", child.ID); !errors.Is(err, model.ErrFolderDepth) {
		t.Fatalf("err = %v, want ErrFolderDepth", err)
	}

	data, err := c.Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	if len(data.Folders) != 2 {
		t.Fatalf("rejected folder must not persist, got %d folders", len(data.Folders))
	}
}
