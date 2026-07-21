import test from 'node:test'
import assert from 'node:assert/strict'
import { createApplicationClient } from './applicationClient.js'

test('优先调用高层 SaveSSHHostCommand，旧绑定只作兼容回退', async () => {
  let modern
  const client = createApplicationClient({ SaveSSHHostCommand: async (command) => { modern = command; return { host: command.host } } })
  const command = { host: { id: 1 }, secretAction: 'keep' }
  await client.saveSSHHost(command, async () => { throw new Error('legacy must not run') })
  assert.equal(modern, command)
})

test('结构化监听预检在旧后端映射为 available/occupied', async () => {
  const client = createApplicationClient({})
  assert.equal((await client.previewLocalListener({ host: '127.0.0.1', port: 80 }, async () => {})).status, 'available')
  assert.equal((await client.previewLocalListener({ host: '127.0.0.1', port: 80 }, async () => { throw new Error('used') })).status, 'occupied')
})
