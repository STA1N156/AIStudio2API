<script setup lang="ts">
import { useCountdown } from '@/countdown'
import { useI18n } from '@/i18n'

// RelativeTime 单独随时钟重绘：传 until 显示剩余时间，传 since 显示已过去的时间，所在列表不必每秒整体重绘
defineProps<{ until?: string; since?: string }>()
const { locale } = useI18n()
const { remaining, elapsed } = useCountdown(locale)
</script>

<template>{{ since === undefined ? remaining(until ?? '') : elapsed(since) }}</template>
