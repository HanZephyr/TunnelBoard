<script setup>
import { computed } from 'vue'

const props = defineProps({
  // 转发运行状态：running | reconnecting | stopped | error
  status: {
    type: String,
    default: 'stopped'
  },
  label: {
    type: String,
    required: true
  }
})

const STATUS_TONE = {
  running: 'ok',
  reconnecting: 'warn',
  stopped: 'muted',
  error: 'danger'
}

const tone = computed(() => STATUS_TONE[props.status] || 'muted')
</script>

<template>
  <span class="status-chip" :class="`status-chip-${tone}`">
    <span class="status-chip-dot" :class="{ pulse: status === 'running' }" aria-hidden="true"></span>
    <span class="status-chip-label">{{ label }}</span>
  </span>
</template>
