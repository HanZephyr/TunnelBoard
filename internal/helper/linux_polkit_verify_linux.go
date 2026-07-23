//go:build linux

package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// ValidateLinuxTemporaryAuthorization 在每个 root 副作用前确认临时授权仍精确
// 属于已验证的 GUI 进程与 TunnelBoard action。revoke 消息不经过本检查，确保即使
// 授权已到期也能关闭会话。
func ValidateLinuxTemporaryAuthorization(ctx context.Context, authorizationID string, parentPID int, parentStartTime uint64) error {
	if authorizationID == "" {
		return errors.New("helper: Linux temporary authorization id is required")
	}
	uidText := strings.TrimSpace(os.Getenv("PKEXEC_UID"))
	uidValue, err := strconv.ParseInt(uidText, 10, 32)
	if err != nil {
		return errors.New("helper: pkexec caller identity is unavailable")
	}
	subject := newLinuxPolkitSubject(parentPID, parentStartTime, int32(uidValue))
	connection, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("helper: connect to polkit authority for validation: %w", err)
	}
	defer connection.Close()
	var entries []linuxPolkitTemporaryAuthorization
	call := connection.Object(linuxPolkitBusName, dbus.ObjectPath(linuxPolkitObject)).CallWithContext(
		ctx, linuxPolkitInterface+".EnumerateTemporaryAuthorizations", 0, subject,
	)
	if call.Err != nil {
		return fmt.Errorf("helper: enumerate Linux temporary authorizations: %w", call.Err)
	}
	if err := call.Store(&entries); err != nil {
		return fmt.Errorf("helper: decode Linux temporary authorizations: %w", err)
	}
	now := uint64(time.Now().Unix())
	for _, entry := range entries {
		if entry.ID == authorizationID && entry.ActionID == linuxPolkitActionID && entry.TimeExpires > now && sameLinuxPolkitSubject(entry.Subject, subject) {
			return nil
		}
	}
	return errors.New("helper: exact Linux temporary authorization is inactive")
}

func sameLinuxPolkitSubject(left, right linuxPolkitSubject) bool {
	if left.Kind != "unix-process" || left.Kind != right.Kind {
		return false
	}
	leftPID, leftPIDOK := left.Details["pid"].Value().(uint32)
	rightPID, rightPIDOK := right.Details["pid"].Value().(uint32)
	leftStart, leftStartOK := left.Details["start-time"].Value().(uint64)
	rightStart, rightStartOK := right.Details["start-time"].Value().(uint64)
	leftUID, leftUIDOK := left.Details["uid"].Value().(int32)
	rightUID, rightUIDOK := right.Details["uid"].Value().(int32)
	return leftPIDOK && rightPIDOK && leftStartOK && rightStartOK && leftUIDOK && rightUIDOK &&
		leftPID == rightPID && leftStart == rightStart && leftUID == rightUID
}
