package application

import (
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
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
}

type SaveSSHHostResult struct {
	Host               SSHHostView    `json:"host"`
	ConnectionChanged  bool           `json:"connectionChanged"`
	AffectedForwardIDs []int          `json:"affectedForwardIds,omitempty"`
	RunningForwardIDs  []int          `json:"runningForwardIds,omitempty"`
	RequiresRestart    bool           `json:"requiresRestart"`
	RestartErrors      map[int]string `json:"restartErrors,omitempty"`
	AcceptedRevision   string         `json:"acceptedRevision"`
	EventSequence      uint64         `json:"eventSequence"`
}

type MoveForwardsCommand struct {
	Meta           CommandMeta `json:"meta"`
	ForwardIDs     []int       `json:"forwardIds"`
	TargetFolderID int         `json:"targetFolderId"`
}

type MoveForwardsResult struct {
	MovedIDs         []int  `json:"movedIds"`
	AcceptedRevision string `json:"acceptedRevision"`
	EventSequence    uint64 `json:"eventSequence"`
}

type RouteChangeAction string

const (
	RouteChangeUpsert  RouteChangeAction = "upsert"
	RouteChangeSetFlag RouteChangeAction = "set_flag"
	RouteChangeDelete  RouteChangeAction = "delete"
)

type RouteFlag string

const (
	RouteFlagHostsEnabled RouteFlag = "hostsEnabled"
	RouteFlagCaddyEnabled RouteFlag = "caddyEnabled"
)

// RouteChangeIntent 只表达一次用户意图；SetFlag 不携带旧 Route 全字段，
// Upsert 用于 RouteModal，Delete 用于删除确认。
type RouteChangeIntent struct {
	ExpectedRevision string            `json:"expectedRevision"`
	Action           RouteChangeAction `json:"action"`
	RouteID          int               `json:"routeId,omitempty"`
	Flag             RouteFlag         `json:"flag,omitempty"`
	Enabled          bool              `json:"enabled,omitempty"`
	Route            *model.WebRoute   `json:"route,omitempty"`
}

type RouteFlagChange struct {
	Flag    RouteFlag `json:"flag"`
	Enabled bool      `json:"enabled"`
}

type RouteChangePreview struct {
	Token                string            `json:"token"`
	ExpiresAt            time.Time         `json:"expiresAt"`
	DesiredRevision      string            `json:"desiredRevision"`
	AppliedRevision      string            `json:"appliedRevision"`
	Route                *model.WebRoute   `json:"route,omitempty"`
	LinkedChanges        []RouteFlagChange `json:"linkedChanges,omitempty"`
	HostsRecords         []route.HostEntry `json:"hostsRecords,omitempty"`
	RequiresConfirmation []string          `json:"requiresConfirmation,omitempty"`
	CATrustNeeded        bool              `json:"caTrustNeeded"`
}

type CommitRouteChangeCommand struct {
	Token            string   `json:"token"`
	ConfirmedDomains []string `json:"confirmedDomains,omitempty"`
	ConfirmCATrust   bool     `json:"confirmCATrust"`
}

type RouteCommandOutcome string

const (
	RouteOutcomeApplied         RouteCommandOutcome = "applied"
	RouteOutcomeHostsOnly       RouteCommandOutcome = "hosts_only"
	RouteOutcomeSavedNotApplied RouteCommandOutcome = "saved_not_applied"
	RouteOutcomeRejected        RouteCommandOutcome = "rejected"
	RouteOutcomeStateUnknown    RouteCommandOutcome = "state_unknown"
)

type AppErrorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RouteCommandResult struct {
	Outcome             RouteCommandOutcome    `json:"outcome"`
	DesiredSaved        bool                   `json:"desiredSaved"`
	AcceptedRevision    string                 `json:"acceptedRevision,omitempty"`
	Route               *model.WebRoute        `json:"route,omitempty"`
	Applied             *biz.RouteAppliedState `json:"applied,omitempty"`
	Error               *AppErrorView          `json:"error,omitempty"`
	StateMayHaveChanged bool                   `json:"stateMayHaveChanged"`
	EventSequence       uint64                 `json:"eventSequence,omitempty"`
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
	Preview   biz.ImportPreview `json:"preview"`
}

type CommitImportCommand struct {
	Meta  CommandMeta    `json:"meta"`
	Token string         `json:"token"`
	Plan  biz.ImportPlan `json:"plan"`
}

type CommitImportResult struct {
	Summary          biz.ImportSummary `json:"summary"`
	AcceptedRevision string            `json:"acceptedRevision"`
	EventSequence    uint64            `json:"eventSequence"`
}
