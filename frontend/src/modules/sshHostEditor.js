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

export function validateSSHHostDraft(draft) {
  if (!draft.name.trim()) return 'nameRequired'
  if (!draft.host.trim()) return 'hostRequired'
  if (!draft.user.trim()) return 'userRequired'
  const port = Number(draft.port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) return 'portRange'
  if (draft.authType === 'ssh_key' && !draft.keyPath.trim()) return 'keyPathRequired'
  if (draft.authType === 'password' && draft.secretAction === 'replace' && !draft.secretInput) return 'passwordRequired'
  return ''
}

export function toSaveSSHHostCommand(draft) {
  const switchedToAgent = draft.authType === 'ssh_agent' && draft.originalAuthType !== 'ssh_agent' && draft.hasSecret
  const secretAction = switchedToAgent ? 'clear' : draft.secretAction
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
