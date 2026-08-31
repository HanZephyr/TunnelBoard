//go:build windows

package application

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func restrictPrivateKeyFile(path string) error {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("application: resolve current user for private key ACL: %w", err)
	}
	sddl := "D:P(A;;FA;;;SY)(A;;FA;;;" + tokenUser.User.Sid.String() + ")"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("application: create private key ACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("application: read private key ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("application: apply private key ACL: %w", err)
	}
	return nil
}

func replacePrivateKeyFile(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	const moveFileReplaceExisting = 0x1
	const moveFileWriteThrough = 0x8
	return windows.MoveFileEx(sourcePointer, destinationPointer, moveFileReplaceExisting|moveFileWriteThrough)
}

func linkPrivateKeyFile(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	if err := windows.CreateHardLink(destinationPointer, sourcePointer, 0); err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return os.ErrExist
		}
		return err
	}
	return nil
}
