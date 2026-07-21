export const DEFAULT_RELEASES_PAGE_URL = 'https://github.com/HanZephyr/TunnelBoard/releases'

export function officialReleaseUrl(candidate) {
  try {
    const parsed = new URL(String(candidate || ''))
    const path = parsed.pathname.toLowerCase()
    if (parsed.protocol === 'https:' && parsed.hostname === 'github.com' && (path === '/hanzephyr/tunnelboard/releases' || path.startsWith('/hanzephyr/tunnelboard/releases/'))) {
      return parsed.toString().replace(/\/$/, '')
    }
  } catch (_) {
    // invalid candidates fail closed to the fixed official page
  }
  return DEFAULT_RELEASES_PAGE_URL
}
