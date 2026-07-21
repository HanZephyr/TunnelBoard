const VALID = new Set(['available', 'occupied', 'owned_by_self', 'unknown'])

export function createListenerPreview() {
  let generation = 0
  const state = { status: 'idle', host: '', port: 0, message: '' }

  async function check(address, request) {
    const current = ++generation
    state.status = 'checking'
    state.host = address.host
    state.port = address.port
    state.message = ''
    try {
      const result = await request(address)
      if (current !== generation) return false
      state.status = VALID.has(result?.status) ? result.status : 'unknown'
      state.message = String(result?.message || '')
      return true
    } catch (error) {
      if (current !== generation) return false
      state.status = 'unknown'
      state.message = error instanceof Error ? error.message : String(error)
      return true
    }
  }

  return {
    state,
    check,
    invalidate() {
      generation += 1
      Object.assign(state, { status: 'idle', host: '', port: 0, message: '' })
    }
  }
}
