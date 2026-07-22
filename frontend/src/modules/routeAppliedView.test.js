import test from 'node:test'
import assert from 'node:assert/strict'
import { routeAppliedView } from './routeAppliedView.js'

const t = (key) => key

test('仅启用 hosts 的 Route 不显示 Caddy 已生效', () => {
  const view = routeAppliedView(
    { hostsEnabled: true, caddyEnabled: false },
    { state: 'applied', hostsApplied: true, caddyRunning: false },
    t
  )

  assert.deepEqual(view, { status: 'running', label: 'routes.status.hostsOnly' })
})

test('启用且运行中的 Caddy Route 显示完整生效', () => {
  const view = routeAppliedView(
    { hostsEnabled: true, caddyEnabled: true },
    { state: 'applied', hostsApplied: true, caddyRunning: true },
    t
  )

  assert.deepEqual(view, { status: 'running', label: 'routes.status.applied' })
})

test('高风险 Route 状态不降级为灰色 stopped', () => {
  for (const [state, status, label] of [
    ['conflict', 'error', 'routes.status.portConflict'],
    ['error', 'error', 'routes.status.error'],
    ['cleanup_pending', 'reconnecting', 'routes.status.cleanupPending'],
    ['quarantined', 'reconnecting', 'routes.status.quarantined']
  ]) {
    assert.deepEqual(
      routeAppliedView({ hostsEnabled: true, caddyEnabled: true }, { state }, t),
      { status, label }
    )
  }
})
