//go:build !linux

package helper

import "context"

func newPlatformIntegration(dataDir string) (Operator, LocalCATrust, func(context.Context) error, error) {
	operator := NewOperator()
	trust := NewCurrentUserCATrustAt(dataDir)
	var closeFn func(context.Context) error
	if closer, ok := operator.(interface{ Close(context.Context) error }); ok {
		closeFn = closer.Close
	}
	return operator, trust, closeFn, nil
}
