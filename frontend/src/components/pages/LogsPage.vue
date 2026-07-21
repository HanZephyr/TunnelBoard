<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { GetLogTailV2 } from '../../../wailsjs/go/main/App'
import { callBackend, errorMessage } from '../../utils/backend'
import { mergeLogTail, normalizeLogCursor } from '../../modules/logTailCursor'

const { t } = useI18n()

const MAX_LINES = 2000
const POLL_INTERVAL_MS = 2000
const BOTTOM_STICK_THRESHOLD_PX = 32
const JSON_LEVEL_ERROR = /"level"\s*:\s*"error"/
const JSON_LEVEL_WARN = /"level"\s*:\s*"warn"/

// ---- 视图状态（本地 ref，随页面 v-if 卸载即销毁）----
const source = ref('tunnelboard') // 'tunnelboard' | 'caddy'
const paused = ref(false)
const autoScroll = ref(true)
const lines = ref([])
const loadError = ref('')
const stuckToBottom = ref(true)
const logViewRef = ref(null)

let cursor = null
let polling = false
let pollTimer = null

function lineLevelClass(line) {
  const text = String(line ?? '')
  if (text.includes('ERROR') || JSON_LEVEL_ERROR.test(text)) return 'log-line-error'
  if (text.includes('WARN') || JSON_LEVEL_WARN.test(text)) return 'log-line-warn'
  return ''
}

function isNearBottom() {
  const el = logViewRef.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight <= BOTTOM_STICK_THRESHOLD_PX
}

function scrollToBottom() {
  const el = logViewRef.value
  if (el) {
    el.scrollTop = el.scrollHeight
  }
}

function onLogScroll() {
  // 用户手动上翻时脱离底部（追加继续，不打断），回到底部后恢复跟随
  stuckToBottom.value = isNearBottom()
}

async function applyTail(result) {
  const shouldStick = autoScroll.value && stuckToBottom.value
  lines.value = mergeLogTail(lines.value, result, MAX_LINES)
  cursor = normalizeLogCursor(result)
  if (shouldStick) {
    await nextTick()
    scrollToBottom()
  }
}

async function poll() {
  if (polling || paused.value) return
  polling = true
  const requestedSource = source.value
  try {
    const result = await callBackend(GetLogTailV2, requestedSource, cursor)
    // 轮询期间用户切换了日志源：丢弃迟到结果，等下一轮按新 cursor 拉取
    if (requestedSource !== source.value) return
    loadError.value = ''
    await applyTail(result)
  } catch (err) {
    // 保留已有内容，仅在视图顶部提示一次；下一轮成功会自动消除
    if (requestedSource === source.value) {
      loadError.value = errorMessage(err)
    }
  } finally {
    polling = false
  }
}

function switchSource(name) {
  if (source.value === name) return
  source.value = name
  cursor = null
  lines.value = []
  loadError.value = ''
  stuckToBottom.value = true
  void poll()
}

function togglePaused() {
  paused.value = !paused.value
  if (!paused.value) {
    void poll()
  }
}

// 只清空视图，不触碰日志文件；cursor 保持不变，后续只追加新行
function clearView() {
  lines.value = []
}

watch(autoScroll, (enabled) => {
  if (enabled) {
    stuckToBottom.value = true
    void nextTick(scrollToBottom)
  }
})

onMounted(() => {
  void poll()
  pollTimer = window.setInterval(poll, POLL_INTERVAL_MS)
})

onBeforeUnmount(() => {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
})
</script>

<template>
  <section class="page-fade logs-page">
    <div class="panel-card logs-panel">
      <div class="panel-head logs-toolbar">
        <div class="btn-group btn-group-sm" role="group" :aria-label="t('logs.sourceLabel')">
          <button
            type="button"
            class="btn"
            :class="source === 'tunnelboard' ? 'btn-primary' : 'btn-outline-primary'"
            @click="switchSource('tunnelboard')"
          >
            <i class="bi bi-app me-1" aria-hidden="true"></i>{{ t('logs.source.app') }}
          </button>
          <button
            type="button"
            class="btn"
            :class="source === 'caddy' ? 'btn-primary' : 'btn-outline-primary'"
            @click="switchSource('caddy')"
          >
            <i class="bi bi-globe2 me-1" aria-hidden="true"></i>{{ t('logs.source.caddy') }}
          </button>
        </div>

        <div class="logs-toolbar-actions">
          <button type="button" class="btn btn-sm btn-outline-secondary" @click="togglePaused">
            <i class="bi me-1" :class="paused ? 'bi-play-fill' : 'bi-pause-fill'" aria-hidden="true"></i>{{ paused ? t('logs.resume') : t('logs.pause') }}
          </button>
          <button type="button" class="btn btn-sm btn-outline-secondary" @click="clearView">
            <i class="bi bi-eraser me-1" aria-hidden="true"></i>{{ t('logs.clear') }}
          </button>
          <div class="form-check form-switch mb-0">
            <input
              id="logs-auto-scroll"
              v-model="autoScroll"
              type="checkbox"
              class="form-check-input"
              :aria-label="t('logs.autoScroll')"
            />
            <label class="form-check-label logs-switch-label" for="logs-auto-scroll">{{ t('logs.autoScroll') }}</label>
          </div>
        </div>
      </div>

      <div v-if="loadError" class="log-warning" role="alert">
        <i class="bi bi-exclamation-triangle" aria-hidden="true"></i>
        <span class="log-warning-text">{{ t('logs.loadFailed', { error: loadError }) }}</span>
        <button
          type="button"
          class="btn-close"
          :aria-label="t('app.common.close')"
          @click="loadError = ''"
        />
      </div>

      <div ref="logViewRef" class="log-view" @scroll.passive="onLogScroll">
        <div v-if="!lines.length" class="log-empty">{{ t('logs.empty') }}</div>
        <div v-for="(line, index) in lines" :key="index" class="log-line" :class="lineLevelClass(line)">{{ line }}</div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.logs-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.logs-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.logs-toolbar {
  flex-wrap: wrap;
}

.logs-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.logs-switch-label {
  font-size: 13px;
  color: var(--tb-ink);
}

.log-warning {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
  padding: 8px 10px;
  border: 1px solid color-mix(in srgb, var(--tb-warn) 35%, transparent);
  border-radius: var(--tb-radius-ctl);
  background: color-mix(in srgb, var(--tb-warn) 12%, transparent);
  color: var(--tb-warn);
  font-size: 12.5px;
}

.log-warning-text {
  min-width: 0;
  word-break: break-all;
}

.log-warning .btn-close {
  margin-left: auto;
  flex: 0 0 auto;
  font-size: 11px;
}

.log-view {
  flex: 1;
  min-height: 0;
  overflow: auto;
  border: 1px solid var(--tb-border);
  border-radius: var(--tb-radius-ctl);
  background: var(--tb-console-bg);
  padding: 10px 12px;
  font-family: var(--tb-mono);
  font-size: 12px;
  line-height: 1.5;
}

.log-line {
  white-space: pre-wrap;
  word-break: break-all;
  color: var(--tb-console-ink);
}

.log-line.log-line-error {
  color: var(--tb-console-danger);
}

.log-line.log-line-warn {
  color: var(--tb-console-warn);
}

.log-empty {
  color: var(--tb-ink-3);
  font-size: 13px;
}
</style>
