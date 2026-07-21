package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/application"
	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

type memStore struct{ data model.VaultData }

func (m *memStore) Load() (model.VaultData, error) { return m.data, nil }
func (m *memStore) Update(fn func(*model.VaultData) error) (model.VaultData, error) {
	d := m.data
	if err := fn(&d); err != nil {
		return model.VaultData{}, err
	}
	m.data = d
	return d, nil
}

type fakeRuntime struct {
	affected  []biz.AffectedForward
	suspended []int
	retired   int
	resumed   int
	starts    int
	stops     int
}

func (f *fakeRuntime) Snapshot() ([]biz.RuntimeStatus, error) {
	return []biz.RuntimeStatus{{ForwardID: 1, Status: biz.RuntimeStateRunning}}, nil
}
func (f *fakeRuntime) Start(int) error                        { f.starts++; return nil }
func (f *fakeRuntime) Stop(int) error                         { f.stops++; return nil }
func (f *fakeRuntime) StartAutoStart() (map[int]error, error) { return nil, nil }
func (f *fakeRuntime) Suspend(_ context.Context, ids []int) (biz.RuntimeSuspendPlan, error) {
	f.suspended = append([]int(nil), ids...)
	p := biz.RuntimeSuspendPlan{}
	for _, id := range ids {
		p.Entries = append(p.Entries, biz.SuspendedForward{ForwardID: id})
	}
	return p, nil
}
func (f *fakeRuntime) SuspendAll(context.Context) (biz.RuntimeSuspendPlan, error) {
	return biz.RuntimeSuspendPlan{}, nil
}
func (f *fakeRuntime) Resume(_ context.Context, p biz.RuntimeSuspendPlan) biz.RuntimeResumeResult {
	f.resumed += len(p.Entries)
	return biz.RuntimeResumeResult{Errors: map[int]string{}}
}
func (f *fakeRuntime) AffectedForHost(int) []biz.AffectedForward  { return f.affected }
func (f *fakeRuntime) LocalListenerOwner(string, int) (int, bool) { return 1, true }
func (f *fakeRuntime) RetireHost(id int)                          { f.retired = id }

type fakeRoutes struct {
	applied    biz.RouteAppliedState
	reconciles int
	result     biz.RouteApplyResult
	err        error
}

func (*fakeRoutes) RouteStatus() ([]biz.RouteStatusItem, error) { return nil, nil }
func (f *fakeRoutes) AppliedState() (biz.RouteAppliedState, error) {
	if f.applied.AppliedDesiredRevision == "" {
		f.applied.AppliedDesiredRevision = "route-v1"
	}
	return f.applied, nil
}
func (*fakeRoutes) PreviewDesired(data model.VaultData, routeID int) (biz.RoutePreview, error) {
	entries, _ := route.PlanHosts(data)
	preview := biz.RoutePreview{HostsRecords: entries}
	for _, candidate := range data.WebRoutes {
		if candidate.ID == routeID && candidate.HostsEnabled && route.NeedsConfirmation(candidate.Domain) {
			preview.RequiresConfirmation = []string{candidate.Domain}
		}
		if candidate.CaddyEnabled {
			preview.CATrustNeeded = true
		}
	}
	return preview, nil
}
func (*fakeRoutes) NeutralizeRoutes(context.Context) error { return nil }
func (f *fakeRoutes) ReconcileRoutes() (biz.RouteApplyResult, error) {
	f.reconciles++
	return f.result, f.err
}

type fakeRestore struct{}

func (fakeRestore) StageRestore(context.Context, biz.RestoreStageRequest) (biz.RestorePreview, error) {
	return biz.RestorePreview{Token: "token"}, nil
}
func (fakeRestore) CommitRestore(context.Context, biz.RestoreCommitRequest) (biz.RestoreCommitResult, error) {
	return biz.RestoreCommitResult{Quarantined: true}, nil
}

type blockingRestore struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingRestore) StageRestore(context.Context, biz.RestoreStageRequest) (biz.RestorePreview, error) {
	return biz.RestorePreview{}, nil
}
func (b *blockingRestore) CommitRestore(context.Context, biz.RestoreCommitRequest) (biz.RestoreCommitResult, error) {
	close(b.entered)
	<-b.release
	return biz.RestoreCommitResult{Quarantined: true}, nil
}

type fakeRecovery struct{}

func (fakeRecovery) State() (bool, bool, error) { return false, false, nil }
func (fakeRecovery) ClearQuarantine() error     { return nil }

func newService(store *memStore, runtime *fakeRuntime) *application.Service {
	return application.NewService(application.Dependencies{Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: runtime, Routes: &fakeRoutes{}, Restore: fakeRestore{}, Recovery: fakeRecovery{}})
}

func TestSnapshotNeverSerializesSSHSecrets(t *testing.T) {
	secret := "known-secret-never-cross-webview"
	store := &memStore{data: model.VaultData{Version: 1, SSHHosts: []model.SSHHost{{ID: 1, Name: "h", Host: "x", Port: 22, User: "u", AuthType: "password", Password: secret}}}}
	snapshot, err := newService(store, &fakeRuntime{}).GetSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snapshot)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "\"password\":") {
		t.Fatalf("snapshot leaked secret: %s", raw)
	}
	if !snapshot.Catalog.SSHHosts[0].HasSecret {
		t.Fatal("snapshot must expose only hasSecret")
	}
}

func TestSaveSSHHostRequiresRestartBeforeChangingRunningConnection(t *testing.T) {
	store := &memStore{data: model.VaultData{Version: 1, SSHHosts: []model.SSHHost{{ID: 1, Name: "h", Host: "old", Port: 22, User: "u", AuthType: "password", Password: "old-secret", CredentialRevision: 1}}}}
	runtime := &fakeRuntime{affected: []biz.AffectedForward{{ForwardID: 7, RunningGeneration: 3}}}
	service := newService(store, runtime)
	cmd := application.SaveSSHHostCommand{Host: application.SSHHostInput{ID: 1, Name: "h", Host: "new", Port: 22, User: "u", AuthType: "password"}, SecretAction: biz.SecretReplace, SecretInput: "new-secret"}
	preview, err := service.SaveSSHHost(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.RequiresRestart || store.data.SSHHosts[0].Host != "old" || runtime.retired != 0 {
		t.Fatalf("preview caused mutation: %+v", preview)
	}

	cmd.ConfirmRestart = true
	result, err := service.SaveSSHHost(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if result.Host.HasSecret != true || store.data.SSHHosts[0].Password != "new-secret" || store.data.SSHHosts[0].CredentialRevision != 2 {
		t.Fatalf("save result=%+v internal=%+v", result, store.data.SSHHosts[0])
	}
	if len(runtime.suspended) != 1 || runtime.suspended[0] != 7 || runtime.retired != 1 || runtime.resumed != 1 {
		t.Fatalf("runtime orchestration = %+v", runtime)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "new-secret") {
		t.Fatalf("result leaked secret: %s", raw)
	}
}

func TestPreviewLocalListenerClassifiesOwnedByEditingForward(t *testing.T) {
	store := &memStore{data: model.VaultData{Version: 1}}
	result := newService(store, &fakeRuntime{}).PreviewLocalListener(context.Background(), application.PreviewLocalListenerCommand{Mode: "local", Host: "127.0.0.1", Port: 1234, ForwardID: 1})
	if result.State != "owned_by_self" || result.OwnerForwardID != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestMaintenanceRejectsStartButNeverBlocksSafeStop(t *testing.T) {
	store := &memStore{data: model.VaultData{Version: 1}}
	runtime := &fakeRuntime{}
	restore := &blockingRestore{entered: make(chan struct{}), release: make(chan struct{})}
	service := application.NewService(application.Dependencies{Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: runtime, Routes: &fakeRoutes{}, Restore: restore, Recovery: fakeRecovery{}})
	done := make(chan error, 1)
	go func() {
		_, err := service.CommitRestore(context.Background(), biz.RestoreCommitRequest{Confirmed: true})
		done <- err
	}()
	<-restore.entered
	startErrors := service.StartForwards(context.Background(), []int{1})
	if !strings.Contains(startErrors[1], "maintenance") || runtime.starts != 0 {
		t.Fatalf("startErrors=%v starts=%d", startErrors, runtime.starts)
	}
	if err := service.StopForward(1); err != nil || runtime.stops != 1 {
		t.Fatalf("safe stop err=%v stops=%d", err, runtime.stops)
	}
	close(restore.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func routeFixture() model.VaultData {
	return model.VaultData{
		Version:   1,
		Folders:   []model.Folder{{ID: 1, Name: "default"}},
		SSHHosts:  []model.SSHHost{{ID: 1, Name: "host", Host: "127.0.0.1", Port: 22, User: "user", AuthType: "ssh_agent"}},
		Forwards:  []model.Forward{{ID: 1, FolderID: 1, Name: "web", Mode: model.ModeLocal, ChainHostIDs: []int{1}, LocalHost: "127.0.0.1", LocalPort: 8080, RemoteHost: "127.0.0.1", RemotePort: 80}},
		WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: "demo.example.com", UpstreamScheme: "http"}},
	}
}

func TestRouteChangeCommitRejectsStaleTokenBeforeSaving(t *testing.T) {
	store := &memStore{data: routeFixture()}
	routes := &fakeRoutes{}
	service := application.NewService(application.Dependencies{Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: &fakeRuntime{}, Routes: routes, Restore: fakeRestore{}, Recovery: fakeRecovery{}})
	snapshot, err := service.GetSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewRouteChange(context.Background(), application.RouteChangeIntent{
		ExpectedRevision: snapshot.Revisions.Vault, Action: application.RouteChangeSetFlag,
		RouteID: 1, Flag: application.RouteFlagHostsEnabled, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Token == "" || preview.DesiredRevision != snapshot.Revisions.Vault || preview.AppliedRevision != "route-v1" {
		t.Fatalf("preview not revision-bound: %+v", preview)
	}
	store.data.Prefs.AutoRun = true // 模拟 Preview 后的并发 Vault mutation。
	result, err := service.CommitRouteChange(context.Background(), application.CommitRouteChangeCommand{Token: preview.Token, ConfirmedDomains: preview.RequiresConfirmation})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != application.RouteOutcomeRejected || result.DesiredSaved || routes.reconciles != 0 {
		t.Fatalf("stale commit result=%+v reconciles=%d", result, routes.reconciles)
	}
	if store.data.WebRoutes[0].HostsEnabled {
		t.Fatal("stale commit changed desired route")
	}
}

func TestRouteChangeKeepsSavedDesiredWhenReconcileFails(t *testing.T) {
	store := &memStore{data: routeFixture()}
	routes := &fakeRoutes{err: errors.New("caddy load failed"), applied: biz.RouteAppliedState{AppliedDesiredRevision: "route-v1", Status: biz.RouteStatusError}}
	service := application.NewService(application.Dependencies{Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: &fakeRuntime{}, Routes: routes, Restore: fakeRestore{}, Recovery: fakeRecovery{}})
	snapshot, _ := service.GetSnapshot(context.Background())
	preview, err := service.PreviewRouteChange(context.Background(), application.RouteChangeIntent{
		ExpectedRevision: snapshot.Revisions.Vault, Action: application.RouteChangeSetFlag,
		RouteID: 1, Flag: application.RouteFlagCaddyEnabled, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.CATrustNeeded {
		t.Fatal("enabling caddy must request current-user CA confirmation")
	}
	result, err := service.CommitRouteChange(context.Background(), application.CommitRouteChangeCommand{Token: preview.Token, ConfirmedDomains: preview.RequiresConfirmation, ConfirmCATrust: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != application.RouteOutcomeSavedNotApplied || !result.DesiredSaved || !result.StateMayHaveChanged {
		t.Fatalf("result=%+v", result)
	}
	if result.Route == nil || !result.Route.HostsEnabled || !result.Route.CaddyEnabled || !store.data.WebRoutes[0].CaddyEnabled {
		t.Fatalf("saved desired route lost: result=%+v stored=%+v", result.Route, store.data.WebRoutes[0])
	}
	if routes.reconciles != 1 || result.AcceptedRevision == snapshot.Revisions.Vault {
		t.Fatalf("reconciles=%d accepted=%q", routes.reconciles, result.AcceptedRevision)
	}
}

func TestRouteChangeRequiresConfirmedDomainBeforeSaving(t *testing.T) {
	store := &memStore{data: routeFixture()}
	routes := &fakeRoutes{}
	service := application.NewService(application.Dependencies{Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: &fakeRuntime{}, Routes: routes, Restore: fakeRestore{}, Recovery: fakeRecovery{}})
	snapshot, _ := service.GetSnapshot(context.Background())
	preview, err := service.PreviewRouteChange(context.Background(), application.RouteChangeIntent{
		ExpectedRevision: snapshot.Revisions.Vault, Action: application.RouteChangeSetFlag,
		RouteID: 1, Flag: application.RouteFlagHostsEnabled, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.RequiresConfirmation) != 1 || preview.RequiresConfirmation[0] != "demo.example.com" {
		t.Fatalf("confirmation preview=%+v", preview)
	}
	rejected, err := service.CommitRouteChange(context.Background(), application.CommitRouteChangeCommand{Token: preview.Token})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Outcome != application.RouteOutcomeRejected || rejected.DesiredSaved || store.data.WebRoutes[0].HostsEnabled || routes.reconciles != 0 {
		t.Fatalf("unconfirmed result=%+v stored=%+v reconciles=%d", rejected, store.data.WebRoutes[0], routes.reconciles)
	}
	result, err := service.CommitRouteChange(context.Background(), application.CommitRouteChangeCommand{Token: preview.Token, ConfirmedDomains: []string{"demo.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != application.RouteOutcomeApplied || !result.DesiredSaved || !store.data.WebRoutes[0].HostsEnabled || routes.reconciles != 1 {
		t.Fatalf("confirmed result=%+v stored=%+v reconciles=%d", result, store.data.WebRoutes[0], routes.reconciles)
	}
}

func TestRouteUpsertEnforcesCaddyHostsInvariantInBackend(t *testing.T) {
	store := &memStore{data: routeFixture()}
	service := application.NewService(application.Dependencies{Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: &fakeRuntime{}, Routes: &fakeRoutes{}, Restore: fakeRestore{}, Recovery: fakeRecovery{}})
	snapshot, _ := service.GetSnapshot(context.Background())
	preview, err := service.PreviewRouteChange(context.Background(), application.RouteChangeIntent{
		ExpectedRevision: snapshot.Revisions.Vault,
		Action:           application.RouteChangeUpsert,
		Route:            &model.WebRoute{ID: 1, ForwardID: 1, Domain: "demo.test", HostsEnabled: false, CaddyEnabled: true, UpstreamScheme: "http"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Route == nil || !preview.Route.HostsEnabled || !preview.Route.CaddyEnabled {
		t.Fatalf("backend did not enforce caddy -> hosts: %+v", preview.Route)
	}
}
