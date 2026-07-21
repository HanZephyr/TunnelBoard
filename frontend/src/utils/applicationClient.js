function runtimeBindings() {
  return globalThis.window?.go?.main?.App || {}
}

export function createApplicationClient(bindings = runtimeBindings()) {
  return {
    async getSnapshot(legacyGetVault) {
      if (typeof bindings.GetSnapshot === 'function') return bindings.GetSnapshot()
      return legacyGetVault()
    },
    async saveSSHHost(command, legacySave, original = {}) {
      if (typeof bindings.SaveSSHHostCommand === 'function') return bindings.SaveSSHHostCommand(command)
      const password = command.secretAction === 'replace' ? command.secretInput : command.secretAction === 'keep' ? String(original.password || '') : ''
      return legacySave({ ...command.host, password })
    },
    async moveForwards(command, legacyMove) {
      if (typeof bindings.MoveForwardsCommand === 'function') return bindings.MoveForwardsCommand(command)
      return legacyMove(command.forwardIds, command.targetFolderId)
    },
    async previewLocalListener(command, legacyCheck) {
      if (typeof bindings.PreviewLocalListenerCommand === 'function') return bindings.PreviewLocalListenerCommand(command)
      try {
        await legacyCheck(command.host, command.port)
        return { status: 'available', host: command.host, port: command.port }
      } catch (error) {
        return { status: 'occupied', host: command.host, port: command.port, message: error instanceof Error ? error.message : String(error) }
      }
    }
  }
}
