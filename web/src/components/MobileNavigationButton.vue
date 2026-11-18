<script setup lang="ts">
import { DialogTrigger } from 'reka-ui'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

const { t } = useI18n()
</script>

<template>
  <DialogTrigger
    class="flex size-10 shrink-0 items-center justify-center rounded hover:bg-raised md:hidden"
    :aria-label="t('nav.open')"
  >
    <UiIcon name="menu" :size="22" />
  </DialogTrigger>
</template>
