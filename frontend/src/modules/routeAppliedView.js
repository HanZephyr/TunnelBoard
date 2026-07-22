export function routeAppliedView(route, status, t) {
  if (!status) return { status: 'reconnecting', label: t('routes.status.unknown') }
  const state = status.state || status.status

  // 后端的 applied 是整套 Route 配置已协调完成，不能据此断言每一条 Route 都启用了 Caddy。
  if (state === 'applied') {
    if (route.caddyEnabled && status.caddyRunning) return { status: 'running', label: t('routes.status.applied') }
    if (route.hostsEnabled && status.hostsApplied) return { status: 'running', label: t('routes.status.hostsOnly') }
    if (!route.hostsEnabled && !route.caddyEnabled) return { status: 'stopped', label: t('routes.status.notDesired') }
  }

  const states = {
    hosts_only: ['running', 'routes.status.hostsOnly'],
    pending: ['reconnecting', 'routes.status.pending'],
    conflict: ['error', 'routes.status.portConflict'],
    error: ['error', 'routes.status.error'],
    unknown: ['reconnecting', 'routes.status.unknown'],
    cleanup_pending: ['reconnecting', 'routes.status.cleanupPending'],
    quarantined: ['reconnecting', 'routes.status.quarantined']
  }
  if (states[state]) return { status: states[state][0], label: t(states[state][1]) }
  if (status.portConflict) return { status: 'error', label: t('routes.status.portConflict') }
  if (route.caddyEnabled && status.caddyRunning) return { status: 'running', label: t('routes.status.applied') }
  if (route.hostsEnabled && status.hostsApplied) return { status: 'running', label: t('routes.status.hostsOnly') }
  if (!route.hostsEnabled && !route.caddyEnabled) return { status: 'stopped', label: t('routes.status.notDesired') }
  return { status: 'reconnecting', label: t('routes.status.unknown') }
}
