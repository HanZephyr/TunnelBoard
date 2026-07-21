<script>
export default { inheritAttrs: false }
</script>

<script setup>
import { markRaw, nextTick, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  loader: { type: Function, required: true },
  pageName: { type: String, required: true }
})
const emit = defineEmits(['open-diagnostics'])
const { t } = useI18n()
const component = shallowRef(null)
const pageRef = ref(null)
const phase = ref('loading')
const error = ref('')
const container = ref(null)
let generation = 0
let focusAfterLoad = false

async function load() {
  const request = ++generation
  phase.value = 'loading'
  error.value = ''
  try {
    const loaded = await props.loader()
    if (request !== generation) return
    component.value = markRaw(loaded?.default || loaded)
    phase.value = 'ready'
    if (focusAfterLoad) {
      focusAfterLoad = false
      await nextTick()
      const heading = container.value?.querySelector('h1, h2')
      if (heading instanceof HTMLElement) {
        heading.setAttribute('tabindex', '-1')
        heading.focus()
      }
    }
  } catch (reason) {
    if (request !== generation) return
    phase.value = 'error'
    error.value = reason instanceof Error ? reason.message : String(reason)
  }
}

function retry() {
  focusAfterLoad = true
  void load()
}

watch(() => props.loader, load)
onMounted(load)

defineExpose({
  openNewForward: (...args) => pageRef.value?.openNewForward?.(...args),
  openNewHost: (...args) => pageRef.value?.openNewHost?.(...args),
  openNewRoute: (...args) => pageRef.value?.openNewRoute?.(...args)
})
</script>

<template>
  <div ref="container" class="page-loader">
    <div v-if="phase === 'loading'" class="panel-card" role="status" aria-live="polite">
      <div class="placeholder-glow">
        <span class="placeholder col-4"></span>
        <span class="placeholder col-8 mt-3"></span>
      </div>
      <span class="visually-hidden">{{ t('app.pageLoader.loading', { page: pageName }) }}</span>
    </div>
    <div v-else-if="phase === 'error'" class="alert alert-danger" role="alert">
      <div>{{ t('app.pageLoader.loadFailed', { page: pageName }) }}：{{ error }}</div>
      <div class="d-flex flex-wrap gap-2 mt-2">
        <button type="button" class="btn btn-sm btn-outline-danger" @click="retry">{{ t('app.pageLoader.retry') }}</button>
        <button type="button" class="btn btn-sm btn-outline-secondary" @click="emit('open-diagnostics')">{{ t('app.pageLoader.diagnostics') }}</button>
      </div>
    </div>
    <component :is="component" v-else-if="component" ref="pageRef" v-bind="$attrs" />
  </div>
</template>

<style scoped>
.page-loader {
  min-height: 0;
  height: 100%;
}
</style>
