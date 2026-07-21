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
