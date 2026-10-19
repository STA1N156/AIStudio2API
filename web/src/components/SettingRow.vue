<script setup lang="ts">
import { useI18n } from '@/i18n'
import { computed } from 'vue'

const props = defineProps<{
  label: string
  controlId?: string
  groupId?: string
  description?: string
  effect: 'immediate' | 'service' | 'management'
  changed?: boolean
}>()

const { t } = useI18n()
const longDescription = computed(() => (props.description?.length ?? 0) > 120)
const summary = computed(() =>
  longDescription.value ? props.description?.split(/[。.] /)[0]?.split('。')[0] : props.description,
)
</script>

<template>
  <div
    class="flex flex-col gap-3 border-b border-line py-5 last:border-b-0 md:flex-row md:items-start md:justify-between md:gap-8"
  >
    <div class="min-w-0 md:max-w-md">
      <div class="flex flex-wrap items-center gap-2">
        <span v-if="groupId" :id="`${groupId}-label`" class="text-sm font-medium text-gray-200">{{
          label
        }}</span>
        <label v-else :for="controlId" class="text-sm font-medium text-gray-200">{{ label }}</label>
        <span class="rounded-md bg-raised px-2 py-0.5 text-[10px] text-gray-400">{{
          t(`settings.effect.${effect}`)
        }}</span>
        <span
          v-if="changed"
          v-tooltip="t('settings.unsavedField')"
          class="h-1.5 w-1.5 rounded-full bg-amber-400"
          ><span class="sr-only">{{ t('settings.unsavedField') }}</span></span
        >
      </div>
      <p v-if="description" class="mt-2 text-xs leading-5 text-gray-400">{{ summary }}</p>
      <details v-if="longDescription" class="action-details mt-2 text-xs text-gray-500">
        <summary>{{ t('ui.advanced') }}</summary>
        <p class="mt-2 leading-5">{{ description }}</p>
      </details>
    </div>
    <div class="w-full md:w-80 md:shrink-0">
      <slot />
    </div>
  </div>
</template>
