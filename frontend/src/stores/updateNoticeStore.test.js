import test from 'node:test'
import assert from 'node:assert/strict'
import { createUpdateNoticeStore } from './updateNoticeStore.js'

test('失败或跳过不会清除已发现的更新', () => {
  const store = createUpdateNoticeStore()
  store.accept({ status: 'available', latestVersion: '2.0.0', releaseNotes: 'notes' })
  store.accept({ status: 'failed', message: 'offline' })
  assert.equal(store.state.status, 'available')
  assert.equal(store.state.latestVersion, '2.0.0')
})

test('后续成功确认无更新会清除旧提示', () => {
  const store = createUpdateNoticeStore()
  store.accept({ status: 'available', latestVersion: '2.0.0' })
  store.accept({ status: 'up_to_date' })
  assert.equal(store.state.status, 'idle')
  assert.equal(store.state.latestVersion, '')
})
