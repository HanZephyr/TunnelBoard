import test from 'node:test'
import assert from 'node:assert/strict'
import { createAppSnapshotStore } from './appSnapshotStore.js'

test('首次读取失败显示错误且不伪造空快照', async () => {
  const store = createAppSnapshotStore()
  await store.refresh(async () => { throw new Error('vault unavailable') })
  assert.equal(store.state.phase, 'error')
  assert.equal(store.state.snapshot, null)
  assert.equal(store.canMutate(), false)
})

test('刷新失败保留上次快照并进入 stale', async () => {
  const store = createAppSnapshotStore()
  await store.refresh(async () => ({ vaultRevision: 'vault-3', eventSequence: 3, folders: [{ id: 1 }], sshHosts: [], forwards: [], webRoutes: [] }))
  await store.refresh(async () => { throw new Error('temporary failure') })
  assert.equal(store.state.phase, 'stale')
  assert.equal(store.state.snapshot.folders[0].id, 1)
  assert.equal(store.canMutate(), false)
})

test('迟到读取不能覆盖更新快照', async () => {
  const store = createAppSnapshotStore()
  let release
  const slow = new Promise((resolve) => { release = resolve })
  const first = store.refresh(() => slow)
  await store.refresh(async () => ({ vaultRevision: 'vault-2', eventSequence: 2, folders: [], sshHosts: [], forwards: [], webRoutes: [] }))
  release({ vaultRevision: 'vault-1', eventSequence: 1, folders: [{ id: 9 }], sshHosts: [], forwards: [], webRoutes: [] })
  await first
  assert.equal(store.state.snapshot.vaultRevision, 'vault-2')
})

test('Vault revision 保持为不透明哈希并按事件序号拒绝旧快照', async () => {
  const store = createAppSnapshotStore()
  store.acceptRevision('hash-new', 8)
  await store.refresh(async () => ({ vaultRevision: 'hash-old', eventSequence: 7 }))
  assert.equal(store.state.phase, 'stale')
  assert.equal(store.state.snapshot, null)
})

test('Snapshot 保留后端 SSH Host 安全默认值', async () => {
  const store = createAppSnapshotStore()
  await store.refresh(async () => ({ sshHostDefaults: { port: 2202, timeoutMs: 9000 } }))
  assert.deepEqual(store.state.snapshot.sshHostDefaults, { port: 2202, timeoutMs: 9000 })
})

test('Snapshot 保留运行时、恢复门禁和能力事实', async () => {
  const store = createAppSnapshotStore()
  await store.refresh(async () => ({
    runtimeStatuses: [{ forwardId: 7, status: 'running' }],
    recovery: { quarantined: false, journalPending: true, maintenance: false },
    capabilities: { mutationAllowed: false }
  }))
  assert.deepEqual(store.state.snapshot.runtimeStatuses, [{ forwardId: 7, status: 'running' }])
  assert.equal(store.state.snapshot.recovery.journalPending, true)
  assert.equal(store.state.snapshot.capabilities.mutationAllowed, false)
  assert.equal(store.canMutate(), false)
})
