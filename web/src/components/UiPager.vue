<script setup lang="ts">
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

// UiPager 是列表底部的翻页条，page 从 1 开始，只有一页时不显示
const page = defineModel<number>({ required: true })
defineProps<{ pages: number; total: number }>()
const { t } = useI18n()
</script>

<template>
  <nav v-if="pages > 1" class="mt-4 flex flex-wrap items-center gap-2 text-xs text-gray-400">
    <span class="mr-auto tabular-nums">{{
      t('ui.pageOf')
        .replace('{page}', String(page))
        .replace('{pages}', String(pages))
        .replace('{total}', String(total))
    }}</span>
    <button
      type="button"
      class="ui-button !min-h-8 !px-2.5 !py-1 !text-xs"
      :disabled="page <= 1"
      @click="page--"
    >
      <UiIcon name="chevronLeft" :size="13" />{{ t('usage.previousPage') }}
    </button>
    <button
      type="button"
      class="ui-button !min-h-8 !px-2.5 !py-1 !text-xs"
      :disabled="page >= pages"
      @click="page++"
    >
      {{ t('usage.nextPage') }}<UiIcon name="chevronRight" :size="13" />
    </button>
  </nav>
</template>
