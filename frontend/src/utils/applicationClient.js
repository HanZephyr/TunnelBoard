function runtimeBindings() {
  return globalThis.window?.go?.main?.App || {}
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
    async moveForwards(command) {
      return invoke('MoveForwardsCommand', command)
    },
    async previewLocalListener(command) {
      return invoke('PreviewLocalListenerCommand', command)
    }
  }
}
