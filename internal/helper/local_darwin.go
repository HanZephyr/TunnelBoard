//go:build darwin

package helper

func newNativePlatformPrivilege() (PlatformPrivilege, error) {
	return NewPlatformPrivilege(PlatformPrivilegeOptions{Platform: "darwin"})
}
