<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorMessage } from '../../utils/backend'
import { createApplicationClient, createCommandMeta } from '../../utils/applicationClient'
import { routeAppliedView as deriveRouteAppliedView } from '../../modules/routeAppliedView'
import {
  UPSTREAM_HOST_MODES,
  upstreamHostDisplayValue,
  upstreamHostFieldsForForm,
  upstreamHostModeForRoute
} from '../../modules/upstreamHostMode'
import TooltipText from '../common/TooltipText.vue'
import IconActionButton from '../common/IconActionButton.vue'
import StatusChip from '../common/StatusChip.vue'
import ConfirmDialog from '../common/ConfirmDialog.vue'
import RouteModal from '../modals/RouteModal.vue'
import BaseDialog from '../common/BaseDialog.vue'

const props = defineProps({
  forwards: {
    type: Array,
    default: () => []
  },
  webRoutes: {
    type: Array,
    default: () => []
  },
  routeStatuses: {
    type: Array,
    default: () => []
  },
  vaultRevision: {
    type: String,
    default: ''
  },
  configurationLocked: { type: Boolean, default: false }
})

const emit = defineEmits(['vault-changed', 'notify'])

const { t } = useI18n()
const application = createApplicationClient()

// 仅 local 模式的 Forward 可被 Route 引用
const localForwards = computed(() => props.forwards.filter((forward) => forward.mode === 'local'))

const committedRoutes = ref({})
const committedDeletions = ref({})
const routeErrors = ref({})
const visibleRoutes = computed(() => {
  const routes = props.webRoutes
    .filter((route) => !committedDeletions.value[route.id])
    .map((route) => committedRoutes.value[route.id]?.route || route)
  for (const entry of Object.values(committedRoutes.value)) {
    if (!routes.some((route) => route.id === entry.route.id)) routes.push(entry.route)
  }
  return routes
})
const sortedRoutes = computed(() =>
  [...visibleRoutes.value].sort((a, b) => String(a.domain).localeCompare(String(b.domain)) || (a.id - b.id))
)

function forwardName(forwardId) {
  const forward = props.forwards.find((item) => item.id === forwardId)
  return forward ? forward.name : `#${forwardId}`
}

// 上游目标完整描述：scheme → forward 名 (host:port)，https 附 SNI 与有效 Host（仅展示，不影响逻辑）
function upstreamDetail(route) {
  const forward = props.forwards.find((item) => item.id === route.forwardId)
  const scheme = route.upstreamScheme === 'https' ? 'https' : 'http'
  const target = forward ? `${forward.name} (${forward.localHost}:${forward.localPort})` : forwardName(route.forwardId)
  const sni = route.upstreamScheme === 'https' && route.tlsSni ? ` · SNI ${route.tlsSni}` : ''
  const upstreamHost = route.upstreamScheme === 'https' ? upstreamHostDisplayValue(route) : ''
  const host = upstreamHost ? ` · Host ${upstreamHost}` : ''
  return `${scheme} → ${target}${sni}${host}`
}

// ---- 系统状态：只消费根 AppSnapshot，页面不再独立轮询或猜测 ----
const statusMap = computed(() => {
  const next = {}
  for (const item of props.routeStatuses) next[item.routeId] = item
  return next
})

function statusOf(routeId) {
  return statusMap.value[routeId] || null
}

function routeErrorMessage(routeId) {
  return routeErrors.value[routeId] || statusOf(routeId)?.error || ''
}

watch(() => props.vaultRevision, (revision) => {
  const routes = { ...committedRoutes.value }
  for (const [id, entry] of Object.entries(routes)) {
    if (entry.acceptedRevision === revision) delete routes[id]
  }
  committedRoutes.value = routes
  const deletions = { ...committedDeletions.value }
  for (const [id, entry] of Object.entries(deletions)) {
    if (entry.acceptedRevision === revision) delete deletions[id]
  }
  committedDeletions.value = deletions
})

// ---- revision-bound Route intent：Preview → 确认 → Commit ----
let routeRequestSequence = 0
const routeMutation = reactive({
  active: false,
  routeId: 0,
  flag: '',
  originalValue: false,
  targetValue: false,
  phase: '',
  requestToken: 0,
  context: null,
  error: ''
})
const routeMutationBusy = computed(() => routeMutation.active)

const dnsConfirm = reactive({
  visible: false,
  preview: null,
  hostsRecords: [],
  domains: [],
  caTrustNeeded: false,
  caFingerprint: '',
  busy: false
})

function openRouteConfirm(preview) {
  if (routeMutation.context?.source === 'modal') routeModalOpen.value = false
  dnsConfirm.preview = preview
  dnsConfirm.hostsRecords = Array.isArray(preview?.hostsRecords) ? preview.hostsRecords : []
  dnsConfirm.domains = Array.isArray(preview?.requiresConfirmation) ? preview.requiresConfirmation : []
  dnsConfirm.caTrustNeeded = !!preview?.caTrustNeeded
  dnsConfirm.caFingerprint = preview?.caFingerprint || ''
  dnsConfirm.visible = true
}

function resetRouteConfirm() {
  dnsConfirm.visible = false
  dnsConfirm.preview = null
  dnsConfirm.hostsRecords = []
  dnsConfirm.domains = []
  dnsConfirm.caTrustNeeded = false
  dnsConfirm.caFingerprint = ''
}

function cancelRouteConfirm() {
  if (dnsConfirm.busy) return
  const restoreRouteModal = routeMutation.context?.source === 'modal'
  routeMutation.requestToken = ++routeRequestSequence
  resetRouteConfirm()
  routeMutation.active = false
  routeMutation.phase = ''
  routeMutation.context = null
  if (restoreRouteModal) routeModalOpen.value = true
}

function resultError(result) {
  return result?.error?.message || result?.error?.Message || ''
}

function setRouteError(routeId, message) {
  if (!routeId) return
  const next = { ...routeErrors.value }
  if (message) next[routeId] = message
  else delete next[routeId]
  routeErrors.value = next
}

function acceptCommittedResult(result, context) {
  const acceptedRevision = result?.acceptedRevision || ''
  if (result?.route && acceptedRevision !== props.vaultRevision) {
    committedRoutes.value = {
      ...committedRoutes.value,
      [result.route.id]: { route: result.route, acceptedRevision }
    }
  } else if (context?.action === 'delete' && context.routeId) {
    committedDeletions.value = {
      ...committedDeletions.value,
      [context.routeId]: { acceptedRevision }
    }
  }
  if (context?.source === 'modal') routeModalOpen.value = false
  setRouteError(context?.routeId || result?.route?.id, result?.outcome === 'saved_not_applied' || result?.outcome === 'state_unknown' ? resultError(result) || t('routes.status.error') : '')
  if (result?.outcome === 'hosts_only') {
    emit('notify', t('routes.notify.portConflictWarning'))
  } else if (result?.outcome === 'saved_not_applied' || result?.outcome === 'state_unknown') {
    emit('notify', resultError(result) || t('routes.status.error'))
  } else {
    emit('notify', t('routes.notify.applied'))
  }
  emit('vault-changed', result)
}

async function commitPreview(preview, confirmedDomains = [], confirmCATrust = false) {
  const requestToken = routeMutation.requestToken
  routeMutation.phase = 'committing'
  const context = routeMutation.context
  try {
    const result = await application.commitRouteChange({
      meta: createCommandMeta(preview.desiredRevision || props.vaultRevision),
      token: preview.token,
      confirmedDomains,
      confirmCATrust
    })
    if (requestToken !== routeMutation.requestToken) return
    if (result?.desiredSaved) {
      acceptCommittedResult(result, context)
    } else {
      routeMutation.error = resultError(result)
      setRouteError(context?.routeId, routeMutation.error)
      if (context?.source === 'modal') routeValidationError.value = routeMutation.error
      else if (routeMutation.error) emit('notify', routeMutation.error)
    }
  } catch (err) {
    if (requestToken !== routeMutation.requestToken) return
    // 传输中断无法证明 Commit 是否到达后端，保留最后已知值并请求 Snapshot 刷新。
    routeMutation.error = errorMessage(err)
    setRouteError(context?.routeId, routeMutation.error)
    emit('notify', routeMutation.error)
    emit('vault-changed')
  } finally {
    if (requestToken === routeMutation.requestToken) {
      resetRouteConfirm()
      dnsConfirm.busy = false
      routeMutation.active = false
      routeMutation.phase = ''
      routeMutation.context = null
    }
  }
}

async function beginRouteChange(intent, context) {
  if (routeMutation.active || props.configurationLocked) return
  const requestToken = ++routeRequestSequence
  Object.assign(routeMutation, {
    active: true,
    routeId: intent.routeId || intent.route?.id || 0,
    flag: intent.flag || '',
    originalValue: !!context?.originalValue,
    targetValue: !!intent.enabled,
    phase: 'previewing',
    requestToken,
    context,
    error: ''
  })
  setRouteError(context?.routeId, '')
  try {
    const preview = await application.previewRouteChange(intent)
    if (requestToken !== routeMutation.requestToken) return
    const needsConfirmation = (Array.isArray(preview?.requiresConfirmation) && preview.requiresConfirmation.length) || preview?.caTrustNeeded
    if (needsConfirmation) {
      routeMutation.phase = 'confirming'
      openRouteConfirm(preview)
      return
    }
    await commitPreview(preview)
  } catch (err) {
    if (requestToken !== routeMutation.requestToken) return
    routeMutation.error = errorMessage(err)
    if (context?.source === 'modal') routeValidationError.value = routeMutation.error
    else emit('notify', routeMutation.error)
    routeMutation.active = false
    routeMutation.phase = ''
    routeMutation.context = null
  }
}

async function confirmDnsOverride() {
  if (dnsConfirm.busy) return
  dnsConfirm.busy = true
  const preview = dnsConfirm.preview
  const domains = [...dnsConfirm.domains]
  await commitPreview(preview, domains, dnsConfirm.caTrustNeeded)
}

// ---- Route 新建 / 编辑 ----
const routeModalOpen = ref(false)
const editingRouteId = ref(null)
const routeValidationError = ref('')
const routeForm = reactive(defaultRouteForm())

function defaultRouteForm() {
  return {
    domain: '',
    forwardId: 0,
    hostsEnabled: true,
    caddyEnabled: false,
    upstreamScheme: 'http',
    tlsSni: '',
    upstreamHostMode: UPSTREAM_HOST_MODES.ORIGINAL,
    upstreamHost: ''
  }
}

function openNewRoute() {
  if (props.configurationLocked) return
  if (!localForwards.value.length) {
    emit('notify', t('routes.notify.needForward'))
    return
  }
  editingRouteId.value = null
  Object.assign(routeForm, defaultRouteForm(), { forwardId: localForwards.value[0]?.id || 0 })
  routeValidationError.value = ''
  routeModalOpen.value = true
}

defineExpose({ openNewRoute })

function editRoute(route) {
  if (props.configurationLocked) return
  const upstreamHostMode = upstreamHostModeForRoute(route)
  editingRouteId.value = route.id
  Object.assign(routeForm, {
    domain: route.domain,
    forwardId: route.forwardId,
    hostsEnabled: !!route.hostsEnabled,
    caddyEnabled: !!route.caddyEnabled,
    upstreamScheme: route.upstreamScheme || 'http',
    tlsSni: route.tlsSni || '',
    upstreamHostMode,
    upstreamHost: upstreamHostMode === UPSTREAM_HOST_MODES.CUSTOM ? (route.upstreamHost || '') : ''
  })
  routeValidationError.value = ''
  routeModalOpen.value = true
}

function validateRoutePayload(payload) {
  if (!payload.domain) return t('routes.errors.domainRequired')
  if (/\s/.test(payload.domain) || !payload.domain.includes('.')) return t('routes.errors.domainInvalid')
  if (!payload.forwardId) return t('routes.errors.forwardRequired')
  if (payload.upstreamScheme === 'https' && !payload.tlsSni) return t('routes.errors.tlsSniRequired')
  if (payload.upstreamScheme === 'https' && payload.upstreamHostMode === UPSTREAM_HOST_MODES.CUSTOM && !payload.upstreamHost) {
    return t('routes.errors.upstreamHostRequired')
  }
  return ''
}

async function saveRoute() {
  if (props.configurationLocked || routeMutation.active) return
  const upstreamHostFields = upstreamHostFieldsForForm({
    upstreamScheme: routeForm.upstreamScheme,
    upstreamHostMode: routeForm.upstreamHostMode,
    upstreamHost: routeForm.upstreamHost
  })
  const payload = {
    id: editingRouteId.value || 0,
    forwardId: Number(routeForm.forwardId),
    domain: routeForm.domain.trim(),
    hostsEnabled: !!routeForm.hostsEnabled,
    caddyEnabled: !!routeForm.caddyEnabled,
    upstreamScheme: routeForm.upstreamScheme,
    tlsSni: routeForm.upstreamScheme === 'https' ? routeForm.tlsSni.trim() : '',
    ...upstreamHostFields
  }
  const error = validateRoutePayload(payload)
  if (error) {
    routeValidationError.value = error
    return
  }
  routeValidationError.value = ''
  await beginRouteChange(
    { action: 'upsert', route: payload, expectedRevision: props.vaultRevision },
    { source: 'modal', action: 'upsert' }
  )
}

// ---- 表格内开关：仅发送事件的真实 checked 意图 ----
function routeFlagValue(route, field) {
  const committed = committedRoutes.value[route.id]?.route
  if (committed) return !!committed[field]
  if (routeMutation.active && routeMutation.routeId === route.id && routeMutation.flag === field) {
    return routeMutation.targetValue
  }
  return !!route[field]
}

async function toggleRouteFlag(route, field, event) {
  if (props.configurationLocked || routeMutation.active) return
  const enabled = !!event.target.checked
  await beginRouteChange(
    { action: 'set_flag', routeId: route.id, flag: field, enabled, expectedRevision: props.vaultRevision },
    { source: 'toggle', action: 'set_flag', routeId: route.id, originalValue: !!route[field] }
  )
}

async function retryRoute(route) {
  if (props.configurationLocked || routeMutation.active) return
  await beginRouteChange(
    { action: 'upsert', route: { ...route }, expectedRevision: props.vaultRevision },
    { source: 'retry', action: 'upsert', routeId: route.id }
  )
}

function routeAppliedView(route) {
  return deriveRouteAppliedView(route, statusOf(route.id), t)
}

// ---- Route 删除（后端负责撤销 hosts / Caddy）----
const deleteDialog = reactive({
  visible: false,
  route: null
})

function deleteRoute(route) {
  if (props.configurationLocked) return
  deleteDialog.route = route
  deleteDialog.visible = true
}

async function confirmDeleteRoute() {
  const route = deleteDialog.route
  deleteDialog.visible = false
  deleteDialog.route = null
  if (!route) return
  await beginRouteChange(
    { action: 'delete', routeId: route.id, expectedRevision: props.vaultRevision },
    { source: 'delete', action: 'delete', routeId: route.id }
  )
}
</script>

<template>
  <section class="page-fade">
    <div class="panel-card">
      <div class="panel-head">
        <h2 class="panel-title mb-0">{{ t('routes.tableTitle') }}</h2>
        <button
          type="button"
          class="btn icon-ghost-btn"
          :title="t('routes.newRoute')"
          :aria-label="t('routes.newRoute')"
          :disabled="configurationLocked || routeMutationBusy"
          @click="openNewRoute"
        >
          <i class="bi bi-plus-lg" aria-hidden="true"></i>
        </button>
      </div>

      <div v-if="!sortedRoutes.length" class="empty-state">
        <i class="bi bi-globe2 empty-state-icon" aria-hidden="true"></i>
        <p class="empty-state-text">{{ t('routes.empty') }}</p>
        <button type="button" class="btn btn-primary header-action-btn" :disabled="configurationLocked || routeMutationBusy" @click="openNewRoute">
          <i class="bi bi-plus-lg" aria-hidden="true"></i>{{ t('app.header.newRoute') }}
        </button>
      </div>

      <div v-else class="route-card-list">
        <div v-for="route in sortedRoutes" :key="route.id" class="route-card">
          <div class="route-card-head">
            <TooltipText :text="route.domain" class-name="route-card-domain" />
            <span class="route-card-forward" :title="forwardName(route.forwardId)">
              {{ forwardName(route.forwardId) }}
            </span>
            <div class="card-corner-actions">
              <IconActionButton
                icon-class="bi-pencil"
                button-class="icon-ghost-btn"
                :title="t('app.common.edit')"
                :aria-label="t('app.common.edit')"
                :disabled="configurationLocked || routeMutationBusy"
                @click="editRoute(route)"
              />
              <IconActionButton
                icon-class="bi-trash3"
                button-class="icon-ghost-btn danger"
                :title="t('app.common.delete')"
                :aria-label="t('app.common.delete')"
                :disabled="configurationLocked || routeMutationBusy"
                @click="deleteRoute(route)"
              />
            </div>
          </div>
          <div class="font-mono route-card-upstream cell-ellipsis" :title="upstreamDetail(route)">
            {{ upstreamDetail(route) }}
          </div>
          <div class="route-card-foot">
            <div class="route-flag-group">
              <span class="route-flag">
                <span class="route-flag-label">{{ t('routes.modal.hostsEnabled') }}</span>
                <span class="form-check form-switch mb-0">
                  <input
                    type="checkbox"
                    class="form-check-input"
                    :checked="routeFlagValue(route, 'hostsEnabled')"
                    :disabled="configurationLocked || routeMutationBusy"
                    :aria-busy="routeMutation.active && routeMutation.routeId === route.id"
                    :aria-label="t('routes.table.hosts')"
                    @change="toggleRouteFlag(route, 'hostsEnabled', $event)"
                  />
                </span>
              </span>
              <span class="route-flag">
                <span class="route-flag-label">{{ t('routes.modal.caddyEnabled') }}</span>
                <span class="form-check form-switch mb-0">
                  <input
                    type="checkbox"
                    class="form-check-input"
                    :checked="routeFlagValue(route, 'caddyEnabled')"
                    :disabled="configurationLocked || routeMutationBusy"
                    :aria-busy="routeMutation.active && routeMutation.routeId === route.id"
                    :aria-label="t('routes.table.caddy')"
                    @change="toggleRouteFlag(route, 'caddyEnabled', $event)"
                  />
                </span>
              </span>
            </div>
            <div class="route-card-status">
              <StatusChip :status="routeAppliedView(route).status" :label="routeAppliedView(route).label" />
              <i
                v-if="statusOf(route.id)?.caTrusted"
                class="bi bi-shield-lock-fill ca-trusted-icon"
                :title="t('routes.status.caTrusted')"
                aria-hidden="true"
              ></i>
            </div>
          </div>
          <div v-if="routeErrorMessage(route.id)" class="form-error mt-2" role="alert">
            <span>{{ routeErrorMessage(route.id) }}</span>
            <button type="button" class="btn btn-sm btn-outline-danger ms-2" :disabled="configurationLocked || routeMutationBusy" @click="retryRoute(route)">
              {{ t('app.snapshot.retry') }}
            </button>
          </div>
        </div>
      </div>
      <span class="visually-hidden" aria-live="polite">{{ routeMutationBusy ? t('routes.status.pending') : '' }}</span>
    </div>
  </section>

  <RouteModal
    :show="routeModalOpen"
    :editing-route-id="editingRouteId"
    :form="routeForm"
    :forwards="localForwards"
    :validation-error="routeValidationError"
    :busy="routeMutationBusy"
    @close="!routeMutationBusy && (routeModalOpen = false)"
    @submit="saveRoute"
  />

  <ConfirmDialog
    :visible="deleteDialog.visible"
    :title="t('routes.confirmations.deleteRouteTitle')"
    :message="t('routes.confirmations.deleteRoute', { domain: deleteDialog.route?.domain || '' })"
    @confirm="confirmDeleteRoute"
    @close="deleteDialog.visible = false"
  />

  <BaseDialog :visible="dnsConfirm.visible" :title="dnsConfirm.domains.length ? t('routes.confirmations.dnsOverrideTitle') : t('routes.confirmations.caTrustTitle')" :busy="dnsConfirm.busy" @close="cancelRouteConfirm">
        <p v-if="dnsConfirm.domains.length" class="action-dialog-message">{{ t('routes.confirmations.dnsOverrideMessage', { domains: dnsConfirm.domains.join(', ') }) }}</p>
        <p v-if="dnsConfirm.caTrustNeeded" class="action-dialog-message">{{ t('routes.confirmations.caTrustMessage') }}</p>
        <div v-if="dnsConfirm.caTrustNeeded && dnsConfirm.caFingerprint" class="inline-notice" role="note">
          <span>{{ t('routes.confirmations.caFingerprintLabel') }}</span>
          <code class="font-mono text-break">{{ dnsConfirm.caFingerprint }}</code>
        </div>
        <table v-if="dnsConfirm.hostsRecords.length" class="table table-sm align-middle mt-2 mb-2">
          <thead>
            <tr>
              <th>{{ t('routes.confirmations.hostsRecordDomain') }}</th>
              <th>{{ t('routes.confirmations.hostsRecordIp') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="record in dnsConfirm.hostsRecords" :key="record.domain">
              <td class="font-mono">{{ record.domain }}</td>
              <td class="font-mono">{{ record.ip }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="dnsConfirm.domains.length" class="inline-notice" role="alert">
          <i class="bi bi-exclamation-triangle" aria-hidden="true"></i>
          <span>{{ t('routes.confirmations.dnsOverrideWarning') }}</span>
        </div>
    <template #footer>
        <button type="button" class="btn btn-outline-secondary" :disabled="dnsConfirm.busy" @click="cancelRouteConfirm">
          {{ t('app.common.cancel') }}
        </button>
        <button type="button" class="btn btn-warning" :disabled="dnsConfirm.busy" @click="confirmDnsOverride">
          {{ t('routes.confirmations.dnsOverrideConfirm') }}
        </button>
    </template>
  </BaseDialog>
</template>
