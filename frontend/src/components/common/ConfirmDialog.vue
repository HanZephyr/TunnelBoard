<script setup>
defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  title: {
    type: String,
    default: ''
  },
  message: {
    type: String,
    default: ''
  },
  confirmLabel: {
    type: String,
    default: ''
  },
  confirmClass: {
    type: String,
    default: 'btn-danger'
  },
  showCancel: {
    type: Boolean,
    default: true
  }
})

const emit = defineEmits(['confirm', 'close'])
</script>

<template>
  <div v-if="visible" class="overlay">
    <div class="dialog-card compact-dialog action-confirm-dialog">
      <div class="dialog-head">
        <h3 class="dialog-title">{{ title || $t('app.common.confirm') }}</h3>
      </div>
      <div class="dialog-body">
        <p class="action-dialog-message mb-0">{{ message }}</p>
      </div>
      <div class="dialog-footer">
        <button
          v-if="showCancel"
          type="button"
          class="btn btn-outline-secondary"
          @click="emit('close')"
        >
          {{ $t('app.common.cancel') }}
        </button>
        <button type="button" class="btn" :class="confirmClass" @click="emit('confirm')">
          {{ confirmLabel || $t('app.common.confirm') }}
        </button>
      </div>
    </div>
  </div>
</template>
