function normalizeSnapshot(input) {
  return {
    ...input,
    vaultRevision: String(input?.vaultRevision ?? input?.revision ?? ''),
    eventSequence: Number(input?.eventSequence ?? 0),
    folders: Array.isArray(input?.folders) ? input.folders : [],
    sshHosts: Array.isArray(input?.sshHosts) ? input.sshHosts : [],
    forwards: Array.isArray(input?.forwards) ? input.forwards : [],
    webRoutes: Array.isArray(input?.webRoutes) ? input.webRoutes : [],
    routeStatuses: Array.isArray(input?.routeStatuses) ? input.routeStatuses : []
  }
}

export function createAppSnapshotStore() {
  let generation = 0
  const state = {
    phase: 'idle',
    snapshot: null,
    error: '',
    acceptedRevision: '',
    acceptedEventSequence: 0
  }

  async function refresh(load) {
    const request = ++generation
    state.phase = state.snapshot ? 'refreshing' : 'loading'
    state.error = ''
    try {
      const next = normalizeSnapshot(await load())
      if (request !== generation) return false
      if (next.eventSequence > 0 && next.eventSequence < state.acceptedEventSequence) {
        state.phase = 'stale'
        state.error = 'snapshot revision is older than an accepted command'
        return false
      }
      state.snapshot = next
      state.phase = 'ready'
      return true
    } catch (error) {
      if (request !== generation) return false
      state.phase = state.snapshot ? 'stale' : 'error'
      state.error = error instanceof Error ? error.message : String(error)
      return false
    }
  }

  return {
    state,
    refresh,
    acceptRevision(revision, eventSequence = 0) {
      state.acceptedRevision = String(revision || '')
      state.acceptedEventSequence = Math.max(state.acceptedEventSequence, Number(eventSequence) || 0)
    },
    canMutate() {
      return state.phase === 'ready'
    }
  }
}
