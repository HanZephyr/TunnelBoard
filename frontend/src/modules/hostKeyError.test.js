import test from 'node:test'
import assert from 'node:assert/strict'
import { parseHostKeyError } from './hostKeyError.js'

test('解析 SSH 主机测试返回的未知 host key，以便用户确认', () => {
  assert.deepEqual(
    parseHostKeyError('ssh dial server.example.test:2222 failed: ssh: handshake failed: forward: host key rejected: biz: ssh host key unknown: server.example.test:2222 fingerprint SHA256:new-key'),
    {
      kind: 'unknown',
      host: 'server.example.test',
      port: 2222,
      storedFingerprint: '',
      fingerprint: 'SHA256:new-key'
    }
  )
})

test('解析 SSH 主机测试返回的已变更 host key，以便用户明确替换', () => {
  assert.deepEqual(
    parseHostKeyError('biz: ssh host key mismatch: 2001:db8::1:22 fingerprint changed (stored SHA256:old-key, got SHA256:new-key)'),
    {
      kind: 'mismatch',
      host: '2001:db8::1',
      port: 22,
      storedFingerprint: 'SHA256:old-key',
      fingerprint: 'SHA256:new-key'
    }
  )
})

test('非 host key 错误不提供信任入口', () => {
  assert.equal(parseHostKeyError('ssh: unable to authenticate'), null)
})
