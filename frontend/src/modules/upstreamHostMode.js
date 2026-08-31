export const UPSTREAM_HOST_MODES = Object.freeze({
  ORIGINAL: 'original',
  TLS_SNI: 'tls_sni',
  CUSTOM: 'custom'
})

export function upstreamHostModeForRoute(route = {}) {
  if (Object.values(UPSTREAM_HOST_MODES).includes(route.upstreamHostMode)) return route.upstreamHostMode
  // 兼容旧 Vault：旧字段有值表示此前明确配置了自定义 Host。
  if (String(route.upstreamHost || '').trim()) return UPSTREAM_HOST_MODES.CUSTOM
  return UPSTREAM_HOST_MODES.ORIGINAL
}

export function upstreamHostFieldsForForm(form = {}) {
  if (form.upstreamScheme !== 'https') return { upstreamHostMode: '', upstreamHost: '' }
  const mode = Object.values(UPSTREAM_HOST_MODES).includes(form.upstreamHostMode)
    ? form.upstreamHostMode
    : UPSTREAM_HOST_MODES.ORIGINAL
  return {
    upstreamHostMode: mode,
    upstreamHost: mode === UPSTREAM_HOST_MODES.CUSTOM ? String(form.upstreamHost || '').trim() : ''
  }
}

export function upstreamHostDisplayValue(route = {}) {
  switch (upstreamHostModeForRoute(route)) {
    case UPSTREAM_HOST_MODES.TLS_SNI:
      return route.tlsSni || ''
    case UPSTREAM_HOST_MODES.CUSTOM:
      return route.upstreamHost || ''
    default:
      return route.domain || ''
  }
}
