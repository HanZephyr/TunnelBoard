import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

function pageMount(source, key) {
  return [...source.matchAll(/<PageLoader[\s\S]*?\/>/g)]
    .map((match) => match[0])
    .find((mount) => mount.includes(`:loader="pageLoaders.${key}"`)) || ''
}

test('根组件向 RoutesPage 传递 revision 和应用状态', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  const mount = pageMount(source, 'routes')
  assert.match(mount, /:route-statuses="routeStatuses"/)
  assert.match(mount, /:vault-revision="vaultRevision"/)
})

test('根 Snapshot 将后端 SSH Host 默认值传给两个编辑入口', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  const matches = source.match(/:ssh-host-defaults="sshHostDefaults"/g) || []
  assert.equal(matches.length, 2)
  assert.match(source, /raw\?\.sshHostDefaults/)
})

test('Route 确认前隐藏表单弹窗，取消确认时恢复', async () => {
  const source = await readFile(new URL('./components/pages/RoutesPage.vue', import.meta.url), 'utf8')
  assert.match(source, /function openRouteConfirm[\s\S]*routeModalOpen\.value = false/)
  assert.match(source, /function cancelRouteConfirm[\s\S]*routeModalOpen\.value = true/)
})

test('Route 页面使用独立的状态视图模块', async () => {
  const source = await readFile(new URL('./components/pages/RoutesPage.vue', import.meta.url), 'utf8')
  assert.match(source, /routeAppliedView as deriveRouteAppliedView/)
  assert.match(source, /deriveRouteAppliedView\(route, statusOf\(route\.id\), t\)/)
})

test('Route HTTPS Host 使用三种模式并仅在自定义模式显示输入框', async () => {
  const [page, modal] = await Promise.all([
    readFile(new URL('./components/pages/RoutesPage.vue', import.meta.url), 'utf8'),
    readFile(new URL('./components/modals/RouteModal.vue', import.meta.url), 'utf8')
  ])
  assert.match(page, /upstreamHostModeForRoute/)
  assert.match(page, /upstreamHostFieldsForForm/)
  assert.match(modal, /routeUpstreamHostMode/)
  assert.match(modal, /upstreamHostModeOriginal/)
  assert.match(modal, /form\.upstreamHostMode === 'custom'/)
})

test('LogsPage 传递完整 generation cursor 而不是只传 offset', async () => {
  const source = await readFile(new URL('./components/pages/LogsPage.vue', import.meta.url), 'utf8')
  assert.match(source, /GetLogTailV2/)
  assert.match(source, /GetLogTailV2, requestedSource, cursor/)
  assert.doesNotMatch(source, /GetLogTail, requestedSource, offset/)
})

test('ForwardModal 真正接入 listenerPreview 状态机', async () => {
  const source = await readFile(new URL('./components/modals/ForwardModal.vue', import.meta.url), 'utf8')
  assert.match(source, /createListenerPreview\(portPreviewState\)/)
  assert.match(source, /portPreview\.check/)
  assert.doesNotMatch(source, /catch \(_\)[\s\S]{0,160}portConflict/)
})

test('SSH 主机和 Forward 编辑器都提供不会保存配置的连通性检查入口', async () => {
  const [hosts, forwards, hostModal, forwardModal] = await Promise.all([
    readFile(new URL('./components/pages/HostsPage.vue', import.meta.url), 'utf8'),
    readFile(new URL('./components/pages/ForwardsPage.vue', import.meta.url), 'utf8'),
    readFile(new URL('./components/modals/HostModal.vue', import.meta.url), 'utf8'),
    readFile(new URL('./components/modals/ForwardModal.vue', import.meta.url), 'utf8')
  ])
  assert.match(hosts, /application\.testSSHHostConnection/)
  assert.match(forwards, /application\.testForwardConnection/)
  assert.match(hosts, /@test-connection="testHostConnection"/)
  assert.match(forwards, /@test-connection="testForwardConnection"/)
  assert.match(hostModal, /\$emit\('test-connection'\)/)
  assert.match(forwardModal, /\$emit\('test-connection'\)/)
})

test('SSH 主机编辑器提供文件选择和 Ed25519 密钥生成功能', async () => {
  const source = await readFile(new URL('./components/hosts/SSHHostFields.vue', import.meta.url), 'utf8')
  assert.match(source, /GenerateSSHKeyPair, SelectSSHKeyFile, SelectSSHKeySavePath/)
  assert.match(source, /\.\.\/\.\.\/\.\.\/wailsjs\/go\/main\/App/)
  assert.match(source, /@click="browseKeyFile"/)
  assert.match(source, /@click="generateKeyPair"/)
  assert.match(source, /generatedPublicKey/)
})

test('SSH 主机测试遇到未知或变更的 host key 时要求用户确认后再重新测试', async () => {
  const [hosts, dialog] = await Promise.all([
    readFile(new URL('./components/pages/HostsPage.vue', import.meta.url), 'utf8'),
    readFile(new URL('./components/modals/HostKeyDialog.vue', import.meta.url), 'utf8')
  ])

  assert.match(hosts, /import \{[^}]*EnrollHostKey[^}]*ReplaceHostKey[^}]*\}/)
  assert.match(hosts, /const hostKey = parseHostKeyError\(message\)/)
  assert.match(hosts, /pendingHostKey\.value = hostKey/)
  assert.match(hosts, /callBackend\(EnrollHostKey, item\.host, item\.port, '', item\.fingerprint\)/)
  assert.match(hosts, /callBackend\(ReplaceHostKey, item\.host, item\.port, '', item\.fingerprint\)/)
  assert.match(hosts, /await testHostConnection\(\)/)
  assert.match(hosts, /<HostKeyDialog[\s\S]*@confirm="confirmHostKey"/)
  assert.match(dialog, /confirmLabel/)
})

test('公共模态框标题栏将关闭按钮保持在同一行右侧', async () => {
  const source = await readFile(new URL('./styles/app-shell.css', import.meta.url), 'utf8')
  const rule = source.match(/\.dialog-head\s*\{([\s\S]*?)\}/)?.[1] || ''
  assert.match(rule, /display:\s*flex/)
  assert.match(rule, /align-items:\s*center/)
  assert.match(rule, /justify-content:\s*space-between/)
})

test('更新偏好失败后保持关闭并提供重试', async () => {
  const source = await readFile(new URL('./components/pages/SettingsPage.vue', import.meta.url), 'utf8')
  assert.match(source, /updatePreferencePhase\.value = 'error'/)
  assert.match(source, /@click="loadUpdatePreference"/)
  assert.match(source, /updatePreferencePhase !== 'ready'/)
})

test('设置页区分可备份的 Vault 数据目录和本机 Caddy HTTPS 运行目录', async () => {
  const [settings, zhCN] = await Promise.all([
    readFile(new URL('./components/pages/SettingsPage.vue', import.meta.url), 'utf8'),
    readFile(new URL('./locales/zh-CN.json', import.meta.url), 'utf8')
  ])
  assert.match(settings, /GetCaddyDataPath/)
  assert.match(settings, /OpenCaddyDataDir/)
  assert.match(settings, /caddyDataPath/)
  assert.match(settings, /settings\.caddyDataDir/)
  assert.match(settings, /settings\.caddyDataDirDesc/)
  assert.match(zhCN, /"caddyDataDir"/)
  assert.match(zhCN, /"caddyDataDirDesc"/)
})

test('启动时的本地 CA 信任提示同时读取缓存状态并订阅后续事件', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  assert.match(source, /GetStartupCATrustRequest/)
  assert.match(source, /ConfirmStartupCATrust/)
  assert.match(source, /EventsOn\('tunnelboard:startup-ca-trust-required'/)
  assert.match(source, /function acceptStartupCATrustRequest/)
  assert.match(source, /function confirmStartupCATrust/)
  assert.match(source, /routes\.confirmations\.caTrustTitle/)
})

test('启动的其他网络错误不能吞掉本地 CA 信任提示', async () => {
  const source = await readFile(new URL('../../app.go', import.meta.url), 'utf8')
  const startup = source.slice(source.indexOf('go func() {'), source.indexOf('// shutdown'))
  assert.ok(startup.indexOf('if result.CATrustNeeded') >= 0)
  assert.ok(startup.indexOf('if result.CATrustNeeded') < startup.indexOf('if err != nil'))
})

test('高层命令结果先登记 revision 和 event sequence 再刷新', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  assert.match(source, /function onVaultChanged[\s\S]*acceptRevision\(result\?\.acceptedRevision, result\?\.eventSequence\)[\s\S]*await loadVault/)
	assert.equal((source.match(/@vault-changed="onVaultChanged"/g) || []).length, 4)
	const settingsMount = pageMount(source, 'settings')
	assert.match(settingsMount, /:vault-revision="vaultRevision"/)
})

test('导入提交携带幂等命令 ID 与当前 Vault revision', async () => {
	const source = await readFile(new URL('./components/pages/SettingsPage.vue', import.meta.url), 'utf8')
	assert.match(source, /CommitImportCommand, \{ meta: createCommandMeta\(props\.vaultRevision\)/)
	assert.match(source, /emit\('vault-changed', result\)/)
})

test('完整还原使用预检、提交和隔离激活三阶段接口', async () => {
	const source = await readFile(new URL('./components/pages/SettingsPage.vue', import.meta.url), 'utf8')
	assert.match(source, /StageRestoreCommand/)
	assert.match(source, /CommitRestoreCommand/)
	assert.match(source, /ActivateRestoredNetwork/)
	assert.match(source, /restoreState\.password = ''[\s\S]*restoreState\.confirmed = false/)
	assert.doesNotMatch(source, /\bRestoreBackup\b/)
})

test('根 Snapshot 向状态页面传递 Runtime 和恢复能力事实', async () => {
  const source = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  assert.match(source, /raw\?\.runtime/)
  assert.match(source, /raw\?\.capabilities/)
  assert.equal((source.match(/:runtime-statuses="runtimeStatuses"/g) || []).length, 2)
  assert.match(source, /configurationLocked/)
})

test('恢复隔离网络必须经过独立确认后才激活', async () => {
  const source = await readFile(new URL('./components/pages/SettingsPage.vue', import.meta.url), 'utf8')
  assert.match(source, /activateRestoreConfirm\.visible = true/)
  assert.match(source, /ConfirmDialog/)
  assert.match(source, /confirmActivateRestoredNetwork/)
  assert.doesNotMatch(source, /@click="onActivateRestoredNetwork"/)
})

test('异步页面加载失败时保留重试和诊断入口', async () => {
  const source = await readFile(new URL('./components/common/PageLoader.vue', import.meta.url), 'utf8')
  assert.match(source, /phase === 'loading'/)
  assert.match(source, /phase === 'error'/)
  assert.match(source, /function retry/)
  assert.match(source, /open-diagnostics/)
})

test('更新入口包含目标版本并通过根级 live region 去重播报', async () => {
  const app = await readFile(new URL('./App.vue', import.meta.url), 'utf8')
  const sidebar = await readFile(new URL('./components/layout/AppSidebar.vue', import.meta.url), 'utf8')
  assert.match(app, /announcedUpdateVersion/)
  assert.match(app, /aria-live="polite"/)
  assert.match(app, /:latest-version="updateNotice\.state\.latestVersion"/)
  assert.match(sidebar, /app\.update\.actionLabel/)
})
