<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import TokenCountTooltip from './TokenCountTooltip.vue'
import { useMediaQuery } from './media-query'
import { useCompare, usePlayground } from './session'

defineProps<{ settingsOpen: boolean; narrow: boolean }>()
const emit = defineEmits<{ newChat: []; toggleSettings: [] }>()
const { t } = useI18n()
const { session } = usePlayground()
const { comparing, enterCompare, toggleRaw } = useCompare()
const handset = useMediaQuery('(max-width: 768px)')
const hasPrompt = computed(
  () =>
    session.turns.length > 0 ||
    session.attachments.length > 0 ||
    session.settings.systemInstruction.trim() !== '',
)
</script>

<template>
  <header class="ms-header">
    <div class="toolbar-left">
      <h1 class="mode-title">{{ t('nav.playground') }}</h1>
      <TokenCountTooltip v-if="!comparing" />
    </div>
    <div class="toolbar-right">
      <button
        v-if="!comparing && !handset"
        v-tooltip.below="t('pg.compareMode')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.compareMode')"
        @click="enterCompare"
      >
        <UiIcon name="compare" :size="20" />
      </button>
      <button
        v-tooltip.below="hasPrompt ? t('pg.newChat') : t('pg.alreadyNew')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.newChat')"
        :disabled="!hasPrompt"
        @click="emit('newChat')"
      >
        <UiIcon name="add" :size="20" />
      </button>
      <button
        v-tooltip.below="t('pg.rawMode')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.rawMode')"
        :aria-pressed="session.rawMode"
        @click="toggleRaw"
      >
        <UiIcon name="rawMode" :size="20" />
      </button>
      <button
        v-if="!comparing && (narrow || !settingsOpen)"
        v-tooltip.below="t('pg.toggleSettings')"
        type="button"
        ms-button
        class="ms-button-primary ms-button-icon"
        :aria-label="t('pg.toggleSettings')"
        @click="emit('toggleSettings')"
      >
        <UiIcon name="options" :size="20" />
      </button>
    </div>
  </header>
</template>

<style scoped>
.toolbar-left,
.toolbar-right {
  display: flex;
  align-items: center;
  gap: 4px;
}
.toolbar-left {
  min-width: 0;
}
.toolbar-right {
  grid-column: 3;
}
.mode-title {
  margin: 0 12px;
  overflow: hidden;
  font-size: 16px;
  font-weight: 500;
  line-height: 24px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
