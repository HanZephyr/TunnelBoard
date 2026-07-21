package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

var ErrBackupResourceLimit = errors.New("vault: backup resource limit exceeded")

const (
	MaxBackupPackageBytes  = 64 << 20
	maxBackupPasswordBytes = 1 << 10
	maxBackupFolders       = 500
	maxBackupSSHHosts      = 1000
	maxBackupForwards      = 5000
	maxBackupWebRoutes     = 2000
	maxBackupHostKeys      = 5000
	maxBackupEntities      = 10000
	maxBackupChainHops     = 16
	maxBackupKeyFiles      = 64
	maxBackupKeyFileBytes  = 1 << 20
	maxBackupAllKeysBytes  = 16 << 20
	maxBackupShortString   = 256
	maxBackupHostString    = 255
	maxBackupPathString    = 4 << 10
	maxBackupNotesString   = 16 << 10
	maxBackupAllStrings    = 8 << 20
	maxBackupJSONDepth     = 32
)

var backupKDFMu sync.Mutex

func resourceLimit(resource string, got, limit int) error {
	return fmt.Errorf("%w: %s is %d, limit is %d", ErrBackupResourceLimit, resource, got, limit)
}

func validateBackupPassword(password string) error {
	n := len([]byte(password))
	if n == 0 {
		return errors.New("vault: backup password is required")
	}
	if n > maxBackupPasswordBytes {
		return resourceLimit("password bytes", n, maxBackupPasswordBytes)
	}
	return nil
}

func validateBackupPayload(data model.VaultData, keyFiles map[string][]byte) error {
	counts := []struct {
		name  string
		got   int
		limit int
	}{
		{"folders", len(data.Folders), maxBackupFolders},
		{"ssh hosts", len(data.SSHHosts), maxBackupSSHHosts},
		{"forwards", len(data.Forwards), maxBackupForwards},
		{"web routes", len(data.WebRoutes), maxBackupWebRoutes},
		{"host keys", len(data.HostKeys), maxBackupHostKeys},
		{"key files", len(keyFiles), maxBackupKeyFiles},
	}
	totalEntities := 0
	for _, count := range counts {
		if count.got > count.limit {
			return resourceLimit(count.name, count.got, count.limit)
		}
		if count.name != "key files" {
			totalEntities += count.got
		}
	}
	if totalEntities > maxBackupEntities {
		return resourceLimit("entities", totalEntities, maxBackupEntities)
	}

	totalStrings := 0
	check := func(name, value string, limit int) error {
		n := len([]byte(value))
		if n > limit {
			return resourceLimit(name, n, limit)
		}
		totalStrings += n
		return nil
	}
	for _, f := range data.Folders {
		if err := check("folder name bytes", f.Name, maxBackupShortString); err != nil {
			return err
		}
	}
	for _, h := range data.SSHHosts {
		for _, field := range []struct {
			name, value string
			limit       int
		}{
			{"ssh host name bytes", h.Name, maxBackupShortString},
			{"ssh host bytes", h.Host, maxBackupHostString},
			{"ssh user bytes", h.User, maxBackupShortString},
			{"ssh auth type bytes", h.AuthType, maxBackupShortString},
			{"ssh key path bytes", h.KeyPath, maxBackupPathString},
			{"ssh agent path bytes", h.AgentSocketPath, maxBackupPathString},
			{"ssh secret bytes", h.Password, maxBackupNotesString},
			{"ssh algorithms bytes", h.HostKeyAlgorithms, maxBackupNotesString},
			{"ssh notes bytes", h.Notes, maxBackupNotesString},
		} {
			if err := check(field.name, field.value, field.limit); err != nil {
				return err
			}
		}
	}
	for _, fw := range data.Forwards {
		if len(fw.ChainHostIDs) > maxBackupChainHops {
			return resourceLimit("ssh chain hops", len(fw.ChainHostIDs), maxBackupChainHops)
		}
		for _, field := range []struct {
			name, value string
			limit       int
		}{
			{"forward name bytes", fw.Name, maxBackupShortString},
			{"forward mode bytes", fw.Mode, maxBackupShortString},
			{"local host bytes", fw.LocalHost, maxBackupHostString},
			{"remote host bytes", fw.RemoteHost, maxBackupHostString},
			{"forward description bytes", fw.Description, maxBackupNotesString},
		} {
			if err := check(field.name, field.value, field.limit); err != nil {
				return err
			}
		}
	}
	for _, route := range data.WebRoutes {
		if err := check("route domain bytes", route.Domain, maxBackupHostString); err != nil {
			return err
		}
		if err := check("route scheme bytes", route.UpstreamScheme, maxBackupShortString); err != nil {
			return err
		}
		if err := check("route tls sni bytes", route.TLSSNI, maxBackupHostString); err != nil {
			return err
		}
	}
	for _, key := range data.HostKeys {
		if err := check("host key host bytes", key.Host, maxBackupHostString); err != nil {
			return err
		}
		if err := check("host key type bytes", key.KeyType, maxBackupShortString); err != nil {
			return err
		}
		if err := check("host key fingerprint bytes", key.FingerprintSHA256, maxBackupShortString); err != nil {
			return err
		}
	}
	if err := check("locale bytes", data.Prefs.UILocale, maxBackupShortString); err != nil {
		return err
	}
	if err := check("ca fingerprint bytes", data.Prefs.CATrustedSHA256, maxBackupShortString); err != nil {
		return err
	}

	keyBytes := 0
	for path, content := range keyFiles {
		if err := check("key file path bytes", path, maxBackupPathString); err != nil {
			return err
		}
		if len(content) > maxBackupKeyFileBytes {
			return resourceLimit("key file bytes", len(content), maxBackupKeyFileBytes)
		}
		keyBytes += len(content)
	}
	if keyBytes > maxBackupAllKeysBytes {
		return resourceLimit("all key file bytes", keyBytes, maxBackupAllKeysBytes)
	}
	if totalStrings > maxBackupAllStrings {
		return resourceLimit("all string bytes", totalStrings, maxBackupAllStrings)
	}
	return nil
}

func validateBackupJSONDepth(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("vault: decode backup payload: %w", err)
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
				if depth > maxBackupJSONDepth {
					return resourceLimit("json depth", depth, maxBackupJSONDepth)
				}
			case '}', ']':
				depth--
			}
		}
	}
}
