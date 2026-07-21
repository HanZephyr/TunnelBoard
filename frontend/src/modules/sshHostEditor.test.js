import test from 'node:test'
import assert from 'node:assert/strict'
import { createSSHAuthDrafts, createSSHHostDraft, switchSSHHostAuthDraft, toSaveSSHHostCommand, validateSSHHostDraft } from './sshHostEditor.js'

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

test('认证方式 A 到 B 再回 A 保留本次表单输入', () => {
  const draft = createSSHHostDraft({ authType: 'password' })
  draft.secretInput = 'password-draft'
  const drafts = createSSHAuthDrafts(draft)
  switchSSHHostAuthDraft(draft, drafts, 'ssh_key')
  draft.keyPath = '~/.ssh/id_ed25519'
  draft.secretInput = 'key-passphrase'
  switchSSHHostAuthDraft(draft, drafts, 'password')
  assert.equal(draft.secretInput, 'password-draft')
  switchSSHHostAuthDraft(draft, drafts, 'ssh_key')
  assert.equal(draft.keyPath, '~/.ssh/id_ed25519')
  assert.equal(draft.secretInput, 'key-passphrase')
})

test('切换到密码认证必须显式输入新密码', () => {
  const draft = createSSHHostDraft({ name: 'host', host: 'example.test', user: 'ops', authType: 'ssh_key', hasSecret: true, keyPath: '~/.ssh/id_ed25519' })
  draft.authType = 'password'
  assert.equal(validateSSHHostDraft(draft), 'passwordRequired')
  assert.equal(toSaveSSHHostCommand(draft).secretAction, 'replace')
})
