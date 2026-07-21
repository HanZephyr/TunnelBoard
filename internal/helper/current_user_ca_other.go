//go:build !windows

package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

var errCurrentUserCATrustUnsupported = errors.New("helper: current-user CA trust is not implemented on this platform")

func CurrentUserDataDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "TunnelBoard"), nil
}

func NewCurrentUserCATrust() (LocalCATrust, error) {
	root, err := CurrentUserDataDir()
	if err != nil {
		return nil, err
	}
	return NewCurrentUserCATrustAt(root), nil
}

func NewCurrentUserCATrustAt(string) LocalCATrust { return unsupportedCurrentUserCATrust{} }

type unsupportedCurrentUserCATrust struct{}

func (unsupportedCurrentUserCATrust) EnsureCurrentCaddyCATrusted(context.Context) (CAIdentity, error) {
	return CAIdentity{}, errCurrentUserCATrustUnsupported
}
func (unsupportedCurrentUserCATrust) RemoveCurrentCaddyCA(context.Context) error { return nil }
func (unsupportedCurrentUserCATrust) Status(context.Context) (CATrustStatus, error) {
	return CATrustStatus{State: CAUnavailable}, nil
}
