//go:build linux

package helper

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
)

const (
	linuxPolkitBusName            = "org.freedesktop.PolicyKit1"
	linuxPolkitObject             = "/org/freedesktop/PolicyKit1/Authority"
	linuxPolkitInterface          = "org.freedesktop.PolicyKit1.Authority"
	linuxPolkitActionID           = "io.github.hanzephyr.TunnelBoard.manage-system"
	polkitAllowInteraction uint32 = 1
)

type linuxPolkitSubject struct {
	Kind    string
	Details map[string]dbus.Variant
}

type linuxPolkitTemporaryAuthorization struct {
	ID           string
	ActionID     string
	Subject      linuxPolkitSubject
	TimeObtained uint64
	TimeExpires  uint64
}

type linuxPolkitAuthority struct {
	connect   func() (*dbus.Conn, error)
	pid       int
	startTime func(int) (uint64, error)
	uid       int32
}

func newLinuxPolkitAuthorizer() linuxPolkitAuthorizer {
	return &linuxPolkitAuthority{
		connect: dbus.SystemBus, pid: os.Getpid(), startTime: linuxProcessStartTime, uid: int32(os.Getuid()),
	}
}

// Authorize 用 GUI 自身的 unix-process subject 请求同一个 polkit action。policy
// 必须产生 temporary authorization；没有 opaque ID 就不启动 pkexec，以保证退出可精确撤销。
func (a *linuxPolkitAuthority) Authorize(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if a == nil || a.connect == nil || a.startTime == nil || a.pid <= 1 {
		return "", errors.New("helper: Linux polkit authority is not configured")
	}
	startTime, err := a.startTime(a.pid)
	if err != nil {
		return "", err
	}
	connection, err := a.connect()
	if err != nil {
		return "", fmt.Errorf("helper: connect to polkit authority: %w", err)
	}
	defer connection.Close()
	subject := newLinuxPolkitSubject(a.pid, startTime, a.uid)
	var authorized, challenge bool
	var details map[string]string
	call := connection.Object(linuxPolkitBusName, dbus.ObjectPath(linuxPolkitObject)).CallWithContext(
		ctx,
		linuxPolkitInterface+".CheckAuthorization",
		0,
		subject,
		linuxPolkitActionID,
		map[string]string{},
		polkitAllowInteraction,
		"",
	)
	if call.Err != nil {
		return "", fmt.Errorf("helper: request polkit authorization: %w", call.Err)
	}
	if err := call.Store(&authorized, &challenge, &details); err != nil {
		return "", fmt.Errorf("helper: decode polkit authorization result: %w", err)
	}
	if !authorized {
		if challenge {
			return "", errors.New("helper: polkit authorization requires an unavailable authentication agent")
		}
		return "", errors.New("helper: polkit authorization was denied")
	}
	authorizationID := details["polkit.temporary_authorization_id"]
	if authorizationID == "" {
		return "", errors.New("helper: polkit authorization is not a revocable temporary authorization")
	}
	return authorizationID, nil
}

func newLinuxPolkitSubject(pid int, startTime uint64, uid int32) linuxPolkitSubject {
	return linuxPolkitSubject{
		Kind: "unix-process",
		Details: map[string]dbus.Variant{
			"pid":        dbus.MakeVariant(uint32(pid)),
			"start-time": dbus.MakeVariant(startTime),
			"uid":        dbus.MakeVariant(uid),
		},
	}
}

func (a *linuxPolkitAuthority) Revoke(ctx context.Context, authorizationID string) error {
	if authorizationID == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.connect == nil {
		return errors.New("helper: Linux polkit authority is not configured")
	}
	connection, err := a.connect()
	if err != nil {
		return fmt.Errorf("helper: connect to polkit authority for revoke: %w", err)
	}
	defer connection.Close()
	call := connection.Object(linuxPolkitBusName, dbus.ObjectPath(linuxPolkitObject)).CallWithContext(
		ctx, linuxPolkitInterface+".RevokeTemporaryAuthorizationById", 0, authorizationID,
	)
	if call.Err != nil {
		return fmt.Errorf("helper: revoke exact Linux temporary authorization: %w", call.Err)
	}
	return nil
}
