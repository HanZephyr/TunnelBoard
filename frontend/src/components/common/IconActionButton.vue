<script setup>
defineProps({
  buttonClass: { type: String, default: 'btn-outline-secondary' },
  title: { type: String, required: true },
  ariaLabel: { type: String, required: true },
  iconClass: { type: String, required: true },
  disabled: { type: Boolean, default: false }
})
defineEmits(['click'])
</script>

<template>
  <button type="button" class="btn btn-sm icon-btn" :class="buttonClass" :title="title" :aria-label="ariaLabel" :disabled="disabled" @click="$emit('click')">
    <i class="bi action-icon" :class="iconClass" aria-hidden="true" />
  </button>
</template>
