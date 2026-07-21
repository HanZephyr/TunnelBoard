import test from 'node:test'
import assert from 'node:assert/strict'
import { createSSHHostDraft, toSaveSSHHostCommand, validateSSHHostDraft } from './sshHostEditor.js'

test('编辑无秘密 Host 默认保留已保存秘密', () => {
  const draft = createSSHHostDraft({ id: 7, name: 'prod', host: 'example.test', port: 22, user: 'u', authType: 'password', hasSecret: true })
  const command = toSaveSSHHostCommand(draft)
  assert.equal(command.secretAction, 'keep')
  assert.equal('password' in command, false)
})

test('切换到 agent 清除旧秘密并移除不适用字段', () => {
  const draft = createSSHHostDraft({ id: 7, name: 'prod', host: 'example.test', port: 22, user: 'u', authType: 'password', hasSecret: true })
  draft.authType = 'ssh_agent'
  const command = toSaveSSHHostCommand(draft)
  assert.equal(command.secretAction, 'clear')
  assert.equal(command.host.keyPath, '')
})

test('full 与 compact 使用同一验证规则', () => {
  const draft = createSSHHostDraft({ authType: 'ssh_key' })
  assert.equal(validateSSHHostDraft(draft), 'nameRequired')
})
