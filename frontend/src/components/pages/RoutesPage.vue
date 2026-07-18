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

const props = defineProps({
  forwards: {
    type: Array,
    default: () => []
  },
  webRoutes: {
    type: Array,
    default: () => []
  }
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

function upstreamLabel(route) {
  if (route.upstreamScheme === 'https') {
    return `https → ${route.tlsSni || ''}`
  }
  return 'http'
}

// ---- 系统状态（GetRouteStatus 轮询合并）----
const statusMap = ref({})
let statusTimer = null

function statusOf(routeId) {
  return statusMap.value[routeId] || null
}

async function refreshStatus() {
  try {
    const items = await callBackend(GetRouteStatus)
    const next = {}
    for (const item of Array.isArray(items) ? items : []) {
      next[item.routeId] = item
    }
    statusMap.value = next
  } catch (_) {
    /* 后端暂不可用时保留现有状态，下一轮轮询再试 */
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

function setRoutePending(routeId, pending) {
  const next = new Set(pendingRouteIds.value)
  if (pending) {
    next.add(routeId)
  } else {
    next.delete(routeId)
  }
  pendingRouteIds.value = next
}

async function toggleRouteFlag(route, field) {
  if (pendingRouteIds.value.has(route.id)) return
  setRoutePending(route.id, true)
  const payload = {
    id: route.id,
    forwardId: route.forwardId,
    domain: route.domain,
    hostsEnabled: field === 'hostsEnabled' ? !route.hostsEnabled : !!route.hostsEnabled,
    caddyEnabled: field === 'caddyEnabled' ? !route.caddyEnabled : !!route.caddyEnabled,
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
    emit('notify', errorMessage(err))
  } finally {
    setRoutePending(route.id, false)
    void refreshStatus()
  }
}

// ---- Route 删除（后端负责撤销 hosts / Caddy）----
const deleteDialog = reactive({
  visible: false,
  route: null
})

function deleteRoute(route) {
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
          @click="openNewRoute"
        >
          <i class="bi bi-plus-lg" aria-hidden="true"></i>
        </button>
      </div>

      <div v-if="!sortedRoutes.length" class="empty-state">
        <i class="bi bi-globe2 empty-state-icon" aria-hidden="true"></i>
        <p class="empty-state-text">{{ t('routes.empty') }}</p>
        <button type="button" class="btn btn-primary header-action-btn" @click="openNewRoute">
          <i class="bi bi-plus-lg" aria-hidden="true"></i>{{ t('app.header.newRoute') }}
        </button>
      </div>

      <div v-else class="page-table-wrap routes-table-wrap">
        <table class="table routes-table align-middle mb-0">
          <thead>
            <tr>
              <th>{{ t('routes.table.domain') }}</th>
              <th>{{ t('routes.table.forward') }}</th>
              <th class="route-switch-cell">{{ t('routes.table.hosts') }}</th>
              <th class="route-switch-cell">{{ t('routes.table.caddy') }}</th>
              <th class="route-upstream-cell">{{ t('routes.table.upstream') }}</th>
              <th>{{ t('routes.table.systemStatus') }}</th>
              <th class="routes-action-cell">{{ t('routes.table.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="route in sortedRoutes" :key="route.id">
              <td class="route-domain-cell"><TooltipText :text="route.domain" /></td>
              <td><TooltipText :text="forwardName(route.forwardId)" /></td>
              <td>
                <div class="form-check form-switch mb-0">
                  <input
                    type="checkbox"
                    class="form-check-input"
                    :checked="route.hostsEnabled"
                    :disabled="pendingRouteIds.has(route.id)"
                    :aria-label="t('routes.table.hosts')"
                    @change="toggleRouteFlag(route, 'hostsEnabled')"
                  />
                </div>
              </td>
              <td>
                <div class="form-check form-switch mb-0">
                  <input
                    type="checkbox"
                    class="form-check-input"
                    :checked="route.caddyEnabled"
                    :disabled="pendingRouteIds.has(route.id)"
                    :aria-label="t('routes.table.caddy')"
                    @change="toggleRouteFlag(route, 'caddyEnabled')"
                  />
                </div>
              </td>
              <td>
                <span
                  class="status-badge font-mono chip-ellipsis"
                  :class="{ running: route.upstreamScheme === 'https' }"
                  :title="upstreamLabel(route)"
                >
                  {{ upstreamLabel(route) }}
                </span>
              </td>
              <td>
                <div class="d-flex align-items-center gap-1 flex-wrap">
                  <StatusChip
                    v-if="route.hostsEnabled"
                    :status="statusOf(route.id)?.hostsApplied ? 'running' : 'stopped'"
                    :label="
                      statusOf(route.id)?.hostsApplied
                        ? t('routes.status.hostsApplied')
                        : t('routes.status.hostsNotApplied')
                    "
                  />
                  <StatusChip
                    v-if="route.caddyEnabled"
                    :status="statusOf(route.id)?.caddyRunning ? 'running' : 'stopped'"
                    :label="
                      statusOf(route.id)?.caddyRunning
                        ? t('routes.status.caddyRunning')
                        : t('routes.status.caddyStopped')
                    "
                  />
                  <span v-if="statusOf(route.id)?.portConflict" class="status-badge busy">
                    <i class="bi bi-exclamation-triangle me-1" aria-hidden="true"></i>{{ t('routes.status.portConflict') }}
                  </span>
                  <i
                    v-if="statusOf(route.id)?.caTrusted"
                    class="bi bi-shield-lock-fill ca-trusted-icon"
                    :title="t('routes.status.caTrusted')"
                    aria-hidden="true"
                  ></i>
                  <span v-if="!route.hostsEnabled && !route.caddyEnabled && !statusOf(route.id)?.portConflict">
                    {{ t('app.common.none') }}
                  </span>
                </div>
              </td>
              <td>
                <div class="row-actions">
                  <IconActionButton
                    icon-class="bi-pencil"
                    button-class="icon-ghost-btn"
                    :title="t('app.common.edit')"
                    :aria-label="t('app.common.edit')"
                    @click="editRoute(route)"
                  />
                  <IconActionButton
                    icon-class="bi-trash3"
                    button-class="icon-ghost-btn danger"
                    :title="t('app.common.delete')"
                    :aria-label="t('app.common.delete')"
                    @click="deleteRoute(route)"
                  />
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
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

  <div v-if="dnsConfirm.visible" class="overlay">
    <div class="dialog-card compact-dialog">
      <div class="dialog-head">
        <h3 class="dialog-title">{{ t('routes.confirmations.dnsOverrideTitle') }}</h3>
      </div>
      <div class="dialog-body">
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
      </div>
      <div class="dialog-footer">
        <button type="button" class="btn btn-outline-secondary" :disabled="dnsConfirm.busy" @click="closeDnsConfirm">
          {{ t('app.common.cancel') }}
        </button>
        <button type="button" class="btn btn-warning" :disabled="dnsConfirm.busy" @click="confirmDnsOverride">
          {{ t('routes.confirmations.dnsOverrideConfirm') }}
        </button>
      </div>
    </div>
  </div>
</template>
