//go:build darwin

package helper

import (
	"context"
	"os/user"
)

func repairDataDirIfNeeded(dir string) error {
	if !dataDirNeedsOwnerRepair(dir) {
		return nil
	}
	current, err := user.Current()
	if err != nil {
		return err
	}
	privilege, err := newNativePlatformPrivilege()
	if err != nil {
		return err
	}
	return privilege.RepairDataDirOwner(context.Background(), dir, current.Username)
}
