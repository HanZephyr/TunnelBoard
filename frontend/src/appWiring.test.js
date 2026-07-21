import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('根组件向 RoutesPage 传递 revision 和应用状态', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  const mount = source.match(/<RoutesPage[\s\S]*?\/>/)?.[0] || ''
  assert.match(mount, /:route-statuses="routeStatuses"/)
  assert.match(mount, /:vault-revision="vaultRevision"/)
})

test('根 Snapshot 将后端 SSH Host 默认值传给两个编辑入口', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  const matches = source.match(/:ssh-host-defaults="sshHostDefaults"/g) || []
  assert.equal(matches.length, 2)
  assert.match(source, /raw\?\.sshHostDefaults/)
})

test('Route 确认前隐藏表单弹窗，取消确认时恢复', async () => {
  const source = await readFile(new URL('./components/pages/RoutesPage.vue', import.meta.url), 'utf8')
  assert.match(source, /function openRouteConfirm[\s\S]*routeModalOpen\.value = false/)
  assert.match(source, /function cancelRouteConfirm[\s\S]*routeModalOpen\.value = true/)
})

test('Route 高风险状态不降级为灰色 stopped', async () => {
  const source = await readFile(new URL('./components/pages/RoutesPage.vue', import.meta.url), 'utf8')
  assert.match(source, /conflict: \['error'/)
  assert.match(source, /error: \['error'/)
  assert.match(source, /cleanup_pending: \['reconnecting'/)
  assert.match(source, /quarantined: \['reconnecting'/)
})

test('LogsPage 传递完整 generation cursor 而不是只传 offset', async () => {
  const source = await readFile(new URL('./components/pages/LogsPage.vue', import.meta.url), 'utf8')
  assert.match(source, /GetLogTailV2/)
  assert.match(source, /GetLogTailV2, requestedSource, cursor/)
  assert.doesNotMatch(source, /GetLogTail, requestedSource, offset/)
})

test('ForwardModal 真正接入 listenerPreview 状态机', async () => {
  const source = await readFile(new URL('./components/modals/ForwardModal.vue', import.meta.url), 'utf8')
  assert.match(source, /createListenerPreview\(portPreviewState\)/)
  assert.match(source, /portPreview\.check/)
  assert.doesNotMatch(source, /catch \(_\)[\s\S]{0,160}portConflict/)
})

test('更新偏好失败后保持关闭并提供重试', async () => {
  const source = await readFile(new URL('./components/pages/SettingsPage.vue', import.meta.url), 'utf8')
  assert.match(source, /updatePreferencePhase\.value = 'error'/)
  assert.match(source, /@click="loadUpdatePreference"/)
  assert.match(source, /updatePreferencePhase !== 'ready'/)
})

test('高层命令结果先登记 revision 和 event sequence 再刷新', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  assert.match(source, /function onVaultChanged[\s\S]*acceptRevision\(result\?\.acceptedRevision, result\?\.eventSequence\)[\s\S]*await loadVault/)
	assert.equal((source.match(/@vault-changed="onVaultChanged"/g) || []).length, 4)
	const settingsMount = source.match(/<SettingsPage[\s\S]*?\/>/)?.[0] || ''
	assert.match(settingsMount, /:vault-revision="vaultRevision"/)
})

test('导入提交携带幂等命令 ID 与当前 Vault revision', async () => {
	const source = await readFile(new URL('./components/pages/SettingsPage.vue', import.meta.url), 'utf8')
	assert.match(source, /CommitImportCommand, \{ meta: createCommandMeta\(props\.vaultRevision\)/)
	assert.match(source, /emit\('vault-changed', result\)/)
})
