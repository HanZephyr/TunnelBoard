<script setup>
defineProps({
  pages: {
    type: Array,
    required: true
  },
  activePage: {
    type: String,
    required: true
  },
  appVersion: {
    type: String,
    required: true
  },
  hasNewVersion: {
    type: Boolean,
    default: false
  },
  collapsed: {
    type: Boolean,
    default: false
  }
})

defineEmits(['switch-page', 'open-release-page', 'toggle-collapse'])
</script>

<template>
  <aside class="sidebar-panel" :class="{ collapsed }">
    <div class="sidebar-main">
      <div class="brand-logo-wrap">
        <div class="brand-meta">
          <div class="brand-title">{{ $t('app.title') }}</div>
          <div class="brand-subtitle">{{ $t('app.sidebar.subtitle') }}</div>
        </div>
        <button
          type="button"
          class="btn sidebar-collapse-btn"
          :title="collapsed ? $t('app.sidebar.expand') : $t('app.sidebar.collapse')"
          :aria-label="collapsed ? $t('app.sidebar.expand') : $t('app.sidebar.collapse')"
          @click="$emit('toggle-collapse')"
        >
          <i class="bi" :class="collapsed ? 'bi-layout-sidebar-inset' : 'bi-layout-sidebar'" aria-hidden="true" />
        </button>
      </div>

      <div class="nav-list mt-4">
        <button
          v-for="page in pages"
          :key="page.key"
          type="button"
          class="btn nav-item-btn"
          :class="{ active: activePage === page.key }"
          :title="collapsed ? page.title : ''"
          :aria-label="page.title"
          @click="$emit('switch-page', page.key)"
        >
          <i class="bi nav-item-icon" :class="page.icon" aria-hidden="true" />
          <span class="nav-item-label">
            <span v-if="!collapsed">{{ page.title }}</span>
            <span v-if="page.beta" class="nav-beta-tag">Beta</span>
          </span>
        </button>
      </div>
    </div>

    <div class="sidebar-footer">
      <span class="sidebar-version" :class="{ compact: collapsed }">
        <template v-if="!collapsed">v{{ appVersion }}</template>
        <template v-else>v</template>
        <span v-if="hasNewVersion" class="version-new-badge" @click="$emit('open-release-page')">new</span>
      </span>
    </div>
  </aside>
</template>
