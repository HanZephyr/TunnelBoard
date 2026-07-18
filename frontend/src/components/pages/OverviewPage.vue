<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  GetRouteStatus,
  GetRuntimeSnapshot,
  StopForward
} from '../../../wailsjs/go/main/App'
import { callBackend, errorMessage } from '../../utils/backend'
import { formatLatency as formatLatencyUtil } from '../../utils/format'
import StatusChip from '../common/StatusChip.vue'

const props = defineProps({
  folders: {
    type: Array,
    default: () => []
  },
  sshHosts: {
    type: Array,
    default: () => []
  },
  forwards: {
    type: Array,
    default: () => []
  },
  webRoutes: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['notify', 'go-forwards'])

const { t } = useI18n()

// ---- 运行时状态轮询（与 ForwardsPage 同一模式：5s，卸载清理）----
const runtimeMap = ref({})
const pendingIds = ref(new Set())
let runtimeTimer = null

function runtimeOf(forwardId) {
  return runtimeMap.value[forwardId] || { status: 'stopped', lastError: '', latencyMs: 0 }
}

function isActiveStatus(status) {
  return status === 'running' || status === 'reconnecting'
}

async function refreshRuntime() {
  try {
    const snapshot = await callBackend(GetRuntimeSnapshot)
    const next = {}
    for (const item of Array.isArray(snapshot) ? snapshot : []) {
      next[item.forwardId] = {
        status: item.status || 'stopped',
        lastError: item.lastError || '',
        latencyMs: Number(item.latencyMs) || 0
      }
    }
    runtimeMap.value = next
  } catch (_) {
    /* 保留现有状态，下一轮轮询再试 */
  }
}

// ---- Web 路由系统状态轮询（5s，卸载清理）----
const routeStatusMap = ref({})
let routeStatusTimer = null

function routeStatusOf(routeId) {
  return routeStatusMap.value[routeId] || null
}

async function refreshRouteStatus() {
  try {
    const items = await callBackend(GetRouteStatus)
    const next = {}
    for (const item of Array.isArray(items) ? items : []) {
      next[item.routeId] = item
    }
    routeStatusMap.value = next
  } catch (_) {
    /* 保留现有状态，下一轮轮询再试 */
  }
}

onMounted(() => {
  void refreshRuntime()
  runtimeTimer = window.setInterval(refreshRuntime, 5000)
  void refreshRouteStatus()
  routeStatusTimer = window.setInterval(refreshRouteStatus, 5000)
})

onBeforeUnmount(() => {
  if (runtimeTimer !== null) {
    window.clearInterval(runtimeTimer)
    runtimeTimer = null
  }
  if (routeStatusTimer !== null) {
    window.clearInterval(routeStatusTimer)
    routeStatusTimer = null
  }
})

// ---- 状态总览统计：runtime snapshot × vault 合并 ----
const statusCounts = computed(() => {
  const counts = { running: 0, reconnecting: 0, error: 0, stopped: 0 }
  for (const forward of props.forwards) {
    const status = runtimeOf(forward.id).status
    if (Object.prototype.hasOwnProperty.call(counts, status)) {
      counts[status] += 1
    } else {
      counts.stopped += 1
    }
  }
  return counts
})

const activeForwards = computed(() =>
  props.forwards.filter((forward) => isActiveStatus(runtimeOf(forward.id).status))
)

const errorForwards = computed(() =>
  props.forwards.filter((forward) => runtimeOf(forward.id).status === 'error')
)

// ---- Forward 启停（与 ForwardsPage 同一绑定、同一参数：StopForward(forward.id)）----
function setPending(forwardId, pending) {
  const next = new Set(pendingIds.value)
  if (pending) {
    next.add(forwardId)
  } else {
    next.delete(forwardId)
  }
  pendingIds.value = next
}

async function stopForward(forward) {
  if (pendingIds.value.has(forward.id)) return
  setPending(forward.id, true)
  try {
    await callBackend(StopForward, forward.id)
  } catch (err) {
    emit('notify', t('forwards.notify.stopFailed', { name: forward.name, error: errorMessage(err) }))
  } finally {
    setPending(forward.id, false)
    void refreshRuntime()
  }
}

// ---- 展示辅助 ----
function statusLabel(status) {
  const key = `forwards.status.${status}`
  const label = t(key)
  return label === key ? status : label
}

function formatLatency(latencyMs) {
  return formatLatencyUtil(latencyMs, t('app.common.none'))
}

function routeFlowLabel(forward) {
  const local = `${forward.localHost}:${forward.localPort}`
  if (forward.mode === 'dynamic') return local
  return `${local} → ${forward.remoteHost}:${forward.remotePort}`
}
</script>

<template>
  <section class="page-fade">
    <!-- 状态总览条 -->
    <div class="overview-stats">
      <div class="stat-card">
        <div class="stat-value ok">{{ statusCounts.running }}</div>
        <div class="stat-label">{{ t('forwards.status.running') }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-value warn">{{ statusCounts.reconnecting }}</div>
        <div class="stat-label">{{ t('forwards.status.reconnecting') }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-value danger">{{ statusCounts.error }}</div>
        <div class="stat-label">{{ t('forwards.status.error') }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-value muted">{{ statusCounts.stopped }}</div>
        <div class="stat-label">{{ t('forwards.status.stopped') }}</div>
      </div>
    </div>

    <!-- 运行中 Forward 实时卡片区 -->
    <div class="overview-section">
      <h2 class="overview-section-title">{{ t('overview.activeTitle') }}</h2>
      <div v-if="!activeForwards.length" class="panel-card empty-state">
        <i class="bi bi-activity empty-state-icon" aria-hidden="true"></i>
        <p class="empty-state-text">{{ t('overview.activeEmpty') }}</p>
        <button type="button" class="btn btn-primary header-action-btn" @click="emit('go-forwards')">
          <i class="bi bi-arrow-right" aria-hidden="true"></i>{{ t('overview.goForwards') }}
        </button>
      </div>
      <div v-else class="ov-list">
        <div v-for="forward in activeForwards" :key="forward.id" class="ov-forward-card">
          <StatusChip
            :status="runtimeOf(forward.id).status"
            :label="statusLabel(runtimeOf(forward.id).status)"
          />
          <span class="ov-forward-name" :title="forward.name">{{ forward.name }}</span>
          <span class="font-mono ov-forward-route" :title="routeFlowLabel(forward)">
            {{ routeFlowLabel(forward) }}
          </span>
          <span class="ov-forward-latency">{{ formatLatency(runtimeOf(forward.id).latencyMs) }}</span>
          <button
            type="button"
            class="btn icon-ghost-btn danger"
            :disabled="pendingIds.has(forward.id)"
            :title="t('forwards.actions.stop')"
            :aria-label="t('forwards.actions.stop')"
            @click="stopForward(forward)"
          >
            <span v-if="pendingIds.has(forward.id)" class="spinner-border spinner-border-sm" aria-hidden="true"></span>
            <i v-else class="bi bi-stop-fill" aria-hidden="true"></i>
          </button>
          <span
            v-if="runtimeOf(forward.id).status === 'error' && runtimeOf(forward.id).lastError"
            class="ov-forward-error cell-ellipsis"
            :title="runtimeOf(forward.id).lastError"
          >
            {{ runtimeOf(forward.id).lastError }}
          </span>
        </div>
      </div>
    </div>

    <!-- Web 路由状态区 -->
    <div class="overview-section">
      <h2 class="overview-section-title">{{ t('overview.routesTitle') }}</h2>
      <div v-if="!props.webRoutes.length" class="ov-empty-text">{{ t('overview.routesEmpty') }}</div>
      <div v-else class="ov-list">
        <div v-for="route in props.webRoutes" :key="route.id" class="ov-route-card">
          <span class="ov-route-domain" :title="route.domain">{{ route.domain }}</span>
          <div class="ov-route-flags">
            <span v-if="route.hostsEnabled" class="ov-status-dot" :class="{ ok: routeStatusOf(route.id)?.hostsApplied }">
              {{
                routeStatusOf(route.id)?.hostsApplied
                  ? t('routes.status.hostsApplied')
                  : t('routes.status.hostsNotApplied')
              }}
            </span>
            <span v-if="route.caddyEnabled" class="ov-status-dot" :class="{ ok: routeStatusOf(route.id)?.caddyRunning }">
              {{
                routeStatusOf(route.id)?.caddyRunning
                  ? t('routes.status.caddyRunning')
                  : t('routes.status.caddyStopped')
              }}
            </span>
            <span v-if="routeStatusOf(route.id)?.portConflict" class="ov-status-dot warn">
              {{ t('routes.status.portConflict') }}
            </span>
            <span v-if="!route.hostsEnabled && !route.caddyEnabled && !routeStatusOf(route.id)?.portConflict">
              {{ t('app.common.none') }}
            </span>
          </div>
        </div>
      </div>
    </div>

    <!-- 最近错误区（无错误时整块不显示） -->
    <div v-if="errorForwards.length" class="overview-section">
      <h2 class="overview-section-title">{{ t('overview.errorsTitle') }}</h2>
      <div class="ov-list">
        <div v-for="forward in errorForwards" :key="forward.id" class="ov-error-card">
          <span class="ov-forward-name" :title="forward.name">{{ forward.name }}</span>
          <span class="ov-error-text" :title="runtimeOf(forward.id).lastError">
            {{ runtimeOf(forward.id).lastError }}
          </span>
          <button type="button" class="ov-link-btn" @click="emit('go-forwards')">
            {{ t('overview.goEdit') }}
          </button>
        </div>
      </div>
    </div>
  </section>
</template>
