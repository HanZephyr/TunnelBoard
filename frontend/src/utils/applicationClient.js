function runtimeBindings() {
  return globalThis.window?.go?.main?.App || {}
}

export function createCommandMeta(expectedRevision = '') {
  const commandId = globalThis.crypto?.randomUUID?.() || `cmd-${Date.now()}-${Math.random().toString(16).slice(2)}`
  return { commandId, ...(expectedRevision ? { expectedRevision } : {}) }
}

export function createApplicationClient(bindings = runtimeBindings()) {
  const invoke = (name, ...args) => {
    const binding = bindings[name]
    if (typeof binding !== 'function') throw new Error(`application binding ${name} is unavailable`)
    return binding(...args)
  }
  return {
    async getSnapshot() {
      return invoke('GetSnapshot')
    },
    async saveSSHHost(command) {
      return invoke('SaveSSHHostCommand', command)
    },
    async commitSSHHostChange(command) {
      return invoke('CommitSSHHostChange', command)
    },
    async moveForwards(command) {
      return invoke('MoveForwardsCommand', command)
    },
    async previewLocalListener(command) {
      return invoke('PreviewLocalListenerCommand', command)
    }
  }
}
