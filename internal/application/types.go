package application

import (
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
)

type CommandMeta struct {
	CommandID        string `json:"commandId"`
	ExpectedRevision string `json:"expectedRevision,omitempty"`
}

type SSHHostView struct {
	ID                  int    `json:"id"`
	Name                string `json:"name"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	User                string `json:"user"`
	AuthType            string `json:"authType"`
	KeyPath             string `json:"keyPath,omitempty"`
	AgentSocketPath     string `json:"agentSocketPath,omitempty"`
	KeepAliveIntervalMs int    `json:"keepAliveIntervalMs,omitempty"`
	TimeoutMs           int    `json:"timeoutMs,omitempty"`
	HostKeyAlgorithms   string `json:"hostKeyAlgorithms,omitempty"`
	Notes               string `json:"notes,omitempty"`
	HasSecret           bool   `json:"hasSecret"`
}

type SSHHostInput struct {
	ID                  int    `json:"id"`
	Name                string `json:"name"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	User                string `json:"user"`
	AuthType            string `json:"authType"`
	KeyPath             string `json:"keyPath,omitempty"`
	AgentSocketPath     string `json:"agentSocketPath,omitempty"`
	KeepAliveIntervalMs int    `json:"keepAliveIntervalMs,omitempty"`
	TimeoutMs           int    `json:"timeoutMs,omitempty"`
	HostKeyAlgorithms   string `json:"hostKeyAlgorithms,omitempty"`
	Notes               string `json:"notes,omitempty"`
}

type CatalogView struct {
	Folders   []model.Folder   `json:"folders"`
	SSHHosts  []SSHHostView    `json:"sshHosts"`
	Forwards  []model.Forward  `json:"forwards"`
	WebRoutes []model.WebRoute `json:"webRoutes"`
	HostKeys  []model.HostKey  `json:"hostKeys"`
}

type PreferencesView struct {
	AutoRun            bool   `json:"autoRun"`
	UpdateCheckEnabled bool   `json:"updateCheckEnabled"`
	UILocale           string `json:"uiLocale,omitempty"`
}

type DomainRevisions struct {
	Vault       string `json:"vault"`
	Runtime     string `json:"runtime"`
	Route       string `json:"route"`
	Preferences string `json:"preferences"`
}

type RecoveryView struct {
	Quarantined    bool `json:"quarantined"`
	JournalPending bool `json:"journalPending"`
	Maintenance    bool `json:"maintenance"`
}

type CapabilityView struct {
	MutationAllowed bool `json:"mutationAllowed"`
}

type AppSnapshot struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	EventSequence   uint64                `json:"eventSequence"`
	ObservedAt      time.Time             `json:"observedAt"`
	Revisions       DomainRevisions       `json:"revisions"`
	Catalog         CatalogView           `json:"catalog"`
	Runtime         []biz.RuntimeStatus   `json:"runtime"`
	Routes          []biz.RouteStatusItem `json:"routes"`
	RouteApplied    biz.RouteAppliedState `json:"routeApplied"`
	Preferences     PreferencesView       `json:"preferences"`
	Recovery        RecoveryView          `json:"recovery"`
	Capabilities    CapabilityView        `json:"capabilities"`
	SSHHostDefaults SSHHostView           `json:"sshHostDefaults"`
}

type SaveSSHHostCommand struct {
	Meta           CommandMeta      `json:"meta"`
	Host           SSHHostInput     `json:"host"`
	SecretAction   biz.SecretAction `json:"secretAction"`
	SecretInput    string           `json:"secretInput,omitempty"`
	ConfirmRestart bool             `json:"confirmRestart"`
	PreviewToken   string           `json:"previewToken,omitempty"`
}

type SaveSSHHostResult struct {
	Host               SSHHostView    `json:"host"`
	ConnectionChanged  bool           `json:"connectionChanged"`
	AffectedForwardIDs []int          `json:"affectedForwardIds,omitempty"`
	RunningForwardIDs  []int          `json:"runningForwardIds,omitempty"`
	RequiresRestart    bool           `json:"requiresRestart"`
	PreviewToken       string         `json:"previewToken,omitempty"`
	PreviewExpiresAt   time.Time      `json:"previewExpiresAt,omitempty"`
	RestartErrors      map[int]string `json:"restartErrors,omitempty"`
	AcceptedRevision   string         `json:"acceptedRevision"`
	EventSequence      uint64         `json:"eventSequence"`
}

type SSHHostChangePreview struct {
	Token             string                `json:"token"`
	ExpiresAt         time.Time             `json:"expiresAt"`
	Host              SSHHostView           `json:"host"`
	ConnectionChanged bool                  `json:"connectionChanged"`
	RequiresCommit    bool                  `json:"requiresCommit"`
	AffectedForwards  []biz.AffectedForward `json:"affectedForwards,omitempty"`
	AcceptedRevision  string                `json:"acceptedRevision"`
}

type CommitSSHHostChangeCommand struct {
	Meta  CommandMeta `json:"meta"`
	Token string      `json:"token"`
}

type SSHHostChangeForwardResult struct {
	ForwardID          int    `json:"forwardId"`
	PreviousGeneration uint64 `json:"previousGeneration,omitempty"`
	Status             string `json:"status"`
	Error              string `json:"error,omitempty"`
	CompensationError  string `json:"compensationError,omitempty"`
}

type CommitSSHHostChangeResult struct {
	Committed        bool                         `json:"committed"`
	Host             SSHHostView                  `json:"host"`
	FailureStage     string                       `json:"failureStage,omitempty"`
	OperationError   string                       `json:"operationError,omitempty"`
	PreflightErrors  map[int]string               `json:"preflightErrors,omitempty"`
	ForwardResults   []SSHHostChangeForwardResult `json:"forwardResults,omitempty"`
	AcceptedRevision string                       `json:"acceptedRevision,omitempty"`
	EventSequence    uint64                       `json:"eventSequence,omitempty"`
}

type MoveForwardsCommand struct {
	Meta           CommandMeta `json:"meta"`
	ForwardIDs     []int       `json:"forwardIds"`
	TargetFolderID int         `json:"targetFolderId"`
}

type MoveForwardsResult struct {
	ChangedIDs       []int  `json:"changedIds"`
	UnchangedIDs     []int  `json:"unchangedIds"`
	AcceptedRevision string `json:"acceptedRevision"`
	EventSequence    uint64 `json:"eventSequence"`
}

type PreviewLocalListenerCommand struct {
	Mode      string `json:"mode"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	ForwardID int    `json:"forwardId,omitempty"`
}

type LocalListenerPreview struct {
	State             string `json:"state"`
	NormalizedAddress string `json:"normalizedAddress,omitempty"`
	OwnerForwardID    int    `json:"ownerForwardId,omitempty"`
	ErrorCode         string `json:"errorCode,omitempty"`
}

type StageImportRequest struct {
	Path     string `json:"path"`
	Password string `json:"password"`
}

type ImportStagePreview struct {
	Token     string            `json:"token"`
	ExpiresAt time.Time         `json:"expiresAt"`
	Preview   ImportPreviewView `json:"preview"`
}

type KeyFileView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int    `json:"size"`
}

type ImportPreviewView struct {
	Counts        map[string]int     `json:"counts"`
	FolderName    string             `json:"folderName"`
	HostConflicts []biz.HostConflict `json:"hostConflicts"`
	KeyFiles      []KeyFileView      `json:"keyFiles"`
}

type CommitImportCommand struct {
	Meta  CommandMeta    `json:"meta"`
	Token string         `json:"token"`
	Plan  biz.ImportPlan `json:"plan"`
}

type CommitImportResult struct {
	Summary          biz.ImportSummary `json:"summary"`
	KeyFiles         []KeyFileView     `json:"keyFiles,omitempty"`
	AcceptedRevision string            `json:"acceptedRevision"`
	EventSequence    uint64            `json:"eventSequence"`
}

type SaveImportKeyFileCommand struct {
	Token         string `json:"token"`
	KeyID         string `json:"keyId"`
	SuggestedName string `json:"suggestedName,omitempty"`
}
