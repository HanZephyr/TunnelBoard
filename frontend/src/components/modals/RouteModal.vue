<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  show: {
    type: Boolean,
    default: false
  },
  editingRouteId: {
    type: Number,
    default: null
  },
  // 由父组件持有的响应式表单对象（沿用项目既有的受控模态框模式）
  form: {
    type: Object,
    required: true
  },
  // 候选 Forward 列表（父组件已过滤为 mode=local）
  forwards: {
    type: Array,
    default: () => []
  },
  validationError: {
    type: String,
    default: ''
  }
})

defineEmits(['close', 'submit'])

const { t } = useI18n()

const forwardOptions = computed(() =>
  props.forwards.map((forward) => ({
    id: forward.id,
    label: `${forward.name} (${forward.localHost}:${forward.localPort})`
  }))
)
</script>

<template>
  <div v-if="show" class="overlay">
    <div class="dialog-card compact-dialog">
      <div class="dialog-head">
        <h3 class="dialog-title">
          {{ editingRouteId ? t('routes.modal.editTitle') : t('routes.modal.newTitle') }}
        </h3>
      </div>
      <div class="dialog-body">
        <div class="row g-2">
          <div class="col-12">
            <label class="form-label" for="routeDomain">{{ t('routes.modal.domain') }}</label>
            <input
              id="routeDomain"
              v-model="form.domain"
              type="text"
              class="form-control"
              :placeholder="t('routes.modal.domainPlaceholder')"
            />
          </div>
          <div class="col-12">
            <label class="form-label" for="routeForward">{{ t('routes.modal.forward') }}</label>
            <select id="routeForward" v-model="form.forwardId" class="form-select" :disabled="!forwardOptions.length">
              <option :value="0" disabled>{{ t('routes.modal.forwardPlaceholder') }}</option>
              <option v-for="option in forwardOptions" :key="option.id" :value="option.id">{{ option.label }}</option>
            </select>
            <div v-if="!forwardOptions.length" class="form-text">{{ t('routes.modal.noLocalForwardsHint') }}</div>
          </div>
          <div class="col-6">
            <div class="form-check form-switch mt-1">
              <input id="routeHostsEnabled" v-model="form.hostsEnabled" type="checkbox" class="form-check-input" />
              <label class="form-check-label" for="routeHostsEnabled">{{ t('routes.modal.hostsEnabled') }}</label>
            </div>
          </div>
          <div class="col-6">
            <div class="form-check form-switch mt-1">
              <input id="routeCaddyEnabled" v-model="form.caddyEnabled" type="checkbox" class="form-check-input" />
              <label class="form-check-label" for="routeCaddyEnabled">{{ t('routes.modal.caddyEnabled') }}</label>
            </div>
          </div>
          <div class="col-12 col-md-6">
            <label class="form-label" for="routeUpstreamScheme">{{ t('routes.modal.upstreamScheme') }}</label>
            <select id="routeUpstreamScheme" v-model="form.upstreamScheme" class="form-select">
              <option value="http">http</option>
              <option value="https">https</option>
            </select>
          </div>
          <div v-if="form.upstreamScheme === 'https'" class="col-12 col-md-6">
            <label class="form-label" for="routeTlsSni">{{ t('routes.modal.tlsSni') }}</label>
            <input
              id="routeTlsSni"
              v-model="form.tlsSni"
              type="text"
              class="form-control"
              :placeholder="t('routes.modal.tlsSniPlaceholder')"
            />
          </div>
        </div>
        <div v-if="validationError" class="form-error mt-2">{{ validationError }}</div>
      </div>
      <div class="dialog-footer">
        <button type="button" class="btn btn-outline-secondary" @click="$emit('close')">
          {{ t('app.common.cancel') }}
        </button>
        <button type="button" class="btn btn-primary" @click="$emit('submit')">
          {{ t('app.common.save') }}
        </button>
      </div>
    </div>
  </div>
</template>
