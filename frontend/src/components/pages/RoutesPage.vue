<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ApplyRoute,
  GetRouteStatus,
  PreviewRoute,
  RemoveRoute,
  SaveWebRoute
} from '../../../wailsjs/go/main/App'
import { callBackend, errorMessage } from '../../utils/backend'
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
  configurationLocked: { type: Boolean, default: false }
})

const emit = defineEmits(['vault-changed', 'notify'])

const { t } = useI18n()

// 仅 local 模式的 Forward 可被 Route 引用
const localForwards = computed(() => props.forwards.filter((forward) => forward.mode === 'local'))

const sortedRoutes = computed(() =>
  [...props.webRoutes].sort((a, b) => String(a.domain).localeCompare(String(b.domain)) || (a.id - b.id))
)

function forwardName(forwardId) {
  const forward = props.forwards.find((item) => item.id === forwardId)
  return forward ? forward.name : `#${forwardId}`
}

// 上游目标完整描述：scheme → forward 名 (host:port)，https 附 SNI（仅展示，不影响逻辑）
function upstreamDetail(route) {
  const forward = props.forwards.find((item) => item.id === route.forwardId)
  const scheme = route.upstreamScheme === 'https' ? 'https' : 'http'
  const target = forward ? `${forward.name} (${forward.localHost}:${forward.localPort})` : forwardName(route.forwardId)
  const sni = route.upstreamScheme === 'https' && route.tlsSni ? ` · SNI ${route.tlsSni}` : ''
  return `${scheme} → ${target}${sni}`
}

// ---- 系统状态（GetRouteStatus 轮询合并）----
const statusMap = ref({})
const statusPhase = ref('checking')
let statusTimer = null

function statusOf(routeId) {
  return statusMap.value[routeId] || null
}

async function refreshStatus() {
  if (!Object.keys(statusMap.value).length) statusPhase.value = 'checking'
  try {
    const items = await callBackend(GetRouteStatus)
    const next = {}
    for (const item of Array.isArray(items) ? items : []) {
      next[item.routeId] = item
    }
    statusMap.value = next
    statusPhase.value = 'ready'
  } catch (_) {
    statusPhase.value = Object.keys(statusMap.value).length ? 'stale' : 'unknown'
  }
}

onMounted(() => {
  void refreshStatus()
  statusTimer = window.setInterval(refreshStatus, 5000)
})

onBeforeUnmount(() => {
  if (statusTimer !== null) {
    window.clearInterval(statusTimer)
    statusTimer = null
  }
})

// ---- 保存即应用：SaveWebRoute → PreviewRoute →（按需确认）→ ApplyRoute ----
const dnsConfirm = reactive({
  visible: false,
  routeId: 0,
  hostsRecords: [],
  domains: [],
  busy: false
})

function openDnsConfirm(routeId, preview) {
  dnsConfirm.routeId = routeId
  dnsConfirm.hostsRecords = Array.isArray(preview?.hostsRecords) ? preview.hostsRecords : []
  dnsConfirm.domains = Array.isArray(preview?.requiresConfirmation) ? preview.requiresConfirmation : []
  dnsConfirm.visible = true
}

function closeDnsConfirm() {
  dnsConfirm.visible = false
  dnsConfirm.routeId = 0
  dnsConfirm.hostsRecords = []
  dnsConfirm.domains = []
}

async function applyRoute(routeId, confirmedDomains) {
  try {
    const result = await callBackend(ApplyRoute, routeId, confirmedDomains)
    if (result?.portConflict) {
      emit('notify', t('routes.notify.portConflictWarning'))
    } else {
      emit('notify', t('routes.notify.applied'))
    }
  } catch (err) {
    // helper 未安装 / 服务不可用等场景：toast 展示错误，不中断页面
    emit('notify', errorMessage(err))
  } finally {
    void refreshStatus()
  }
}

async function previewAndApply(routeId) {
  let preview
  try {
    preview = await callBackend(PreviewRoute, routeId)
  } catch (err) {
    emit('notify', errorMessage(err))
    return
  }
  if (Array.isArray(preview?.requiresConfirmation) && preview.requiresConfirmation.length) {
    openDnsConfirm(routeId, preview)
    return
  }
  await applyRoute(routeId, [])
}

async function confirmDnsOverride() {
  if (dnsConfirm.busy) return
  dnsConfirm.busy = true
  const routeId = dnsConfirm.routeId
  const domains = [...dnsConfirm.domains]
  closeDnsConfirm()
  try {
    await applyRoute(routeId, domains)
  } finally {
    dnsConfirm.busy = false
  }
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
    tlsSni: ''
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
  editingRouteId.value = route.id
  Object.assign(routeForm, {
    domain: route.domain,
    forwardId: route.forwardId,
    hostsEnabled: !!route.hostsEnabled,
    caddyEnabled: !!route.caddyEnabled,
    upstreamScheme: route.upstreamScheme || 'http',
    tlsSni: route.tlsSni || ''
  })
  routeValidationError.value = ''
  routeModalOpen.value = true
}

function validateRoutePayload(payload) {
  if (!payload.domain) return t('routes.errors.domainRequired')
  if (/\s/.test(payload.domain) || !payload.domain.includes('.')) return t('routes.errors.domainInvalid')
  if (!payload.forwardId) return t('routes.errors.forwardRequired')
  if (payload.upstreamScheme === 'https' && !payload.tlsSni) return t('routes.errors.tlsSniRequired')
  return ''
}

async function saveRoute() {
  if (props.configurationLocked) return
  const payload = {
    id: editingRouteId.value || 0,
    forwardId: Number(routeForm.forwardId),
    domain: routeForm.domain.trim(),
    hostsEnabled: !!routeForm.hostsEnabled,
    caddyEnabled: !!routeForm.caddyEnabled,
    upstreamScheme: routeForm.upstreamScheme,
    tlsSni: routeForm.upstreamScheme === 'https' ? routeForm.tlsSni.trim() : ''
  }
  const error = validateRoutePayload(payload)
  if (error) {
    routeValidationError.value = error
    return
  }
  try {
    const saved = await callBackend(SaveWebRoute, payload)
    routeModalOpen.value = false
    emit('vault-changed')
    emit('notify', t('routes.notify.saved', { domain: saved?.domain || payload.domain }))
    await previewAndApply(saved?.id || payload.id)
  } catch (err) {
    routeValidationError.value = errorMessage(err)
  }
}

// ---- 表格内开关：保存（带全部字段）→ 预览 / 确认 / 应用 ----
const pendingRouteIds = ref(new Set())
const routeMutationBusy = computed(() => pendingRouteIds.value.size > 0 || dnsConfirm.busy)

function setRoutePending(routeId, pending) {
  const next = new Set(pendingRouteIds.value)
  if (pending) {
    next.add(routeId)
  } else {
    next.delete(routeId)
  }
  pendingRouteIds.value = next
}

async function toggleRouteFlag(route, field, event) {
  if (props.configurationLocked) {
    event.target.checked = !!route[field]
    return
  }
  if (pendingRouteIds.value.has(route.id)) return
  setRoutePending(route.id, true)
  const payload = {
    id: route.id,
    forwardId: route.forwardId,
    domain: route.domain,
    hostsEnabled: field === 'hostsEnabled' ? !!event.target.checked : !!route.hostsEnabled,
    caddyEnabled: field === 'caddyEnabled' ? !!event.target.checked : !!route.caddyEnabled,
    upstreamScheme: route.upstreamScheme || 'http',
    tlsSni: route.tlsSni || ''
  }
  // Caddy 生效的前提是 hosts 启用：开 Caddy 联动开 hosts；关 hosts 联动关 Caddy
  if (field === 'caddyEnabled' && payload.caddyEnabled) payload.hostsEnabled = true
  if (field === 'hostsEnabled' && !payload.hostsEnabled) payload.caddyEnabled = false
  try {
    await callBackend(SaveWebRoute, payload)
    emit('vault-changed')
    await previewAndApply(route.id)
  } catch (err) {
    event.target.checked = !!route[field]
    emit('notify', errorMessage(err))
  } finally {
    setRoutePending(route.id, false)
    void refreshStatus()
  }
}

function routeAppliedView(route) {
  const status = statusOf(route.id)
  if (statusPhase.value === 'checking') return { status: 'reconnecting', label: t('routes.status.checking') }
  if (!status || statusPhase.value === 'unknown') return { status: 'reconnecting', label: t('routes.status.unknown') }
  const state = status.state || status.status
  const states = {
    applied: ['running', 'routes.status.applied'],
    hosts_only: ['running', 'routes.status.hostsOnly'],
    pending: ['reconnecting', 'routes.status.pending'],
    conflict: ['busy', 'routes.status.portConflict'],
    error: ['stopped', 'routes.status.error'],
    unknown: ['reconnecting', 'routes.status.unknown'],
    cleanup_pending: ['busy', 'routes.status.cleanupPending'],
    quarantined: ['busy', 'routes.status.quarantined']
  }
  if (states[state]) return { status: states[state][0], label: t(states[state][1]) }
  if (status.portConflict) return { status: 'busy', label: t('routes.status.portConflict') }
  if (route.caddyEnabled && status.caddyRunning) return { status: 'running', label: t('routes.status.applied') }
  if (route.hostsEnabled && status.hostsApplied) return { status: 'running', label: t('routes.status.hostsOnly') }
  if (!route.hostsEnabled && !route.caddyEnabled) return { status: 'stopped', label: t('routes.status.notDesired') }
  return { status: 'reconnecting', label: t('routes.status.unknown') }
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
  try {
    await callBackend(RemoveRoute, route.id)
    emit('vault-changed')
    emit('notify', t('routes.notify.deleted', { domain: route.domain }))
  } catch (err) {
    emit('notify', errorMessage(err))
  } finally {
    void refreshStatus()
  }
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
                    :checked="route.hostsEnabled"
                    :disabled="configurationLocked || routeMutationBusy"
                    :aria-busy="pendingRouteIds.has(route.id)"
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
                    :checked="route.caddyEnabled"
                    :disabled="configurationLocked || routeMutationBusy"
                    :aria-busy="pendingRouteIds.has(route.id)"
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
    @close="routeModalOpen = false"
    @submit="saveRoute"
  />

  <ConfirmDialog
    :visible="deleteDialog.visible"
    :title="t('routes.confirmations.deleteRouteTitle')"
    :message="t('routes.confirmations.deleteRoute', { domain: deleteDialog.route?.domain || '' })"
    @confirm="confirmDeleteRoute"
    @close="deleteDialog.visible = false"
  />

  <BaseDialog :visible="dnsConfirm.visible" :title="t('routes.confirmations.dnsOverrideTitle')" :busy="dnsConfirm.busy" @close="closeDnsConfirm">
        <p class="action-dialog-message">{{ t('routes.confirmations.dnsOverrideMessage', { domains: dnsConfirm.domains.join(', ') }) }}</p>
        <table class="table table-sm align-middle mt-2 mb-2">
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
        <div class="inline-notice" role="alert">
          <i class="bi bi-exclamation-triangle" aria-hidden="true"></i>
          <span>{{ t('routes.confirmations.dnsOverrideWarning') }}</span>
        </div>
    <template #footer>
        <button type="button" class="btn btn-outline-secondary" :disabled="dnsConfirm.busy" @click="closeDnsConfirm">
          {{ t('app.common.cancel') }}
        </button>
        <button type="button" class="btn btn-warning" :disabled="dnsConfirm.busy" @click="confirmDnsOverride">
          {{ t('routes.confirmations.dnsOverrideConfirm') }}
        </button>
    </template>
  </BaseDialog>
</template>
