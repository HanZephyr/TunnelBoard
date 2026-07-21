import test from 'node:test'
import assert from 'node:assert/strict'
import { createApplicationClient, createCommandMeta } from './applicationClient.js'

test('命令元数据包含唯一 ID 和当前 Vault revision', () => {
  const first = createCommandMeta('vault-a')
  const second = createCommandMeta('vault-a')
  assert.equal(first.expectedRevision, 'vault-a')
  assert.ok(first.commandId)
  assert.notEqual(first.commandId, second.commandId)
})

test('SSH Host 只调用不含持久秘密响应的高层命令', async () => {
  let modern
  const client = createApplicationClient({ SaveSSHHostCommand: async (command) => { modern = command; return { host: command.host } } })
  const command = { host: { id: 1 }, secretAction: 'keep' }
  await client.saveSSHHost(command)
  assert.equal(modern, command)
})

test('缺少高层绑定时失败关闭，不回退到旧秘密契约', async () => {
  const client = createApplicationClient({})
  await assert.rejects(client.getSnapshot(), /GetSnapshot is unavailable/)
  await assert.rejects(client.saveSSHHost({ host: {} }), /SaveSSHHostCommand is unavailable/)
})

test('SSH Host 变更确认只提交一次性 token', async () => {
  let received
  const client = createApplicationClient({ CommitSSHHostChange: async (command) => { received = command; return { committed: true } } })
  const command = { meta: createCommandMeta('vault-a'), token: 'preview-token' }
  await client.commitSSHHostChange(command)
  assert.deepEqual(received, command)
})

test('Route 变更只调用绑定 revision 的高层命令', async () => {
  const calls = []
  const client = createApplicationClient({
    PreviewRouteChange: async (intent) => { calls.push(['preview', intent]); return { token: 'route-token' } },
    CommitRouteChange: async (command) => { calls.push(['commit', command]); return { outcome: 'applied' } }
  })
  const intent = { action: 'set_flag', routeId: 7, flag: 'hostsEnabled', enabled: true, expectedRevision: 'vault-v1' }
  assert.equal((await client.previewRouteChange(intent)).token, 'route-token')
  const command = { meta: createCommandMeta('vault-v1'), token: 'route-token', confirmedDomains: ['demo.example.com'] }
  assert.equal((await client.commitRouteChange(command)).outcome, 'applied')
  assert.deepEqual(calls, [
    ['preview', intent],
    ['commit', command]
  ])
})
