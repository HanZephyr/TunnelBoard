package biz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

var ErrRestorePreviewStale = errors.New("biz: restore preview is stale")

const restoreRuntimeBudget = 5 * time.Second

type RestoreFacts struct {
	VaultRevision   string `json:"vaultRevision"`
	RuntimeRevision string `json:"runtimeRevision"`
	RouteRevision   string `json:"routeRevision"`
}

type RestoreStageRequest struct{ Path, Password string }

type RestorePreview struct {
	Token      string             `json:"token"`
	ExpiresAt  time.Time          `json:"expiresAt"`
	Counts     BackupEntityCounts `json:"counts"`
	FileDigest string             `json:"fileDigest"`
	Facts      RestoreFacts       `json:"facts"`
}

type RestoreCommitRequest struct {
	Token     string
	Confirmed bool
}

type RestoreCommitResult struct {
	TransactionID  string `json:"transactionId"`
	Quarantined    bool   `json:"quarantined"`
	JournalPending bool   `json:"journalPending"`
}

type RestoreVaultCandidate struct{ ID string }
type RestoreSuspendPlan struct{ RunningForwardIDs []int }

type RestoreJournal struct {
	TransactionID              string       `json:"transactionId"`
	FileDigest                 string       `json:"fileDigest"`
	Before                     RestoreFacts `json:"before"`
	BeforeVaultStorageRevision string       `json:"beforeVaultStorageRevision"`
	Phase                      string       `json:"phase"`
	CandidateID                string       `json:"candidateId,omitempty"`
	RunningForwardIDs          []int        `json:"runningForwardIds,omitempty"`
	RoutesNeutralized          bool         `json:"routesNeutralized,omitempty"`
	VaultReplaced              bool         `json:"vaultReplaced,omitempty"`
}

type RestoreCompensation struct {
	TransactionID     string
	Before            RestoreFacts
	Candidate         RestoreVaultCandidate
	SuspendPlan       RestoreSuspendPlan
	RoutesNeutralized bool
	ReplacedVault     bool
}

// RestoreEffects is an internal seam. Its Adapter owns durable candidate/journal writes,
// Runtime suspension and Route reconciliation; RestoreCoordinator owns their ordering.
type RestoreEffects interface {
	Snapshot(context.Context) (RestoreFacts, error)
	VaultStorageRevision(context.Context) (string, error)
	CaptureRunningForwards(context.Context) ([]int, error)
	PrepareCandidate(context.Context, model.VaultData) (RestoreVaultCandidate, error)
	WriteJournal(context.Context, RestoreJournal) error
	SuspendAll(context.Context) (RestoreSuspendPlan, error)
	NeutralizeRoutes(context.Context) error
	ReplaceVault(context.Context, RestoreVaultCandidate) error
	EnterQuarantine(context.Context) error
	CommitJournal(context.Context, string) error
	Compensate(context.Context, RestoreCompensation) error
}

const (
	RestorePhasePlanned           = "planned"
	RestorePhaseCandidatePrepared = "candidate_prepared"
	RestorePhaseRuntimeSuspending = "runtime_suspending"
	RestorePhaseRuntimeSuspended  = "runtime_suspended"
	RestorePhaseRoutesNeutralized = "routes_neutralized"
	RestorePhaseReplacingVault    = "replacing_vault"
	RestorePhaseVaultReplaced     = "vault_replaced"
	RestorePhaseQuarantined       = "quarantined"
)

// RestoreCoordinator is the deep Module exposed to the application layer.
// Stage is read-only; Commit consumes a confirmed, current staged token exactly once.
type RestoreCoordinator struct {
	mu       sync.Mutex
	packages BackupPackage
	effects  RestoreEffects
	staged   *restoreStageState
}

type restoreStageState struct {
	token string
	facts RestoreFacts
}

func NewRestoreCoordinator(packages BackupPackage, effects RestoreEffects) *RestoreCoordinator {
	return &RestoreCoordinator{packages: packages, effects: effects}
}

func (c *RestoreCoordinator) StageRestore(ctx context.Context, request RestoreStageRequest) (RestorePreview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.staged = nil
	facts, err := c.effects.Snapshot(ctx)
	if err != nil {
		return RestorePreview{}, err
	}
	preview, err := c.packages.Stage(ctx, StageRequest{
		Path: request.Path, Password: request.Password, Purpose: StagePurposeRestore, VaultRevision: facts.VaultRevision,
	})
	if err != nil {
		return RestorePreview{}, err
	}
	c.staged = &restoreStageState{token: preview.Token, facts: facts}
	return RestorePreview{
		Token: preview.Token, ExpiresAt: preview.ExpiresAt,
		Counts: preview.Counts, FileDigest: preview.FileDigest, Facts: facts,
	}, nil
}

func (c *RestoreCoordinator) CommitRestore(ctx context.Context, request RestoreCommitRequest) (RestoreCommitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !request.Confirmed {
		return RestoreCommitResult{}, ErrRestoreNotConfirmed
	}
	if c.staged == nil || request.Token == "" || request.Token != c.staged.token {
		return RestoreCommitResult{}, ErrBackupStageToken
	}
	current, err := c.effects.Snapshot(ctx)
	if err != nil {
		return RestoreCommitResult{}, err
	}
	if current != c.staged.facts {
		c.packages.Cancel(request.Token)
		c.staged = nil
		return RestoreCommitResult{}, ErrRestorePreviewStale
	}
	staged, err := c.packages.Take(ctx, TakeStageRequest{
		Token: request.Token, Purpose: StagePurposeRestore, VaultRevision: current.VaultRevision,
	})
	if err != nil {
		c.staged = nil
		return RestoreCommitResult{}, err
	}
	c.staged = nil
	// CA trust is current-user/current-machine applied state and is never portable.
	staged.Vault.Prefs.CATrustedSHA256 = ""
	transactionID, err := newRestoreTransactionID()
	if err != nil {
		clearKeyFiles(staged.KeyFiles)
		return RestoreCommitResult{}, err
	}
	beforeStorageRevision, err := c.effects.VaultStorageRevision(ctx)
	if err != nil {
		clearKeyFiles(staged.KeyFiles)
		return RestoreCommitResult{}, fmt.Errorf("biz: capture restore vault storage revision: %w", err)
	}
	runningForwardIDs, err := c.effects.CaptureRunningForwards(ctx)
	if err != nil {
		clearKeyFiles(staged.KeyFiles)
		return RestoreCommitResult{}, fmt.Errorf("biz: capture restore runtime plan: %w", err)
	}
	compensation := RestoreCompensation{TransactionID: transactionID, Before: current}
	journal := RestoreJournal{
		TransactionID: transactionID, FileDigest: staged.FileDigest, Before: current,
		BeforeVaultStorageRevision: beforeStorageRevision,
		Phase:                      RestorePhasePlanned, RunningForwardIDs: append([]int(nil), runningForwardIDs...),
	}
	if err := c.effects.WriteJournal(ctx, journal); err != nil {
		clearKeyFiles(staged.KeyFiles)
		return RestoreCommitResult{}, fmt.Errorf("biz: write restore journal: %w", err)
	}
	candidate, err := c.effects.PrepareCandidate(ctx, staged.Vault)
	clearKeyFiles(staged.KeyFiles)
	if err != nil {
		return c.compensate(ctx, compensation, "prepare restore candidate", err)
	}
	compensation.Candidate = candidate
	journal.CandidateID = candidate.ID
	journal.Phase = RestorePhaseCandidatePrepared
	if err := c.effects.WriteJournal(ctx, journal); err != nil {
		return c.compensate(ctx, compensation, "persist prepared restore candidate", err)
	}
	// Persist intent before touching Runtime. If the process exits while SuspendAll is
	// running, startup recovery must conservatively restore the captured running set.
	journal.Phase = RestorePhaseRuntimeSuspending
	if err := c.effects.WriteJournal(ctx, journal); err != nil {
		return c.compensate(ctx, compensation, "persist runtime suspension intent", err)
	}
	suspendCtx, suspendCancel := context.WithTimeout(ctx, restoreRuntimeBudget)
	plan, err := c.effects.SuspendAll(suspendCtx)
	suspendCancel()
	compensation.SuspendPlan = plan
	if err != nil {
		return c.compensate(ctx, compensation, "suspend runtime", err)
	}
	journal.RunningForwardIDs = append([]int(nil), plan.RunningForwardIDs...)
	journal.Phase = RestorePhaseRuntimeSuspended
	if err := c.effects.WriteJournal(ctx, journal); err != nil {
		return c.compensate(ctx, compensation, "persist suspended runtime", err)
	}
	if err := c.effects.NeutralizeRoutes(ctx); err != nil {
		return c.compensate(ctx, compensation, "neutralize routes", err)
	}
	compensation.RoutesNeutralized = true
	journal.RoutesNeutralized = true
	journal.Phase = RestorePhaseRoutesNeutralized
	if err := c.effects.WriteJournal(ctx, journal); err != nil {
		return c.compensate(ctx, compensation, "persist neutralized routes", err)
	}
	journal.Phase = RestorePhaseReplacingVault
	if err := c.effects.WriteJournal(ctx, journal); err != nil {
		return c.compensate(ctx, compensation, "persist vault replacement intent", err)
	}
	if err := c.effects.ReplaceVault(ctx, candidate); err != nil {
		return c.compensate(ctx, compensation, "replace vault", err)
	}
	compensation.ReplacedVault = true
	journal.VaultReplaced = true
	journal.Phase = RestorePhaseVaultReplaced
	if err := c.effects.WriteJournal(ctx, journal); err != nil {
		return RestoreCommitResult{TransactionID: transactionID, JournalPending: true}, fmt.Errorf("biz: persist replaced vault: %w", err)
	}
	// After replacement, failures remain journal-pending and startup recovery must converge
	// to the new Vault in quarantine; rolling back here could resurrect old network effects.
	if err := c.effects.EnterQuarantine(ctx); err != nil {
		return RestoreCommitResult{TransactionID: transactionID, JournalPending: true}, fmt.Errorf("biz: enter restore quarantine: %w", err)
	}
	journal.Phase = RestorePhaseQuarantined
	if err := c.effects.WriteJournal(ctx, journal); err != nil {
		return RestoreCommitResult{TransactionID: transactionID, Quarantined: true, JournalPending: true}, fmt.Errorf("biz: persist restore quarantine: %w", err)
	}
	if err := c.effects.CommitJournal(ctx, transactionID); err != nil {
		return RestoreCommitResult{TransactionID: transactionID, Quarantined: true, JournalPending: true}, fmt.Errorf("biz: commit restore journal: %w", err)
	}
	return RestoreCommitResult{TransactionID: transactionID, Quarantined: true}, nil
}

func (c *RestoreCoordinator) compensate(ctx context.Context, state RestoreCompensation, operation string, cause error) (RestoreCommitResult, error) {
	// Compensation must get a fresh budget: the suspend context may already have expired,
	// and reusing it would make recovery of successfully stopped Forward instances impossible.
	compensationCtx, cancel := context.WithTimeout(context.Background(), restoreRuntimeBudget)
	defer cancel()
	compensationErr := c.effects.Compensate(compensationCtx, state)
	if compensationErr != nil {
		return RestoreCommitResult{TransactionID: state.TransactionID, JournalPending: true}, fmt.Errorf("biz: %s: %w; compensation failed: %v", operation, cause, compensationErr)
	}
	return RestoreCommitResult{}, fmt.Errorf("biz: %s: %w", operation, cause)
}

func newRestoreTransactionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("biz: create restore transaction id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
