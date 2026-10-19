<script setup lang="ts">
import { computed, reactive, ref, useTemplateRef, watch } from 'vue'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { BarChart, LineChart } from 'echarts/charts'
import { AriaComponent, GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import VChart, { type Exposed } from 'vue-echarts'
import { useI18n } from '@/i18n'
import { useTheme } from '@/theme'
import type { UsageDimension, UsageReport } from '@/types'
import UiSelect from '../UiSelect.vue'
import ChartLegend from './ChartLegend.vue'
import { useUsageFormat } from './format'
import {
  assignColors,
  dimensions,
  otherColor,
  readPreference,
  successRate,
  writePreference,
} from './model'

use([
  CanvasRenderer,
  BarChart,
  LineChart,
  AriaComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
])

const props = defineProps<{ report: UsageReport; stack: UsageDimension }>()
const emit = defineEmits<{ 'update:stack': [value: UsageDimension] }>()

const { t } = useI18n()
const { theme: uiTheme } = useTheme()
const { compact, integer, percent, bucketLabel, dimensionLabel } = useUsageFormat()
const metric = ref<'requests' | 'tokens'>(
  readPreference('usage.trafficMetric', 'requests', (value) =>
    ['requests', 'tokens'].includes(value as string),
  ),
)
watch(metric, (value) => writePreference('usage.trafficMetric', value))

type ChartKey = 'traffic' | 'outcomes'
// hidden 是各图在图例中关闭的序列名
const hidden = reactive<Record<ChartKey, string[]>>({
  traffic: [],
  outcomes: [],
})
const charts: Record<ChartKey, Readonly<{ value: Exposed | null }>> = {
  traffic: useTemplateRef<Exposed>('traffic'),
  outcomes: useTemplateRef<Exposed>('outcomes'),
}

// Series 是图例需要的序列名与颜色
interface Series {
  name: string
  color?: string | undefined
}

// withLegend 用页面图例的开关状态生成 ECharts 图例选择，图表内不绘制图例
function withLegend<T extends { series: Series[] }>(key: ChartKey, option: T) {
  const selected = Object.fromEntries(
    option.series.map((item) => [item.name, !hidden[key].includes(item.name)]),
  )
  return { ...option, legend: { show: false, selected } }
}

// legendItems 返回页面图例的条目
function legendItems(option: { series: Series[] }): { name: string; color: string }[] {
  return option.series.map((item) => ({ name: item.name, color: item.color ?? otherColor }))
}

// toggle 切换一个序列的显示
function toggle(key: ChartKey, name: string): void {
  hidden[key] = hidden[key].includes(name)
    ? hidden[key].filter((item) => item !== name)
    : [...hidden[key], name]
}

// focus 在悬停图例时突出对应序列，离开时恢复
function focus(key: ChartKey, name: string | null): void {
  charts[key].value?.dispatchAction(
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
  value: number | null
  color: string
}

// tooltipRows 生成坐标轴提示框内容，可按数值降序并追加合计
function tooltipRows(
  items: TooltipItem[],
  format: (value: number, item: TooltipItem) => string,
  options: { sort?: boolean; total?: boolean } = {},
): string {
  const rows = items.filter((item) => item.value !== null && item.value !== undefined)
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
function base(format: (value: number) => string, rightAxis?: (value: number) => string) {
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
    yAxis: rightAxis ? [axis(format, true), axis(rightAxis, false)] : [axis(format, true)],
  }
}

const trafficOption = computed(() => {
  const { series, stack_by: stackBy } = props.report
  const colors = assignColors(series.filter((item) => !item.other).map((item) => item.key))
  return withLegend('traffic', {
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
      color: item.other ? otherColor : colors.get(item.key),
      data: metric.value === 'tokens' ? item.tokens : item.requests,
    })),
  })
})

const outcomesOption = computed(() => {
  const buckets = props.report.buckets
  const rateName = t('usage.successRate')
  const rate = (value: number) => percent(value / 100, 0)
  const bar = (name: string, color: string, data: number[]) => ({
    name,
    type: 'bar',
    stack: 'outcomes',
    barMaxWidth: 22,
    color,
    data,
  })
  return withLegend('outcomes', {
    ...base(compact, rate),
    tooltip: {
      ...base(compact).tooltip,
      axisPointer: { type: 'shadow' },
      formatter: (items: TooltipItem[]) =>
        tooltipRows(items, (value, item) =>
          item.seriesName === rateName ? percent(value / 100) : integer(value),
        ),
    },
    yAxis: [
      base(compact).yAxis[0],
      {
        type: 'value',
        min: 0,
        max: 100,
        splitLine: { show: false },
        axisLabel: { color: theme.value.axis, formatter: rate },
      },
    ],
    series: [
      bar(
        t('usage.succeeded'),
        '#22c55e',
        buckets.map((item) => item.succeeded),
      ),
      bar(
        t('usage.rateLimited'),
        '#f59e0b',
        buckets.map((item) => item.rate_limited),
      ),
      bar(
        t('usage.otherErrors'),
        '#ef4444',
        buckets.map((item) => item.failed - item.rate_limited),
      ),
      bar(
        t('usage.canceled'),
        otherColor,
        buckets.map((item) => item.canceled),
      ),
      {
        name: rateName,
        type: 'line',
        yAxisIndex: 1,
        symbol: 'circle',
        symbolSize: 4,
        showSymbol: false,
        connectNulls: false,
        color: '#60a5fa',
        data: buckets.map((item) => {
          const value = successRate(item)
          return value === null ? null : Math.round(value * 1000) / 10
        }),
      },
    ],
  })
})
</script>

<template>
  <div class="grid grid-cols-1 gap-4 xl:grid-cols-2">
    <figure class="min-w-0 rounded-lg border border-line bg-panel p-4">
      <div class="mb-2 flex flex-wrap items-center gap-2">
        <figcaption class="mr-auto">
          <span class="text-sm font-medium text-gray-200">{{ t('usage.chartTraffic') }}</span>
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
        :hidden="hidden.traffic"
        @toggle="toggle('traffic', $event)"
        @focus="focus('traffic', $event)"
      />
      <div class="h-72"><VChart ref="traffic" :option="trafficOption" autoresize /></div>
    </figure>
    <figure class="flex min-w-0 flex-col rounded-lg border border-line bg-panel p-4">
      <figcaption class="mb-2 text-sm font-medium text-gray-200">
        {{ t('usage.chartOutcomes') }}
      </figcaption>
      <ChartLegend
        :items="legendItems(outcomesOption)"
        :hidden="hidden.outcomes"
        @toggle="toggle('outcomes', $event)"
        @focus="focus('outcomes', $event)"
      />
      <div class="min-h-60 flex-1">
        <VChart ref="outcomes" :option="outcomesOption" autoresize />
      </div>
    </figure>
  </div>
</template>
