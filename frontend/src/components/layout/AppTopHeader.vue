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
    <div>
      <h1 class="page-title">{{ currentPage?.title }}</h1>
      <p class="page-subtitle">{{ currentPage?.subtitle }}</p>
    </div>
    <div class="d-flex align-items-center gap-2">
      <button v-if="activePage === 'forwards'" type="button" class="btn btn-primary" @click="$emit('new-forward')">
        {{ $t('app.header.newForward') }}
      </button>
      <button v-if="activePage === 'hosts'" type="button" class="btn btn-primary" @click="$emit('new-host')">
        {{ $t('app.header.newHost') }}
      </button>
      <button v-if="activePage === 'routes'" type="button" class="btn btn-primary" @click="$emit('new-route')">
        {{ $t('app.header.newRoute') }}
      </button>
    </div>
  </header>
</template>
