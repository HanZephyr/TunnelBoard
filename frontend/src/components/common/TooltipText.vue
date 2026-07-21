<script setup>
defineProps({
  text: { type: String, required: true },
  className: { type: String, default: 'cell-ellipsis' }
})
</script>

<template>
  <span :class="className" :title="text">{{ text }}</span>
</template>
