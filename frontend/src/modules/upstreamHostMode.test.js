import test from 'node:test'
import assert from 'node:assert/strict'
import {
  UPSTREAM_HOST_MODES,
  upstreamHostDisplayValue,
  upstreamHostFieldsForForm,
  upstreamHostModeForRoute
} from './upstreamHostMode.js'

test('新路由默认使用原始 Host', () => {
  assert.equal(upstreamHostModeForRoute({}), UPSTREAM_HOST_MODES.ORIGINAL)
  assert.deepEqual(
    upstreamHostFieldsForForm({ upstreamScheme: 'https', upstreamHostMode: UPSTREAM_HOST_MODES.ORIGINAL, upstreamHost: 'stale.example' }),
    { upstreamHostMode: UPSTREAM_HOST_MODES.ORIGINAL, upstreamHost: '' }
  )
})

test('三种 Host 模式分别保留原始 Host、TLS SNI 或自定义 Host', () => {
  const route = { domain: 'admin.example.localhost', tlsSni: 'backend.internal' }
  assert.equal(upstreamHostDisplayValue({ ...route, upstreamHostMode: UPSTREAM_HOST_MODES.ORIGINAL }), route.domain)
  assert.equal(upstreamHostDisplayValue({ ...route, upstreamHostMode: UPSTREAM_HOST_MODES.TLS_SNI }), route.tlsSni)
  assert.equal(upstreamHostDisplayValue({ ...route, upstreamHostMode: UPSTREAM_HOST_MODES.CUSTOM, upstreamHost: 'backend.example' }), 'backend.example')
  assert.deepEqual(
    upstreamHostFieldsForForm({ upstreamScheme: 'https', upstreamHostMode: UPSTREAM_HOST_MODES.CUSTOM, upstreamHost: ' backend.example ' }),
    { upstreamHostMode: UPSTREAM_HOST_MODES.CUSTOM, upstreamHost: 'backend.example' }
  )
})

test('旧记录已有 upstreamHost 时兼容为自定义 Host', () => {
  assert.equal(upstreamHostModeForRoute({ upstreamHost: 'legacy.internal' }), UPSTREAM_HOST_MODES.CUSTOM)
  assert.equal(upstreamHostDisplayValue({ domain: 'admin.example.localhost', upstreamHost: 'legacy.internal' }), 'legacy.internal')
})

test('HTTP 和 HTTPS 保存自定义 Host，并在原始模式清除旧值', () => {
  for (const upstreamScheme of ['http', 'https']) {
    assert.deepEqual(
      upstreamHostFieldsForForm({ upstreamScheme, upstreamHostMode: UPSTREAM_HOST_MODES.CUSTOM, upstreamHost: ' backend.example:8080 ' }),
      { upstreamHostMode: UPSTREAM_HOST_MODES.CUSTOM, upstreamHost: 'backend.example:8080' }
    )
    assert.deepEqual(
      upstreamHostFieldsForForm({ upstreamScheme, upstreamHostMode: UPSTREAM_HOST_MODES.ORIGINAL, upstreamHost: 'stale.example' }),
      { upstreamHostMode: UPSTREAM_HOST_MODES.ORIGINAL, upstreamHost: '' }
    )
  }
})

test('跟随 TLS SNI 切换到 HTTP 后保留为自定义 Host，HTTPS 继续跟随', () => {
  const form = { upstreamScheme: 'https', upstreamHostMode: UPSTREAM_HOST_MODES.TLS_SNI, tlsSni: ' backend.internal ', upstreamHost: 'stale.example' }
  assert.deepEqual(upstreamHostFieldsForForm(form), { upstreamHostMode: UPSTREAM_HOST_MODES.TLS_SNI, upstreamHost: '' })
  assert.deepEqual(upstreamHostFieldsForForm({ ...form, upstreamScheme: 'http' }), {
    upstreamHostMode: UPSTREAM_HOST_MODES.CUSTOM,
    upstreamHost: 'backend.internal'
  })
})
