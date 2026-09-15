import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { recoverRevisionConflict } from './vaultRevision.js'

const t = (key) => key
const conflict = 'application: revision conflict: current=new'

test('版本冲突等待刷新完成，普通错误不刷新', async () => {
  let complete
  let settled = false
  const pending = recoverRevisionConflict(conflict, () => new Promise(resolve => { complete = resolve }), t)
    .then(result => { settled = true; return result })
  await Promise.resolve()
  assert.equal(settled, false)
  complete(true)
  assert.equal(await pending, 'hosts.errors.revisionConflict')
  assert.equal(await recoverRevisionConflict(new Error('authentication failed'), () => assert.fail(), t), 'authentication failed')
})

test('刷新失败不提示直接保存', async () => {
  for (const refresh of [async () => false, async () => { throw new Error('offline') }]) {
    assert.equal(await recoverRevisionConflict(conflict, refresh, t), 'hosts.errors.revisionRefreshFailed')
  }
})

// 执行组件的实际处理函数，以覆盖指纹入库、刷新与重新握手之间的时序。
test('主机首次信任和替换指纹都必须等待刷新，再测试连接', async () => {
  const source = await readFile(new URL('../components/pages/HostsPage.vue', import.meta.url), 'utf8')
  const handler = source.slice(source.indexOf('async function confirmHostKey()'), source.indexOf('\nfunction cancelHostKey()'))
  for (const kind of ['unknown', 'mismatch']) {
    for (const refreshOK of [true, false]) {
      const calls = []
      const pendingHostKey = { value: { kind, host: 'example.test', port: 22, fingerprint: 'test' } }
      const hostKeyBusy = { value: false }
      const hostTest = {}
      const dependencies = {
        pendingHostKey, hostKeyBusy, hostTest, t,
        EnrollHostKey: 'enroll', ReplaceHostKey: 'replace',
        callBackend: async operation => { calls.push(operation) },
        refreshVault: async () => { calls.push('refresh'); return refreshOK },
        testHostConnection: async () => { calls.push('test') },
        errorMessage: String
      }
      const run = new Function(...Object.keys(dependencies), `${handler}; return confirmHostKey()`)
      await run(...Object.values(dependencies))
      assert.deepEqual(calls, [kind === 'mismatch' ? 'replace' : 'enroll', 'refresh', ...(refreshOK ? ['test'] : [])])
      assert.equal(hostKeyBusy.value, false)
      if (!refreshOK) assert.equal(hostTest.message, 'hosts.errors.revisionRefreshFailed')
    }
  }
})

test('启动语言持久化先于首次快照，两个新建入口接入冲突恢复', async () => {
  const app = await readFile(new URL('../App.vue', import.meta.url), 'utf8')
  const startup = app.slice(app.indexOf('onMounted(async () =>'), app.indexOf('onBeforeUnmount(() =>'))
  assert.ok(startup.indexOf('await callBackend(SaveUILocale') < startup.indexOf('await loadVault()'))
  assert.match(app, /provide\(vaultRefreshKey, async \(\) => \{\s*await loadVault\(\)\s*await nextTick\(\)/)
  for (const path of ['pages/HostsPage.vue', 'modals/ForwardModal.vue']) {
    const source = await readFile(new URL(`../components/${path}`, import.meta.url), 'utf8')
    assert.match(source, /await recoverRevisionConflict\(err, refreshVault, t\)/)
  }
})

test('保存冲突保留草稿且不自动重放，用户再次保存携带刷新后的版本', async () => {
  const source = await readFile(new URL('../components/pages/HostsPage.vue', import.meta.url), 'utf8')
  const handler = source.slice(source.indexOf('async function saveHost()'), source.indexOf('\nfunction hostChangeFailure('))
  const props = { configurationLocked: false, vaultRevision: 'old' }
  const hostForm = { name: 'test', secretInput: 'synthetic-secret' }
  const submitted = []
  let closed = 0
  const dependencies = {
    props, hostForm, editingHostId: { value: null },
    toSaveSSHHostCommand: draft => ({ host: { ...draft } }),
    createCommandMeta: expectedRevision => ({ expectedRevision }),
    validationMessage: () => '', validateSSHHostDraft: () => '',
    hostValidationError: { value: '' }, hostSaveBusy: { value: false },
    application: { saveSSHHost: async command => {
      submitted.push(command)
      if (command.meta.expectedRevision !== 'new') throw conflict
      return { host: command.host }
    } },
    refreshVault: async () => { props.vaultRevision = 'new'; return true },
    recoverRevisionConflict, t, emit: () => {},
    finishHostModal: () => { closed++ }
  }
  const run = new Function(...Object.keys(dependencies), `${handler}; return saveHost()`)
  await run(...Object.values(dependencies))
  assert.equal(submitted.length, 1)
  assert.equal(closed, 0)
  assert.equal(hostForm.secretInput, 'synthetic-secret')
  assert.equal(dependencies.hostValidationError.value, 'hosts.errors.revisionConflict')
  await run(...Object.values(dependencies))
  assert.equal(submitted.length, 2)
  assert.equal(submitted[1].meta.expectedRevision, 'new')
  assert.equal(closed, 1)
})
