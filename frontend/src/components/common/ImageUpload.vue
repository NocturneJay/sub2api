<template>
  <div class="flex items-start gap-4">
    <!-- Preview Box -->
    <div class="flex-shrink-0">
      <div
        class="flex items-center justify-center overflow-hidden rounded-xl border-2 border-dashed border-gray-300 bg-gray-50 dark:border-dark-600 dark:bg-dark-800"
        :class="[previewSizeClass, { 'border-solid': !!modelValue }]"
      >
        <!-- SVG mode: render inline -->
        <span
          v-if="mode === 'svg' && modelValue"
          class="text-gray-600 dark:text-gray-300 [&>svg]:h-full [&>svg]:w-full"
          :class="innerSizeClass"
          v-html="sanitizedValue"
        ></span>
        <!-- Image mode: show as img -->
        <img
          v-else-if="mode === 'image' && modelValue"
          :src="modelValue"
          alt=""
          class="h-full w-full object-contain"
        />
        <!-- Empty placeholder -->
        <svg
          v-else
          class="text-gray-400 dark:text-dark-500"
          :class="placeholderSizeClass"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="1.5"
            d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"
          />
        </svg>
      </div>
    </div>

    <!-- Controls -->
    <div class="flex-1 space-y-2">
      <div class="flex items-center gap-2">
        <label class="btn btn-secondary btn-sm cursor-pointer">
          <input
            type="file"
            :accept="acceptTypes"
            class="hidden"
            @change="handleUpload"
          />
          <Icon name="upload" size="sm" class="mr-1.5" :stroke-width="2" />
          {{ resolvedUploadLabel }}
        </label>
        <button
          v-if="modelValue"
          type="button"
          class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
          @click="$emit('update:modelValue', '')"
        >
          <Icon name="trash" size="sm" class="mr-1.5" :stroke-width="2" />
          {{ resolvedRemoveLabel }}
        </button>
      </div>
      <p v-if="hint" class="text-xs text-gray-500 dark:text-gray-400">{{ hint }}</p>
      <div v-if="allowUrl && mode === 'image'" class="space-y-1.5 pt-1">
        <label class="block text-xs font-medium text-gray-600 dark:text-gray-300">
          {{ urlLabel }}
        </label>
        <div class="flex items-center gap-2">
          <input
            v-model="urlValue"
            type="url"
            inputmode="url"
            autocomplete="url"
            class="input min-w-0 flex-1 font-mono text-xs"
            :placeholder="urlPlaceholder"
            @keydown.enter.prevent="applyUrl"
          />
          <button
            type="button"
            data-testid="apply-image-url"
            class="btn btn-secondary btn-sm flex-shrink-0"
            @click="applyUrl"
          >
            <Icon name="check" size="sm" class="mr-1.5" :stroke-width="2" />
            {{ t('common.confirm') }}
          </button>
        </div>
        <p v-if="urlHint" class="text-xs text-gray-500 dark:text-gray-400">{{ urlHint }}</p>
      </div>
      <p v-if="error" class="text-xs text-red-500">{{ error }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeSvg } from '@/utils/sanitize'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  modelValue: string
  mode?: 'image' | 'svg'
  size?: 'sm' | 'md'
  uploadLabel?: string
  removeLabel?: string
  hint?: string
  maxSize?: number // bytes
  allowUrl?: boolean
  urlLabel?: string
  urlPlaceholder?: string
  urlHint?: string
}>(), {
  mode: 'image',
  size: 'md',
  uploadLabel: '',
  removeLabel: '',
  hint: '',
  maxSize: 300 * 1024,
  allowUrl: false,
  urlLabel: '',
  urlPlaceholder: '',
  urlHint: '',
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const error = ref('')
const urlValue = ref(props.modelValue.startsWith('data:image/') ? '' : props.modelValue)

const resolvedUploadLabel = computed(() => props.uploadLabel || t('common.upload'))
const resolvedRemoveLabel = computed(() => props.removeLabel || t('common.remove'))

const acceptTypes = computed(() => props.mode === 'svg' ? '.svg' : 'image/*')

const sanitizedValue = computed(() =>
  props.mode === 'svg' ? sanitizeSvg(props.modelValue ?? '') : ''
)

const previewSizeClass = computed(() => props.size === 'sm' ? 'h-14 w-14' : 'h-20 w-20')
const innerSizeClass = computed(() => props.size === 'sm' ? 'h-7 w-7' : 'h-12 w-12')
const placeholderSizeClass = computed(() => props.size === 'sm' ? 'h-5 w-5' : 'h-8 w-8')

watch(
  () => props.modelValue,
  (value) => {
    urlValue.value = value.startsWith('data:image/') ? '' : value
  }
)

function normalizeImageUrl(value: string): string {
  const trimmed = value.trim()
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) {
    return trimmed
  }

  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'https:' ? parsed.toString() : ''
  } catch {
    return ''
  }
}

function applyUrl() {
  error.value = ''
  const normalized = normalizeImageUrl(urlValue.value)
  if (!normalized) {
    error.value = t('common.invalidImageUrl')
    return
  }

  urlValue.value = normalized
  emit('update:modelValue', normalized)
}

function handleUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  error.value = ''

  if (!file) return

  if (props.maxSize && file.size > props.maxSize) {
    error.value = t('common.fileTooLargeKb', {
      size: (file.size / 1024).toFixed(1),
      max: (props.maxSize / 1024).toFixed(0)
    })
    input.value = ''
    return
  }

  const reader = new FileReader()
  if (props.mode === 'svg') {
    reader.onload = (e) => {
      const text = e.target?.result as string
      if (text) emit('update:modelValue', text.trim())
    }
    reader.readAsText(file)
  } else {
    if (!file.type.startsWith('image/')) {
      error.value = t('common.selectImageFile')
      input.value = ''
      return
    }
    reader.onload = (e) => {
      emit('update:modelValue', e.target?.result as string)
      urlValue.value = ''
    }
    reader.readAsDataURL(file)
  }

  reader.onerror = () => {
    error.value = t('common.fileReadFailed')
  }
  input.value = ''
}
</script>
