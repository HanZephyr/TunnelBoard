import test from 'node:test'
import assert from 'node:assert/strict'
import { createListenerPreview } from './listenerPreview.js'

test('关闭旧会话后丢弃迟到的端口预检', async () => {
  const preview = createListenerPreview()
  let release
  const pending = preview.check({ host: '127.0.0.1', port: 9000 }, () => new Promise((resolve) => { release = resolve }))
  preview.invalidate()
  release({ state: 'occupied' })
  await pending
  assert.equal(preview.state.status, 'idle')
})

test('新 generation 的结果不会被旧结果覆盖', async () => {
  const preview = createListenerPreview()
  let releaseOld
  const old = preview.check({ host: '127.0.0.1', port: 9000 }, () => new Promise((resolve) => { releaseOld = resolve }))
  await preview.check({ host: '127.0.0.1', port: 9001 }, async () => ({ state: 'available' }))
  releaseOld({ state: 'occupied' })
  await old
  assert.equal(preview.state.status, 'available')
  assert.equal(preview.state.port, 9001)
})

test('通信失败进入 unknown 而不伪装成端口冲突', async () => {
  const preview = createListenerPreview()
  await preview.check({ host: '127.0.0.1', port: 9000 }, async () => { throw new Error('backend unavailable') })
  assert.equal(preview.state.status, 'unknown')
  assert.equal(preview.state.message, 'backend unavailable')
})

test('读取后端 LocalListenerPreview 的 state 字段', async () => {
  const preview = createListenerPreview()
  await preview.check(
    { host: '127.0.0.1', port: 33001, forwardId: 7 },
    async () => ({ state: 'owned_by_self', ownerForwardId: 7 })
  )
  assert.equal(preview.state.status, 'owned_by_self')
})
