<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  onMounted,
  onUnmounted,
  ref,
  useTemplateRef,
  watch,
} from 'vue'
import type { Exposed } from 'vue-echarts'
import { useI18n } from '@/i18n'
import { afterTask } from '@/lazy'
import { useTheme } from '@/theme'
import type { UsageDimension, UsageReport } from '@/types'
import UiSelect from '../UiSelect.vue'
import ChartLegend from './ChartLegend.vue'
import { useUsageFormat } from './format'
import { readPreference, writePreference } from '@/preference'
import { assignColors, dimensions, otherColor } from './model'

const props = defineProps<{ report: UsageReport; stack: UsageDimension }>()
const emit = defineEmits<{ 'update:stack': [value: UsageDimension] }>()

// chartModule 是随用量图表一同开始载入的 ECharts 块，载入与求值在之后的任务中完成
const chartModule = import('./chart')
// VChart 是 ECharts 图表组件，块求值后的下一个任务才挂载首张图表
const VChart = defineAsyncComponent(() => chartModule.then(afterTask))

const { t } = useI18n()
const { theme: uiTheme } = useTheme()
const { compact, integer, bucketLabel, dimensionLabel } = useUsageFormat()
const root = useTemplateRef<HTMLElement>('root')
// visible 标记已进入视口的图表绘图区
const visible = ref(false)
let observer: IntersectionObserver

onMounted(() => {
  observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue
      visible.value = true
      observer.unobserve(entry.target)
    }
  })
  root.value!.querySelectorAll('[data-chart]').forEach((figure) => observer.observe(figure))
})
onUnmounted(() => observer.disconnect())
const metric = ref<'requests' | 'tokens'>(
  readPreference('usage.trafficMetric', 'requests', (value) =>
    ['requests', 'tokens'].includes(value as string),
  ),
)
watch(metric, (value) => writePreference('usage.trafficMetric', value))

const hidden = ref<string[]>([])
const traffic = useTemplateRef<Exposed>('traffic')

// Series 是图例需要的序列名与颜色
interface Series {
  name: string
  color: string
}

// withLegend 用页面图例的开关状态生成 ECharts 图例选择，图表内不绘制图例
function withLegend<T extends { series: Series[] }>(option: T) {
  const selected = Object.fromEntries(
    option.series.map((item) => [item.name, !hidden.value.includes(item.name)]),
  )
  return { ...option, legend: { show: false, selected } }
}

// legendItems 返回页面图例的条目
function legendItems(option: { series: Series[] }): Series[] {
  return option.series.map((item) => ({ name: item.name, color: item.color }))
}

// toggle 切换一个序列的显示
function toggle(name: string): void {
  hidden.value = hidden.value.includes(name)
    ? hidden.value.filter((item) => item !== name)
    : [...hidden.value, name]
}

// focus 在悬停图例时突出对应序列，离开时恢复
function focus(name: string | null): void {
  traffic.value?.dispatchAction(
    name === null ? { type: 'downplay' } : { type: 'highlight', seriesName: name },
  )
}

const theme = computed(() =>
  uiTheme.value === 'light'
    ? {
        axis: '#57606a',
        grid: '#eef1f5',
        line: '#d0d7de',
        surface: '#ffffff',
        text: '#1f2328',
      }
    : {
        axis: '#6b7280',
        grid: '#21262d',
        line: '#30363d',
        surface: '#1c2128',
        text: '#c9d1d9',
      },
)

const labels = computed(() => {
  const { buckets, bucket_seconds: seconds, from, to } = props.report
  const multiDay = new Date(to).getTime() - new Date(from).getTime() > 86_400_000
  return buckets.map((bucket) => bucketLabel(bucket.at, seconds, multiDay))
})

// TooltipItem 是 ECharts 坐标轴提示框传入的单个序列值
interface TooltipItem {
  axisValueLabel: string
  seriesName: string
  seriesType: string
  value: number | null
  color: string
}

// barData 把柱状序列的零值写成 null，零高的柱不生成图形元素，提示框仍按 0 显示
const barData = (values: number[]): (number | null)[] => values.map((value) => value || null)

// tooltipRows 生成坐标轴提示框内容，可按数值降序并追加合计
function tooltipRows(
  items: TooltipItem[],
  format: (value: number, item: TooltipItem) => string,
  options: { sort?: boolean; total?: boolean } = {},
): string {
  const rows = items
    .map((item) => (item.seriesType === 'bar' ? { ...item, value: item.value ?? 0 } : item))
    .filter((item) => item.value !== null && item.value !== undefined)
  if (options.sort) rows.sort((a, b) => (b.value ?? 0) - (a.value ?? 0))
  const escape = (value: string): string =>
    value.replace(/[&<>"]/g, (char) => `&#${char.charCodeAt(0)};`)
  const lines = rows.map(
    (item) =>
      `<div style="display:flex;gap:12px;justify-content:space-between"><span><span style="display:inline-block;width:8px;height:8px;border-radius:2px;margin-right:6px;background:${item.color}"></span>${escape(item.seriesName)}</span><span style="font-variant-numeric:tabular-nums">${format(item.value ?? 0, item)}</span></div>`,
  )
  if (options.total && rows[0]) {
    const total = rows.reduce((sum, item) => sum + (item.value ?? 0), 0)
    lines.push(
      `<div style="display:flex;justify-content:space-between;margin-top:4px;padding-top:4px;border-top:1px solid ${theme.value.line}"><span>${escape(t('usage.total'))}</span><span style="font-variant-numeric:tabular-nums">${format(total, rows[0])}</span></div>`,
    )
  }
  return `<div style="min-width:180px"><div style="margin-bottom:4px;color:${theme.value.axis}">${escape(items[0]?.axisValueLabel ?? '')}</div>${lines.join('')}</div>`
}

// base 返回各图共用的坐标轴、网格与提示框配置
function base(format: (value: number) => string) {
  const palette = theme.value
  const axis = (formatter: (value: number) => string, split: boolean) => ({
    type: 'value',
    splitLine: split ? { lineStyle: { color: palette.grid } } : { show: false },
    axisLabel: { color: palette.axis, formatter },
  })
  return {
    animationDuration: 250,
    aria: { enabled: true },
    grid: { left: 8, right: 8, top: 12, bottom: 4, containLabel: true },
    tooltip: {
      trigger: 'axis',
      confine: true,
      backgroundColor: palette.surface,
      borderColor: palette.line,
      textStyle: { color: palette.text, fontSize: 12 },
    },
    xAxis: {
      type: 'category',
      data: labels.value,
      axisLine: { lineStyle: { color: palette.line } },
      axisTick: { show: false },
      axisLabel: { color: palette.axis, hideOverlap: true },
    },
    yAxis: [axis(format, true)],
  }
}

const trafficOption = computed(() => {
  const { series, stack_by: stackBy } = props.report
  const colors = assignColors(series.filter((item) => !item.other).map((item) => item.key))
  return withLegend({
    ...base(compact),
    tooltip: {
      ...base(compact).tooltip,
      axisPointer: { type: 'shadow' },
      formatter: (items: TooltipItem[]) => tooltipRows(items, integer, { sort: true, total: true }),
    },
    series: series.map((item) => ({
      name: item.other ? t('usage.other') : dimensionLabel(stackBy, item.key),
      type: 'bar',
      stack: 'traffic',
      barMaxWidth: 22,
      emphasis: { focus: 'series' },
      color: item.other ? otherColor : colors.get(item.key)!,
      data: barData(metric.value === 'tokens' ? item.tokens : item.requests),
    })),
  })
})
</script>

<template>
  <div ref="root" class="min-w-0">
    <figure class="min-w-0 rounded-lg border border-line bg-panel p-4">
      <div class="mb-2 flex flex-wrap items-center gap-2">
        <figcaption class="mr-auto">
          <span class="text-sm font-semibold text-gray-200">{{ t('usage.chartTraffic') }}</span>
          <span class="ml-2 text-xs text-gray-500">{{ t('usage.chartTrafficHelp') }}</span>
        </figcaption>
        <UiSelect
          :model-value="stack"
          :aria-label="t('usage.stackBy')"
          class="w-32 rounded-md border border-line bg-canvas px-2 py-1 text-xs text-gray-200"
          @update:model-value="emit('update:stack', $event as UsageDimension)"
        >
          <option v-for="item in dimensions" :key="item" :value="item">
            {{ t(`usage.dimension.${item}`) }}
          </option>
        </UiSelect>
        <div
          class="inline-flex rounded-md border border-line bg-canvas p-0.5"
          role="radiogroup"
          :aria-label="t('usage.chartTraffic')"
        >
          <button
            v-for="item in ['requests', 'tokens'] as const"
            :key="item"
            type="button"
            role="radio"
            :aria-checked="metric === item"
            class="rounded px-2.5 py-0.5 text-xs transition-colors"
            :class="metric === item ? 'bg-raised text-white' : 'text-gray-400 hover:text-gray-200'"
            @click="metric = item"
          >
            {{ t(item === 'requests' ? 'usage.metricRequests' : 'usage.metricTokens') }}
          </button>
        </div>
      </div>
      <ChartLegend
        :items="legendItems(trafficOption)"
        :hidden="hidden"
        @toggle="toggle"
        @focus="focus"
      />
      <div data-chart="traffic" class="h-72">
        <VChart v-if="visible" ref="traffic" :option="trafficOption" autoresize />
      </div>
    </figure>
  </div>
</template>
