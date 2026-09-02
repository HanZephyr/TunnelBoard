package helper

import (
	"errors"
	"os"
	"path/filepath"
)

// RepairDataDirIfNeeded 在当前用户读不了 vault.dat（常见于曾经 sudo 启动）时，
// 弹出一次管理员授权把数据目录属主改回当前用户。不是 Darwin 或无需修复时为空操作。
func RepairDataDirIfNeeded(dir string) error {
	return repairDataDirIfNeeded(dir)
}

func dataDirNeedsOwnerRepair(dir string) bool {
	_, err := os.ReadFile(filepath.Join(dir, "vault.dat"))
	return errors.Is(err, os.ErrPermission)
}
