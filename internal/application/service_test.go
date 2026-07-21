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

type fakeRoutes struct{}

func (fakeRoutes) RouteStatus() ([]biz.RouteStatusItem, error) { return nil, nil }
func (fakeRoutes) AppliedState() (biz.RouteAppliedState, error) {
	return biz.RouteAppliedState{AppliedDesiredRevision: "route-v1"}, nil
}
func (fakeRoutes) NeutralizeRoutes(context.Context) error         { return nil }
func (fakeRoutes) ReconcileRoutes() (biz.RouteApplyResult, error) { return biz.RouteApplyResult{}, nil }

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
	return application.NewService(application.Dependencies{Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: runtime, Routes: fakeRoutes{}, Restore: fakeRestore{}, Recovery: fakeRecovery{}})
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
			{ID: 1, Name: "one", Host: "10.0.0.1", Port: 22, User: "ops", AuthType: "password", Password: "secret"},
			{ID: 2, Name: "two", Host: "10.0.0.2", Port: 22, User: "ops", AuthType: "password", Password: "secret"},
		}}}
		service := application.NewService(application.Dependencies{
			Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: &fakeRuntime{}, Routes: fakeRoutes{}, Restore: fakeRestore{}, Recovery: fakeRecovery{}, CommandCache: options,
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
	service := application.NewService(application.Dependencies{Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: runtime, Routes: fakeRoutes{}, Restore: restore, Recovery: fakeRecovery{}})
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
