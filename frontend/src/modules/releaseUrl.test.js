import test from 'node:test'
import assert from 'node:assert/strict'
import { officialReleaseUrl } from './releaseUrl.js'

test('只允许 TunnelBoard 官方 GitHub Releases 地址', () => {
  assert.equal(officialReleaseUrl('https://github.com/HanZephyr/TunnelBoard/releases/tag/v1.2.3'), 'https://github.com/HanZephyr/TunnelBoard/releases/tag/v1.2.3')
  assert.equal(officialReleaseUrl('https://evil.test/phish'), 'https://github.com/HanZephyr/TunnelBoard/releases')
})
