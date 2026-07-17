<script setup>
import { computed } from 'vue'

const props = defineProps({
  show: {
    type: Boolean,
    default: false
  },
  // unknown: 首次连接（TOFU）；mismatch: 指纹与已存记录不一致
  kind: {
    type: String,
    default: 'unknown'
  },
  host: {
    type: String,
    default: ''
  },
  port: {
    type: Number,
    default: 0
  },
  fingerprint: {
    type: String,
    default: ''
  },
  storedFingerprint: {
    type: String,
    default: ''
  },
  busy: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['confirm', 'cancel'])

const isMismatch = computed(() => props.kind === 'mismatch')
const endpoint = computed(() => `${props.host}:${props.port}`)
</script>

<template>
  <div v-if="show" class="overlay">
    <div class="dialog-card compact-dialog action-confirm-dialog hostkey-dialog">
      <div class="dialog-head">
        <h3 class="dialog-title" :class="{ 'hostkey-title-danger': isMismatch }">
          <i
            class="bi me-1"
            :class="isMismatch ? 'bi-exclamation-triangle-fill' : 'bi-shield-lock'"
            aria-hidden="true"
          ></i>
          {{ isMismatch ? $t('forwards.hostkey.mismatchTitle') : $t('forwards.hostkey.unknownTitle') }}
        </h3>
      </div>
      <div class="dialog-body">
        <p class="action-dialog-message">
          {{
            isMismatch
              ? $t('forwards.hostkey.mismatchMessage', { endpoint })
              : $t('forwards.hostkey.unknownMessage', { endpoint })
          }}
        </p>

        <div v-if="isMismatch" class="hostkey-fingerprint-grid">
          <div class="hostkey-fingerprint-block">
            <div class="hostkey-fingerprint-label">{{ $t('forwards.hostkey.storedFingerprint') }}</div>
            <div class="hostkey-fingerprint-value">{{ storedFingerprint || '--' }}</div>
          </div>
          <div class="hostkey-fingerprint-block">
            <div class="hostkey-fingerprint-label">{{ $t('forwards.hostkey.newFingerprint') }}</div>
            <div class="hostkey-fingerprint-value danger">{{ fingerprint || '--' }}</div>
          </div>
        </div>
        <div v-else class="hostkey-fingerprint-block">
          <div class="hostkey-fingerprint-label">{{ $t('forwards.hostkey.fingerprint') }}</div>
          <div class="hostkey-fingerprint-value">{{ fingerprint || '--' }}</div>
        </div>
      </div>
      <div class="dialog-footer">
        <button type="button" class="btn btn-outline-secondary" :disabled="busy" @click="emit('cancel')">
          {{ $t('app.common.cancel') }}
        </button>
        <button
          type="button"
          class="btn"
          :class="isMismatch ? 'btn-danger' : 'btn-primary'"
          :disabled="busy"
          @click="emit('confirm')"
        >
          <span v-if="busy" class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>
          {{ isMismatch ? $t('forwards.hostkey.replaceAndStart') : $t('forwards.hostkey.trustAndStart') }}
        </button>
      </div>
    </div>
  </div>
</template>
