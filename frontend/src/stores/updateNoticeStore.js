export function createUpdateNoticeStore() {
  const state = { status: 'idle', latestVersion: '', releaseNotes: '', releasePageUrl: '', message: '' }
  return {
    state,
    accept(outcome = {}) {
      if (state.status === 'available' && outcome.status !== 'available') return
      state.status = outcome.status || 'idle'
      state.latestVersion = String(outcome.latestVersion || '')
      state.releaseNotes = String(outcome.releaseNotes || '')
      state.releasePageUrl = String(outcome.releasePageUrl || '')
      state.message = String(outcome.message || '')
    }
  }
}
