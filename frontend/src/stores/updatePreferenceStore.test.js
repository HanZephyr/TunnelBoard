import test from 'node:test'
import assert from 'node:assert/strict'
import { createUpdatePreferenceStore } from './updatePreferenceStore.js'

test('偏好读取前和失败后都 fail closed', async () => {
  const store = createUpdatePreferenceStore()
  assert.equal(store.view.checked, false)
  assert.equal(store.shouldAutoCheck(), false)
  await store.load(async () => { throw new Error('unavailable') })
  assert.equal(store.view.phase, 'error')
  assert.equal(store.view.checked, false)
  assert.equal(store.shouldAutoCheck(), false)
})

test('仅成功读取 true 时允许启动自动检查', async () => {
  const store = createUpdatePreferenceStore()
  await store.load(async () => true)
  assert.equal(store.view.phase, 'ready')
  assert.equal(store.view.checked, true)
  assert.equal(store.shouldAutoCheck(), true)
})
