<script>
export default { inheritAttrs: false }
</script>

<script setup>
import { nextTick, onBeforeUnmount, ref, useAttrs, watch } from 'vue'

let openDialogCount = 0

const props = defineProps({
  visible: { type: Boolean, default: false },
  title: { type: String, default: '' },
  busy: { type: Boolean, default: false },
  dismissible: { type: Boolean, default: true },
  labelledBy: { type: String, default: '' }
})
const emit = defineEmits(['close'])
const attrs = useAttrs()
const panel = ref(null)
const titleId = `dialog-title-${Math.random().toString(36).slice(2)}`
let returnFocus = null
let registeredOpen = false

function registerOpen(open) {
  if (open && !registeredOpen) {
    registeredOpen = true
    openDialogCount += 1
  } else if (!open && registeredOpen) {
    registeredOpen = false
    openDialogCount = Math.max(0, openDialogCount - 1)
  }
  const app = document.getElementById('app')
  if (openDialogCount > 0) {
    app?.setAttribute('inert', '')
    document.body.classList.add('dialog-open')
  } else {
    app?.removeAttribute('inert')
    document.body.classList.remove('dialog-open')
  }
}

function focusables() {
  return [...(panel.value?.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])') || [])]
}

function requestClose() {
  if (!props.busy && props.dismissible) emit('close')
}

function onKeydown(event) {
  if (event.key === 'Escape') {
    event.preventDefault()
    requestClose()
    return
  }
  if (event.key !== 'Tab') return
  const items = focusables()
  if (!items.length) {
    event.preventDefault()
    panel.value?.focus()
    return
  }
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(() => props.visible, async (visible) => {
  if (visible) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    registerOpen(true)
    await nextTick()
    const preferred = panel.value?.querySelector('[data-dialog-initial-focus]')
    ;(preferred || focusables()[0] || panel.value)?.focus()
  } else {
    registerOpen(false)
    if (returnFocus?.isConnected) returnFocus.focus()
    returnFocus = null
  }
})

onBeforeUnmount(() => {
  registerOpen(false)
})
</script>

<template>
  <Teleport to="body">
    <div v-if="visible" class="overlay" @keydown="onKeydown">
      <section
        v-bind="attrs"
        ref="panel"
        class="dialog-card compact-dialog"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="labelledBy || titleId"
        tabindex="-1"
      >
        <header class="dialog-head">
          <h3 :id="labelledBy || titleId" class="dialog-title">{{ title }}</h3>
          <button v-if="dismissible" type="button" class="btn-close" :aria-label="$t('app.common.close')" :disabled="busy" @click="requestClose" />
        </header>
        <div class="dialog-body"><slot /></div>
        <footer v-if="$slots.footer" class="dialog-footer"><slot name="footer" /></footer>
      </section>
    </div>
  </Teleport>
</template>
