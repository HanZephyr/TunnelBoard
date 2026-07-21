package biz

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

type RouteApplyStatus string

const (
	RouteStatusApplied        RouteApplyStatus = "applied"
	RouteStatusHostsOnly      RouteApplyStatus = "hosts_only"
	RouteStatusPending        RouteApplyStatus = "pending"
	RouteStatusError          RouteApplyStatus = "error"
	RouteStatusCleanupPending RouteApplyStatus = "cleanup_pending"
	RouteStatusQuarantined    RouteApplyStatus = "quarantined"
	RouteStatusUnknown        RouteApplyStatus = "unknown"
)

// RouteAppliedState 是当前用户、当前机器的事实，不属于可移植 Vault 或备份。
type RouteAppliedState struct {
	AppliedDesiredRevision string            `json:"appliedDesiredRevision,omitempty"`
	HostsDigest            string            `json:"hostsDigest,omitempty"`
	AppliedHosts           []route.HostEntry `json:"appliedHosts,omitempty"`
	CaddyConfigDigest      string            `json:"caddyConfigDigest,omitempty"`
	CaddyGeneration        string            `json:"caddyGeneration,omitempty"`
	CATrustedSHA256        string            `json:"caTrustedSHA256,omitempty"`
	Status                 RouteApplyStatus  `json:"status"`
	PortConflict           string            `json:"portConflict,omitempty"`
	LastError              string            `json:"lastError,omitempty"`
	PendingTxID            string            `json:"pendingTxId,omitempty"`
}

type routeJournal struct {
	TxID            string            `json:"txId"`
	DesiredRevision string            `json:"desiredRevision"`
	BeforeApplied   RouteAppliedState `json:"beforeApplied"`
	TargetHosts     []route.HostEntry `json:"targetHosts,omitempty"`
	TargetCaddyHash string            `json:"targetCaddyHash,omitempty"`
	Phase           string            `json:"phase"`
	CreatedAt       time.Time         `json:"createdAt"`
}

type routeStateStore struct {
	statePath   string
	journalPath string
}

func newRouteStateStore(baseDir string) routeStateStore {
	return routeStateStore{
		statePath:   filepath.Join(baseDir, "state", "route-applied.json"),
		journalPath: filepath.Join(baseDir, "state", "route-journal.json"),
	}
}

func (s routeStateStore) loadState() (RouteAppliedState, error) {
	var state RouteAppliedState
	err := readJSONFile(s.statePath, &state)
	if errors.Is(err, os.ErrNotExist) {
		state.Status = RouteStatusUnknown
		return state, nil
	}
	return state, err
}

func (s routeStateStore) saveState(state RouteAppliedState) error {
	return writeJSONAtomic(s.statePath, state)
}

func (s routeStateStore) loadJournal() (routeJournal, error) {
	var journal routeJournal
	return journal, readJSONFile(s.journalPath, &journal)
}

func (s routeStateStore) saveJournal(journal routeJournal) error {
	return writeJSONAtomic(s.journalPath, journal)
}

func (s routeStateStore) clearJournal() error {
	err := os.Remove(s.journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func readJSONFile(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("route state: decode %s: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("route state: create directory: %w", err)
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("route state: encode: %w", err)
	}
	file, err := os.CreateTemp(dir, ".route-state-*.tmp")
	if err != nil {
		return fmt.Errorf("route state: create temporary file: %w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("route state: persist: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("route state: replace: %w", err)
	}
	return nil
}

func desiredRouteRevision(data model.VaultData) string {
	payload := struct {
		Forwards []model.Forward  `json:"forwards"`
		Routes   []model.WebRoute `json:"routes"`
	}{Forwards: data.Forwards, Routes: data.WebRoutes}
	raw, _ := json.Marshal(payload)
	return digestBytes(raw)
}

func digestHosts(entries []route.HostEntry) string {
	raw, _ := json.Marshal(entries)
	return digestBytes(raw)
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func newRouteTxID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func sanitizeRouteError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
