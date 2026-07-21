//go:build linux

package helper

func newNativePlatformPrivilege() (PlatformPrivilege, error) {
	return NewPlatformPrivilege(PlatformPrivilegeOptions{Platform: "linux"})
}
