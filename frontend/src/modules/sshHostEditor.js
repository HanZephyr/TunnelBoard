export function createSSHHostDraft(view = {}) {
  return {
    id: Number(view.id) || 0,
    name: String(view.name || ''),
    host: String(view.host || ''),
    port: Number(view.port) || 22,
    user: String(view.user || ''),
    authType: view.authType || 'ssh_key',
    keyPath: String(view.keyPath || ''),
    agentSocketPath: String(view.agentSocketPath || ''),
    keepAliveIntervalMs: Number(view.keepAliveIntervalMs ?? 5000),
    timeoutMs: Number(view.timeoutMs ?? 5000),
    hostKeyAlgorithms: String(view.hostKeyAlgorithms || ''),
    notes: String(view.notes || ''),
    hasSecret: view.hasSecret === true,
    originalAuthType: view.authType || 'ssh_key',
    secretInput: '',
    secretAction: view.hasSecret === true ? 'keep' : 'replace'
  }
}

export function createSSHAuthDrafts(draft) {
  return {
    password: { secretInput: draft.authType === 'password' ? draft.secretInput : '' },
    ssh_key: { keyPath: draft.keyPath, secretInput: draft.authType === 'ssh_key' ? draft.secretInput : '' },
    ssh_agent: { agentSocketPath: draft.agentSocketPath }
  }
}

export function switchSSHHostAuthDraft(draft, drafts, nextType) {
  const currentType = draft.authType
  if (currentType === 'password') drafts.password.secretInput = draft.secretInput
  if (currentType === 'ssh_key') {
    drafts.ssh_key.keyPath = draft.keyPath
    drafts.ssh_key.secretInput = draft.secretInput
  }
  if (currentType === 'ssh_agent') drafts.ssh_agent.agentSocketPath = draft.agentSocketPath

  draft.authType = nextType
  if (nextType === 'password') draft.secretInput = drafts.password.secretInput
  if (nextType === 'ssh_key') {
    draft.keyPath = drafts.ssh_key.keyPath
    draft.secretInput = drafts.ssh_key.secretInput
  }
  if (nextType === 'ssh_agent') draft.agentSocketPath = drafts.ssh_agent.agentSocketPath
}

export function clearSSHHostTransientSecrets(draft) {
  if (draft) draft.secretInput = ''
}

function resolvedSecretAction(draft) {
  if (draft.authType === 'ssh_agent') return 'clear'
  if (draft.hasSecret && draft.authType !== draft.originalAuthType) return draft.secretInput ? 'replace' : 'clear'
  return draft.secretAction
}

export function validateSSHHostDraft(draft) {
  if (!draft.name.trim()) return 'nameRequired'
  if (!draft.host.trim()) return 'hostRequired'
  if (!draft.user.trim()) return 'userRequired'
  const port = Number(draft.port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) return 'portRange'
  if (draft.authType === 'ssh_key' && !draft.keyPath.trim()) return 'keyPathRequired'
  if (draft.authType === 'password' && resolvedSecretAction(draft) === 'replace' && !draft.secretInput) return 'passwordRequired'
  return ''
}

export function toSaveSSHHostCommand(draft) {
  const secretAction = resolvedSecretAction(draft)
  return {
    meta: {},
    host: {
      id: Number(draft.id) || 0,
      name: draft.name.trim(),
      host: draft.host.trim(),
      port: Number(draft.port),
      user: draft.user.trim(),
      authType: draft.authType,
      keyPath: draft.authType === 'ssh_key' ? draft.keyPath.trim() : '',
      agentSocketPath: draft.authType === 'ssh_agent' ? draft.agentSocketPath.trim() : '',
      keepAliveIntervalMs: Number(draft.keepAliveIntervalMs) || 0,
      timeoutMs: Number(draft.timeoutMs) || 0,
      hostKeyAlgorithms: draft.hostKeyAlgorithms.trim(),
      notes: draft.notes.trim()
    },
    secretAction,
    confirmRestart: false,
    ...(secretAction === 'replace' ? { secretInput: draft.secretInput } : {})
  }
}
