package model_test

import (
	"errors"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

// validGraph 返回一个满足全部不变量的数据图，各用例在此基础上做单一破坏。
func validGraph() model.VaultData {
	return model.VaultData{
		Version: 1,
		Folders: []model.Folder{
			{ID: 1, Name: "工作"},
			{ID: 2, Name: "生产", ParentID: 1},
		},
		SSHHosts: []model.SSHHost{
			{ID: 1, Name: "跳板", Host: "10.0.0.1", Port: 22, AuthType: "ssh_key"},
			{ID: 2, Name: "落地", Host: "10.0.0.2", Port: 22, AuthType: "password"},
		},
		Forwards: []model.Forward{
			{ID: 1, FolderID: 2, Name: "db", Mode: "local", ChainHostIDs: []int{1, 2},
				LocalHost: "127.0.0.1", LocalPort: 5432, RemoteHost: "db.internal", RemotePort: 5432},
			{ID: 2, FolderID: 1, Name: "socks", Mode: "dynamic", ChainHostIDs: []int{1},
				LocalHost: "127.0.0.1", LocalPort: 1080},
		},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "db.test", UpstreamScheme: "http"},
		},
		HostKeys: []model.HostKey{
			{ID: 1, Host: "10.0.0.1", Port: 22, KeyType: "ssh-ed25519", FingerprintSHA256: "SHA256:abc"},
		},
	}
}

// 同类实体 ID 必须唯一；指纹库中同一 (Host, Port) 只允许一条记录。
func TestValidateUniqueness(t *testing.T) {
	t.Run("文件夹 ID 重复", func(t *testing.T) {
		d := validGraph()
		d.Folders[1].ID = 1
		d.Folders[1].ParentID = 0
		if err := d.Validate(); !errors.Is(err, model.ErrDuplicateID) {
			t.Fatalf("err = %v, want ErrDuplicateID", err)
		}
	})
	t.Run("SSH 主机 ID 重复", func(t *testing.T) {
		d := validGraph()
		d.SSHHosts[1].ID = 1
		if err := d.Validate(); !errors.Is(err, model.ErrDuplicateID) {
			t.Fatalf("err = %v, want ErrDuplicateID", err)
		}
	})
	t.Run("Forward ID 重复", func(t *testing.T) {
		d := validGraph()
		d.Forwards[1].ID = 1
		if err := d.Validate(); !errors.Is(err, model.ErrDuplicateID) {
			t.Fatalf("err = %v, want ErrDuplicateID", err)
		}
	})
	t.Run("相同地址端口指纹重复", func(t *testing.T) {
		d := validGraph()
		d.HostKeys = append(d.HostKeys, model.HostKey{ID: 2, Host: "10.0.0.1", Port: 22,
			KeyType: "ssh-ed25519", FingerprintSHA256: "SHA256:def"})
		if err := d.Validate(); !errors.Is(err, model.ErrDuplicateHostKey) {
			t.Fatalf("err = %v, want ErrDuplicateHostKey", err)
		}
	})
}

// WebRoute 只能引用存在的、Mode 为 local 的 Forward；HTTPS 上游必须显式配置 TLS SNI。
func TestValidateWebRouteRules(t *testing.T) {
	t.Run("引用不存在的 Forward", func(t *testing.T) {
		d := validGraph()
		d.WebRoutes[0].ForwardID = 99
		if err := d.Validate(); !errors.Is(err, model.ErrRefMissing) {
			t.Fatalf("err = %v, want ErrRefMissing", err)
		}
	})
	t.Run("引用非 local 模式 Forward", func(t *testing.T) {
		d := validGraph()
		d.WebRoutes[0].ForwardID = 2 // dynamic
		if err := d.Validate(); !errors.Is(err, model.ErrRouteNeedsLocalForward) {
			t.Fatalf("err = %v, want ErrRouteNeedsLocalForward", err)
		}
	})
	t.Run("HTTPS 上游缺少 TLS SNI", func(t *testing.T) {
		d := validGraph()
		d.WebRoutes[0].UpstreamScheme = "https"
		d.WebRoutes[0].TLSSNI = ""
		if err := d.Validate(); !errors.Is(err, model.ErrRouteNeedsTLSSNI) {
			t.Fatalf("err = %v, want ErrRouteNeedsTLSSNI", err)
		}
	})
	t.Run("HTTPS 上游默认原始 Host", func(t *testing.T) {
		d := validGraph()
		d.WebRoutes[0].UpstreamScheme = "https"
		d.WebRoutes[0].TLSSNI = "backend.internal"
		if err := d.Validate(); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
	t.Run("自定义 Host 必须填写值", func(t *testing.T) {
		d := validGraph()
		d.WebRoutes[0].UpstreamScheme = "https"
		d.WebRoutes[0].TLSSNI = "backend.internal"
		d.WebRoutes[0].UpstreamHostMode = model.UpstreamHostModeCustom
		if err := d.Validate(); !errors.Is(err, model.ErrRouteNeedsUpstreamHost) {
			t.Fatalf("err = %v, want ErrRouteNeedsUpstreamHost", err)
		}
	})
	t.Run("拒绝未知 Host 模式", func(t *testing.T) {
		d := validGraph()
		d.WebRoutes[0].UpstreamScheme = "https"
		d.WebRoutes[0].TLSSNI = "backend.internal"
		d.WebRoutes[0].UpstreamHostMode = "origin-header"
		if err := d.Validate(); !errors.Is(err, model.ErrInvalidUpstreamHostMode) {
			t.Fatalf("err = %v, want ErrInvalidUpstreamHostMode", err)
		}
	})
}

// Forward 必须归属存在的文件夹、使用合法模式、引用非空且存在的 SSH 主机链。
func TestValidateForwardReferences(t *testing.T) {
	t.Run("文件夹引用不存在", func(t *testing.T) {
		d := validGraph()
		d.Forwards[0].FolderID = 99
		if err := d.Validate(); !errors.Is(err, model.ErrRefMissing) {
			t.Fatalf("err = %v, want ErrRefMissing", err)
		}
	})
	t.Run("非法转发模式", func(t *testing.T) {
		d := validGraph()
		d.Forwards[0].Mode = "bridged"
		if err := d.Validate(); !errors.Is(err, model.ErrInvalidMode) {
			t.Fatalf("err = %v, want ErrInvalidMode", err)
		}
	})
	t.Run("空主机链", func(t *testing.T) {
		d := validGraph()
		d.Forwards[0].ChainHostIDs = nil
		if err := d.Validate(); !errors.Is(err, model.ErrEmptyChain) {
			t.Fatalf("err = %v, want ErrEmptyChain", err)
		}
	})
	t.Run("链中主机不存在", func(t *testing.T) {
		d := validGraph()
		d.Forwards[0].ChainHostIDs = []int{1, 99}
		if err := d.Validate(); !errors.Is(err, model.ErrRefMissing) {
			t.Fatalf("err = %v, want ErrRefMissing", err)
		}
	})
}

// 文件夹最多两层：ParentID 必须指向顶层文件夹；父引用必须存在。
func TestValidateFolderDepthAndParent(t *testing.T) {
	t.Run("三层文件夹被拒绝", func(t *testing.T) {
		d := validGraph()
		d.Folders = append(d.Folders, model.Folder{ID: 3, Name: "超层", ParentID: 2})
		if err := d.Validate(); !errors.Is(err, model.ErrFolderDepth) {
			t.Fatalf("err = %v, want ErrFolderDepth", err)
		}
	})
	t.Run("父引用不存在被拒绝", func(t *testing.T) {
		d := validGraph()
		d.Folders[1].ParentID = 99
		if err := d.Validate(); !errors.Is(err, model.ErrRefMissing) {
			t.Fatalf("err = %v, want ErrRefMissing", err)
		}
	})
}

// 合法数据图必须通过校验。
func TestValidateAcceptsValidGraph(t *testing.T) {
	d := validGraph()
	if err := d.Validate(); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
}
