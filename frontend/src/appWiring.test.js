import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('根组件向 RoutesPage 传递 revision 和应用状态', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  const mount = source.match(/<RoutesPage[\s\S]*?\/>/)?.[0] || ''
  assert.match(mount, /:route-statuses="routeStatuses"/)
  assert.match(mount, /:vault-revision="vaultRevision"/)
})
