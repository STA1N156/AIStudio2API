import { computed, onMounted, onUnmounted, ref, type Ref } from 'vue'

// 页面上所有倒计时共用一个时钟，只更新显示，不发起请求；没有倒计时显示时停止。
const now = ref(Date.now())
let users = 0
let timer: number | undefined

// useCountdown 返回剩余与已过去时间文案；调用它的组件会随时钟每秒重绘，列表里应改用 RelativeTime 组件
export function useCountdown(locale: Readonly<Ref<string>>) {
  const formatter = computed(() => new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' }))
  onMounted(() => {
    if (users++ > 0) return
    now.value = Date.now()
    timer = window.setInterval(() => {
      now.value = Date.now()
    }, 1000)
  })
  onUnmounted(() => {
    if (--users === 0) window.clearInterval(timer)
  })
  function remaining(until: string): string {
    const seconds = Math.max(0, Math.ceil((Date.parse(until) - now.value) / 1000))
    if (!Number.isFinite(seconds)) return '—'
    if (seconds >= 3600) return formatter.value.format(Math.round(seconds / 360) / 10, 'hour')
    if (seconds >= 60) return formatter.value.format(Math.ceil(seconds / 60), 'minute')
    return formatter.value.format(seconds, 'second')
  }
  // elapsed 返回从 since 到现在过去了多久，如“12 秒前”
  function elapsed(since: string): string {
    const seconds = Math.max(0, Math.floor((now.value - Date.parse(since)) / 1000))
    if (!Number.isFinite(seconds)) return '—'
    if (seconds >= 3600) return formatter.value.format(-Math.floor(seconds / 360) / 10, 'hour')
    if (seconds >= 60) return formatter.value.format(-Math.floor(seconds / 60), 'minute')
    return formatter.value.format(-seconds, 'second')
  }
  return { remaining, elapsed }
}
