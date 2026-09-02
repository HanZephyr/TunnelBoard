package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/HanZephyr/TunnelBoard/internal/loopbackhttps"
)

const (
	darwinPFAnchorPath = "/etc/pf.anchors/com.hanzephyr.tunnelboard"
	darwinPFConfPath   = "/etc/pf.conf"
	darwinHTTPSPlistPath = "/Library/LaunchDaemons/com.hanzephyr.tunnelboard.https-redirect.plist"
	darwinPFAnchorName = "com.hanzephyr.tunnelboard"
)

const darwinPFConfSnippet = "rdr-anchor \"" + darwinPFAnchorName + "\"\nload anchor \"" + darwinPFAnchorName + "\" from \"" + darwinPFAnchorPath + "\"\n"

const sampleApplePFConf = `scrub-anchor "com.apple/*"
nat-anchor "com.apple/*"
rdr-anchor "com.apple/*"
dummynet-anchor "com.apple/*"
anchor "com.apple/*"
load anchor "com.apple" from "/etc/pf.anchors/com.apple"
`

func darwinPFAnchorContents() string {
	return fmt.Sprintf("rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port %d -> 127.0.0.1 port %d\n",
		loopbackhttps.PublicPort, loopbackhttps.DarwinUnprivilegedPort)
}

func darwinHTTPSRedirectPlist() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.hanzephyr.tunnelboard.https-redirect</string>
	<key>ProgramArguments</key>
	<array>
		<string>/sbin/pfctl</string>
		<string>-E</string>
		<string>-f</string>
		<string>/etc/pf.conf</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
}

func mergePFConf(existing string) string {
	if strings.Contains(existing, `rdr-anchor "`+darwinPFAnchorName+`"`) {
		return existing
	}
	trimmed := strings.TrimRight(existing, "\n")
	if trimmed == "" {
		return darwinPFConfSnippet
	}
	return trimmed + "\n\n" + darwinPFConfSnippet
}

func loopbackHTTPSRedirectInstalled() bool {
	got, err := os.ReadFile(darwinPFAnchorPath)
	if err != nil {
		return false
	}
	return string(got) == darwinPFAnchorContents()
}

func (p *platformPrivilege) EnsureLoopbackHTTPSRedirect(ctx context.Context) error {
	if p.platform != "darwin" {
		return nil
	}
	if loopbackHTTPSRedirectInstalled() {
		return nil
	}
	existing, err := os.ReadFile(darwinPFConfPath)
	if err != nil {
		if runtime.GOOS == "darwin" {
			return fmt.Errorf("helper: read pf.conf: %w", err)
		}
		existing = []byte(sampleApplePFConf)
	}
	dir, cleanup, err := p.writePrivateDir(map[string][]byte{
		"anchor": []byte(darwinPFAnchorContents()),
		"pf.conf": []byte(mergePFConf(string(existing))),
		"plist":  []byte(darwinHTTPSRedirectPlist()),
	})
	if err != nil {
		return err
	}
	defer cleanup()
	return p.runDarwin(ctx, "ensure-https-redirect", dir, "/etc/pf.conf", "/usr/bin/true")
}

func (p *platformPrivilege) RepairDataDirOwner(ctx context.Context, dir, owner string) error {
	if p.platform != "darwin" {
		return nil
	}
	if err := validateRepairOwner(owner); err != nil {
		return err
	}
	if err := validateRepairDir(dir); err != nil {
		return err
	}
	return p.runDarwin(ctx, "repair-data-dir", owner, dir, "/usr/sbin/chown")
}

func validateRepairOwner(owner string) error {
	if owner == "" || len(owner) > 64 {
		return errors.New("helper: invalid data dir owner")
	}
	for _, r := range owner {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			continue
		}
		return errors.New("helper: invalid data dir owner")
	}
	return nil
}

func validateRepairDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("helper: data dir must be absolute")
	}
	cleaned := filepath.Clean(dir)
	if cleaned != dir {
		return errors.New("helper: data dir must be cleaned")
	}
	expected, err := CurrentUserDataDir()
	if err != nil {
		return err
	}
	if cleaned != filepath.Clean(expected) {
		return errors.New("helper: data dir is not the current user TunnelBoard directory")
	}
	return nil
}
