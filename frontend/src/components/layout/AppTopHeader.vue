<script setup>
defineProps({
  currentPage: {
    type: Object,
    default: null
  },
  activePage: {
    type: String,
    required: true
  }
})

defineEmits(['new-forward', 'new-host', 'new-route'])
</script>

<template>
  <header class="top-header">
    <div class="top-header-title">
      <h1 class="page-title">{{ currentPage?.title }}</h1>
      <p class="page-subtitle">{{ currentPage?.subtitle }}</p>
    </div>
    <div class="top-header-actions">
      <button
        v-if="activePage === 'forwards'"
        type="button"
        class="btn btn-primary header-action-btn"
        @click="$emit('new-forward')"
      >
        <i class="bi bi-plus-lg" aria-hidden="true"></i>{{ $t('app.header.newForward') }}
      </button>
      <button
        v-if="activePage === 'hosts'"
        type="button"
        class="btn btn-primary header-action-btn"
        @click="$emit('new-host')"
      >
        <i class="bi bi-plus-lg" aria-hidden="true"></i>{{ $t('app.header.newHost') }}
      </button>
      <button
        v-if="activePage === 'routes'"
        type="button"
        class="btn btn-primary header-action-btn"
        @click="$emit('new-route')"
      >
        <i class="bi bi-plus-lg" aria-hidden="true"></i>{{ $t('app.header.newRoute') }}
      </button>
    </div>
  </header>
</template>
