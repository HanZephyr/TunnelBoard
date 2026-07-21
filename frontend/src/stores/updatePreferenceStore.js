export function createUpdatePreferenceStore() {
  let generation = 0
  const view = { phase: 'loading', checked: false, error: '' }

  async function load(reader) {
    const request = ++generation
    view.phase = 'loading'
    view.checked = false
    view.error = ''
    try {
      const enabled = await reader()
      if (request !== generation) return false
      view.phase = 'ready'
      view.checked = enabled === true
      return true
    } catch (error) {
      if (request !== generation) return false
      view.phase = 'error'
      view.checked = false
      view.error = error instanceof Error ? error.message : String(error)
      return false
    }
  }

  return {
    view,
    load,
    setReady(enabled) {
      generation += 1
      view.phase = 'ready'
      view.checked = enabled === true
      view.error = ''
    },
    shouldAutoCheck() {
      return view.phase === 'ready' && view.checked === true
    }
  }
}
