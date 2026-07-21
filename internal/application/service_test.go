package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/application"
	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

type memStore struct {
	data    model.VaultData
	updates int
}

func (m *memStore) Load() (model.VaultData, error) { return m.data, nil }
func (m *memStore) Update(fn func(*model.VaultData) error) (model.VaultData, error) {
	d := m.data
	if err := fn(&d); err != nil {
		return model.VaultData{}, err
	}
	m.updates++
	m.data = d
	return d, nil
}

type fakeRuntime struct {
	affected     []biz.AffectedForward
	preflight    map[int]string
	operations   []string
	suspendErr   error
	resumeErrors map[int]string
	suspended    []int
	retired      int
	resumed      int
	starts       int
	stops        int
}

func (f *fakeRuntime) Snapshot() ([]biz.RuntimeStatus, error) {
	return []biz.RuntimeStatus{{ForwardID: 1, Status: biz.RuntimeStateRunning}}, nil
}
func (f *fakeRuntime) Start(int) error                        { f.starts++; return nil }
func (f *fakeRuntime) Stop(int) error                         { f.stops++; return nil }
func (f *fakeRuntime) StartAutoStart() (map[int]error, error) { return nil, nil }
func (f *fakeRuntime) Suspend(_ context.Context, ids []int) (biz.RuntimeSuspendPlan, error) {
	f.operations = append(f.operations, "suspend")
	f.suspended = append([]int(nil), ids...)
	p := biz.RuntimeSuspendPlan{}
	for _, id := range ids {
		p.Entries = append(p.Entries, biz.SuspendedForward{ForwardID: id})
	}
	return p, f.suspendErr
}
func (f *fakeRuntime) SuspendAll(context.Context) (biz.RuntimeSuspendPlan, error) {
	return biz.RuntimeSuspendPlan{}, nil
}
func (f *fakeRuntime) Resume(_ context.Context, p biz.RuntimeSuspendPlan) biz.RuntimeResumeResult {
	f.operations = append(f.operations, "resume")
	f.resumed += len(p.Entries)
	result := biz.RuntimeResumeResult{Errors: map[int]string{}}
	for _, entry := range p.Entries {
		if message := f.resumeErrors[entry.ForwardID]; message != "" {
			result.Errors[entry.ForwardID] = message
		} else {
			result.Started = append(result.Started, entry.ForwardID)
		}
	}
	return result
}
func (f *fakeRuntime) AffectedForHost(int) []biz.AffectedForward  { return f.affected }
func (f *fakeRuntime) LocalListenerOwner(string, int) (int, bool) { return 1, true }
func (f *fakeRuntime) RetireHost(id int) {
	f.operations = append(f.operations, "retire")
	f.retired = id
}
func (f *fakeRuntime) PreflightHostChange(_ context.Context, _ model.SSHHost, _ []biz.AffectedForward) map[int]string {
	f.operations = append(f.operations, "preflight")
	return f.preflight
}

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
	cmd.PreviewToken = preview.PreviewToken
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

func TestSaveSSHHostCommandIDReturnsCachedResultWithoutRepeatingMutation(t *testing.T) {
	store := &memStore{data: model.VaultData{Version: 1}}
	service := newService(store, &fakeRuntime{})
	cmd := application.SaveSSHHostCommand{
		Meta:         application.CommandMeta{CommandID: "save-host-1"},
		Host:         application.SSHHostInput{Name: "db", Host: "10.0.0.1", Port: 22, User: "ops", AuthType: "password"},
		SecretAction: biz.SecretReplace,
		SecretInput:  "secret",
	}

	first, err := service.SaveSSHHost(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SaveSSHHost(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("cached result mismatch: first=%+v second=%+v", first, second)
	}
	if store.updates != 1 || len(store.data.SSHHosts) != 1 {
		t.Fatalf("duplicate command repeated mutation: updates=%d hosts=%d", store.updates, len(store.data.SSHHosts))
	}

	cmd.Host.Host = "10.0.0.2"
	if _, err := service.SaveSSHHost(context.Background(), cmd); !errors.Is(err, application.ErrCommandIDConflict) {
		t.Fatalf("same id with different payload error = %v", err)
	}
	if store.updates != 1 || store.data.SSHHosts[0].Host != "10.0.0.1" {
		t.Fatalf("conflicting command mutated data: updates=%d host=%s", store.updates, store.data.SSHHosts[0].Host)
	}
}

func TestCommandResultCacheExpiresAndEvictsOldestEntry(t *testing.T) {
	newCachedService := func(options application.CommandCacheOptions) (*application.Service, *memStore) {
		store := &memStore{data: model.VaultData{Version: 1, SSHHosts: []model.SSHHost{
			{ID: 1, Name: "one", Host: "10.0.0.1", Port: 22, User: "ops", AuthType: "password", Password: "secret", TimeoutMs: 5000},
			{ID: 2, Name: "two", Host: "10.0.0.2", Port: 22, User: "ops", AuthType: "password", Password: "secret", TimeoutMs: 5000},
		}}}
		service := application.NewService(application.Dependencies{
			Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: &fakeRuntime{}, Routes: &fakeRoutes{}, Restore: fakeRestore{}, Recovery: fakeRecovery{}, CommandCache: options,
		})
		return service, store
	}
	command := func(id string, hostID int) application.SaveSSHHostCommand {
		return application.SaveSSHHostCommand{
			Meta:         application.CommandMeta{CommandID: id},
			Host:         application.SSHHostInput{ID: hostID, Name: map[int]string{1: "one", 2: "two"}[hostID], Host: map[int]string{1: "10.0.0.1", 2: "10.0.0.2"}[hostID], Port: 22, User: "ops", AuthType: "password", Notes: "updated"},
			SecretAction: biz.SecretKeep,
		}
	}

	t.Run("ttl", func(t *testing.T) {
		service, store := newCachedService(application.CommandCacheOptions{TTL: time.Millisecond, Capacity: 10})
		first, err := service.SaveSSHHost(context.Background(), command("ttl-1", 1))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		second, err := service.SaveSSHHost(context.Background(), command("ttl-1", 1))
		if err != nil {
			t.Fatal(err)
		}
		if store.updates != 2 || second.EventSequence == first.EventSequence {
			t.Fatalf("expired result was retained: updates=%d first=%d second=%d", store.updates, first.EventSequence, second.EventSequence)
		}
	})

	t.Run("capacity", func(t *testing.T) {
		service, store := newCachedService(application.CommandCacheOptions{TTL: time.Hour, Capacity: 1})
		if _, err := service.SaveSSHHost(context.Background(), command("capacity-1", 1)); err != nil {
			t.Fatal(err)
		}
		if _, err := service.SaveSSHHost(context.Background(), command("capacity-2", 2)); err != nil {
			t.Fatal(err)
		}
		if _, err := service.SaveSSHHost(context.Background(), command("capacity-1", 1)); err != nil {
			t.Fatal(err)
		}
		if store.updates != 3 {
			t.Fatalf("oldest result was not evicted: updates=%d", store.updates)
		}
	})
}

func TestMoveForwardsCommandIDCachesChangedAndUnchangedResult(t *testing.T) {
	store := &memStore{data: model.VaultData{
		Version:  1,
		Folders:  []model.Folder{{ID: 1, Name: "source"}, {ID: 2, Name: "target"}},
		SSHHosts: []model.SSHHost{{ID: 1, Name: "host", Host: "10.0.0.1", Port: 22, User: "ops", AuthType: "password", Password: "secret"}},
		Forwards: []model.Forward{
			{ID: 10, Name: "move", FolderID: 1, Mode: "local", ChainHostIDs: []int{1}, LocalHost: "127.0.0.1", LocalPort: 5010, RemoteHost: "db", RemotePort: 5432},
			{ID: 20, Name: "stay", FolderID: 2, Mode: "local", ChainHostIDs: []int{1}, LocalHost: "127.0.0.1", LocalPort: 5020, RemoteHost: "db", RemotePort: 5432},
		},
	}}
	service := newService(store, &fakeRuntime{})
	cmd := application.MoveForwardsCommand{Meta: application.CommandMeta{CommandID: "move-1"}, ForwardIDs: []int{10, 20}, TargetFolderID: 2}

	first, err := service.MoveForwards(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.MoveForwards(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("cached result mismatch: first=%+v second=%+v", first, second)
	}
	if !reflect.DeepEqual(first.ChangedIDs, []int{10}) || !reflect.DeepEqual(first.UnchangedIDs, []int{20}) {
		t.Fatalf("classification = %+v", first)
	}
	if store.updates != 1 {
		t.Fatalf("duplicate command repeated mutation: updates=%d", store.updates)
	}

	cmd.TargetFolderID = 1
	if _, err := service.MoveForwards(context.Background(), cmd); !errors.Is(err, application.ErrCommandIDConflict) {
		t.Fatalf("same id with different payload error = %v", err)
	}
}

func TestMoveForwardsAllUnchangedDoesNotWriteOrEmitMutation(t *testing.T) {
	store := &memStore{data: model.VaultData{
		Version:  1,
		Folders:  []model.Folder{{ID: 2, Name: "target"}},
		SSHHosts: []model.SSHHost{{ID: 1, Name: "host", Host: "10.0.0.1", Port: 22, User: "ops", AuthType: "password", Password: "secret"}},
		Forwards: []model.Forward{{ID: 10, Name: "stay", FolderID: 2, Mode: "local", ChainHostIDs: []int{1}, LocalHost: "127.0.0.1", LocalPort: 5010, RemoteHost: "db", RemotePort: 5432}},
	}}
	service := newService(store, &fakeRuntime{})
	before, err := service.GetSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.MoveForwards(context.Background(), application.MoveForwardsCommand{Meta: application.CommandMeta{CommandID: "noop-move"}, ForwardIDs: []int{10}, TargetFolderID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChangedIDs) != 0 || !reflect.DeepEqual(result.UnchangedIDs, []int{10}) {
		t.Fatalf("result = %+v", result)
	}
	if store.updates != 0 || result.EventSequence != before.EventSequence || result.AcceptedRevision != before.Revisions.Vault {
		t.Fatalf("no-op produced mutation: updates=%d result=%+v before=%+v", store.updates, result, before)
	}
}

func TestCommitSSHHostChangePreflightFailureHasZeroSideEffectsAndConsumesToken(t *testing.T) {
	store := &memStore{data: model.VaultData{Version: 1, SSHHosts: []model.SSHHost{{ID: 1, Name: "h", Host: "old", Port: 22, User: "u", AuthType: "password", Password: "old-secret", CredentialRevision: 1}}}}
	runtime := &fakeRuntime{affected: []biz.AffectedForward{{ForwardID: 7, RunningGeneration: 3}}, preflight: map[int]string{7: "authentication failed for new-secret"}}
	service := newService(store, runtime)
	preview, err := service.PreviewSSHHostChange(context.Background(), application.SaveSSHHostCommand{
		Host:         application.SSHHostInput{ID: 1, Name: "h", Host: "new", Port: 22, User: "u", AuthType: "password"},
		SecretAction: biz.SecretReplace, SecretInput: "new-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Token == "" || preview.ExpiresAt.IsZero() || len(preview.AffectedForwards) != 1 || preview.AffectedForwards[0].RunningGeneration != 3 {
		t.Fatalf("preview = %+v", preview)
	}
	raw, _ := json.Marshal(preview)
	if strings.Contains(string(raw), "new-secret") || strings.Contains(preview.Token, "new-secret") {
		t.Fatalf("preview leaked secret: %s", raw)
	}
	result, err := service.CommitSSHHostChange(context.Background(), application.CommitSSHHostChangeCommand{Token: preview.Token})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed || result.FailureStage != "preflight" || result.PreflightErrors[7] == "" {
		t.Fatalf("result = %+v", result)
	}
	raw, _ = json.Marshal(result)
	if strings.Contains(string(raw), "new-secret") {
		t.Fatalf("commit result leaked secret: %s", raw)
	}
	if store.data.SSHHosts[0].Host != "old" || runtime.retired != 0 || len(runtime.suspended) != 0 || strings.Join(runtime.operations, ",") != "preflight" {
		t.Fatalf("preflight failure caused side effects: store=%+v runtime=%+v", store.data.SSHHosts[0], runtime)
	}
	_, err = service.CommitSSHHostChange(context.Background(), application.CommitSSHHostChangeCommand{Token: preview.Token})
	if !errors.Is(err, application.ErrSSHHostChangeToken) {
		t.Fatalf("second commit err = %v", err)
	}
}

func TestCommitSSHHostChangeRejectsStaleRuntimeGenerationBeforePreflight(t *testing.T) {
	store := &memStore{data: model.VaultData{Version: 1, SSHHosts: []model.SSHHost{{ID: 1, Name: "h", Host: "old", Port: 22, User: "u", AuthType: "password", Password: "old-secret", CredentialRevision: 1}}}}
	runtime := &fakeRuntime{affected: []biz.AffectedForward{{ForwardID: 7, RunningGeneration: 3}}}
	service := newService(store, runtime)
	preview, err := service.PreviewSSHHostChange(context.Background(), application.SaveSSHHostCommand{
		Host:         application.SSHHostInput{ID: 1, Name: "h", Host: "new", Port: 22, User: "u", AuthType: "password"},
		SecretAction: biz.SecretReplace, SecretInput: "new-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.affected[0].RunningGeneration = 4
	_, err = service.CommitSSHHostChange(context.Background(), application.CommitSSHHostChangeCommand{Token: preview.Token})
	if !errors.Is(err, application.ErrSSHHostChangeStale) {
		t.Fatalf("commit err = %v", err)
	}
	if len(runtime.operations) != 0 || store.data.SSHHosts[0].Host != "old" {
		t.Fatalf("stale commit caused side effects: operations=%v host=%s", runtime.operations, store.data.SSHHosts[0].Host)
	}
}

func TestCommitSSHHostChangePreflightsBeforeSuspendAndRestartsCapturedSet(t *testing.T) {
	store := &memStore{data: model.VaultData{Version: 1, SSHHosts: []model.SSHHost{{ID: 1, Name: "h", Host: "old", Port: 22, User: "u", AuthType: "password", Password: "old-secret", CredentialRevision: 1}}}}
	runtime := &fakeRuntime{affected: []biz.AffectedForward{{ForwardID: 7, RunningGeneration: 3}}}
	service := newService(store, runtime)
	preview, err := service.PreviewSSHHostChange(context.Background(), application.SaveSSHHostCommand{
		Host:         application.SSHHostInput{ID: 1, Name: "h", Host: "new", Port: 22, User: "u", AuthType: "password"},
		SecretAction: biz.SecretReplace, SecretInput: "new-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.CommitSSHHostChange(context.Background(), application.CommitSSHHostChangeCommand{Token: preview.Token})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed || store.data.SSHHosts[0].Host != "new" || store.data.SSHHosts[0].Password != "new-secret" {
		t.Fatalf("result=%+v host=%+v", result, store.data.SSHHosts[0])
	}
	if got := strings.Join(runtime.operations, ","); got != "preflight,suspend,retire,resume" {
		t.Fatalf("operation order = %s", got)
	}
	if len(result.ForwardResults) != 1 || result.ForwardResults[0].ForwardID != 7 || result.ForwardResults[0].Status != "restarted" || result.ForwardResults[0].PreviousGeneration != 3 {
		t.Fatalf("forward results = %+v", result.ForwardResults)
	}
}

func TestCommitSSHHostChangeReturnsPerForwardCompensationFailure(t *testing.T) {
	store := &memStore{data: model.VaultData{Version: 1, SSHHosts: []model.SSHHost{{ID: 1, Name: "h", Host: "old", Port: 22, User: "u", AuthType: "password", Password: "old-secret", CredentialRevision: 1}}}}
	runtime := &fakeRuntime{
		affected:     []biz.AffectedForward{{ForwardID: 7, RunningGeneration: 3}},
		suspendErr:   errors.New("stop deadline exceeded"),
		resumeErrors: map[int]string{7: "old generation could not be restored"},
	}
	service := newService(store, runtime)
	preview, err := service.PreviewSSHHostChange(context.Background(), application.SaveSSHHostCommand{
		Host:         application.SSHHostInput{ID: 1, Name: "h", Host: "new", Port: 22, User: "u", AuthType: "password"},
		SecretAction: biz.SecretReplace, SecretInput: "new-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.CommitSSHHostChange(context.Background(), application.CommitSSHHostChangeCommand{Token: preview.Token})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed || result.FailureStage != "stop" || !strings.Contains(result.OperationError, "deadline") {
		t.Fatalf("result = %+v", result)
	}
	if len(result.ForwardResults) != 1 || result.ForwardResults[0].Status != "compensation_failed" || result.ForwardResults[0].CompensationError == "" {
		t.Fatalf("forward result = %+v", result.ForwardResults)
	}
	if store.data.SSHHosts[0].Host != "old" || runtime.retired != 0 {
		t.Fatalf("failed stop mutated host: %+v runtime=%+v", store.data.SSHHosts[0], runtime)
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
