import { computed, onMounted, onUnmounted, ref, type Ref } from 'vue'

// 当前页面共用一个时钟，只更新显示，不发起请求。
export function useCountdown(locale: Readonly<Ref<string>>) {
  const now = ref(Date.now())
  const formatter = computed(() => new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' }))
  let timer: number | undefined
  onMounted(() => {
    timer = window.setInterval(() => {
      now.value = Date.now()
    }, 1000)
  })
  onUnmounted(() => window.clearInterval(timer))
  function remaining(until: string): string {
    const seconds = Math.max(0, Math.ceil((Date.parse(until) - now.value) / 1000))
    if (!Number.isFinite(seconds)) return '—'
    if (seconds >= 3600) return formatter.value.format(Math.round(seconds / 360) / 10, 'hour')
    if (seconds >= 60) return formatter.value.format(Math.ceil(seconds / 60), 'minute')
    return formatter.value.format(seconds, 'second')
  }
  return { now, remaining }
}
