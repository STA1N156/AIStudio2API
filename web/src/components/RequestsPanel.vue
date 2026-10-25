<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import { usePaged } from '@/paging'
import type { Account, Cooldown, RequestState, RequestSummary } from '@/types'
import DetailDialog from './DetailDialog.vue'
import UiIcon from './UiIcon.vue'
import PageHeader from './PageHeader.vue'
import RelativeTime from './RelativeTime.vue'
import UiPager from './UiPager.vue'

const props = defineProps<{
  accounts: Account[]
  cooldowns: Cooldown[]
  requests: RequestSummary[]
  loading: boolean
  cooldownError: string
  requestError: string
}>()

const emit = defineEmits<{
  refresh: []
  notice: [message: string, tone: 'success' | 'error']
}>()

const { locale, t, errorText, logText } = useI18n()
const coolingAccounts = computed(() => new Set(props.cooldowns.map((item) => item.account_id)).size)
const cancelling = ref('')
const refreshing = ref(false)
// selectedCategory 为 null 表示全部；空字符串是没有类型的冷却
const selectedCategory = ref<string | null>(null)
const detail = ref<Cooldown | null>(null)

// splitReason 把冷却原因拆成冷却类型与上游错误原文
function splitReason(reason: string | undefined): { category: string; text: string } {
  const value = reason ?? ''
  const separator = value.indexOf(': ')
  if (separator <= 0) return { category: '', text: value }
  return { category: value.slice(0, separator), text: value.slice(separator + 2) }
}

// categoryLabel 返回冷却类型的当前语言名称，没有类型时为其他
function categoryLabel(category: string): string {
  return category ? logText(category) : t('cooldowns.other')
}

// categories 是全部与各冷却类型的数量，类型按数量从多到少排列
const categories = computed(() => {
  const counts = new Map<string, number>()
  for (const cooldown of props.cooldowns) {
    const category = splitReason(cooldown.reason).category
    counts.set(category, (counts.get(category) ?? 0) + 1)
  }
  return [...counts.entries()].sort((left, right) => right[1] - left[1])
})

// activeCategory 是当前生效的冷却类型，所选类型已无冷却时为全部
const activeCategory = computed(() =>
  categories.value.length > 1 &&
  categories.value.some(([category]) => category === selectedCategory.value)
    ? selectedCategory.value
    : null,
)

// visibleCooldowns 是所选类型的冷却，按恢复时间先后排列
const visibleCooldowns = computed(() =>
  props.cooldowns
    .filter(
      (cooldown) =>
        activeCategory.value === null ||
        splitReason(cooldown.reason).category === activeCategory.value,
    )
    .sort((left, right) => Date.parse(left.until) - Date.parse(right.until)),
)

// modelGroups 按模型汇总所选类型的冷却，冷却账号多的模型在前；earliest 是最早恢复时间
const modelGroups = computed(() => {
  const groups = new Map<
    string,
    { model: string; items: Cooldown[]; accounts: Set<string>; earliest: string }
  >()
  for (const cooldown of visibleCooldowns.value) {
    const group = groups.get(cooldown.model_id) ?? {
      model: cooldown.model_id,
      items: [],
      accounts: new Set<string>(),
      earliest: cooldown.until,
    }
    group.items.push(cooldown)
    group.accounts.add(cooldown.account_id)
    groups.set(cooldown.model_id, group)
  }
  return [...groups.values()].sort(
    (left, right) =>
      right.accounts.size - left.accounts.size || left.model.localeCompare(right.model),
  )
})
const expandedModel = ref('')
const expandedCooldowns = computed(
  () => modelGroups.value.find((group) => group.model === expandedModel.value)?.items ?? [],
)
const { page, pages, paged: pagedCooldowns } = usePaged(expandedCooldowns, 50)
watch([expandedModel, activeCategory], () => (page.value = 1))
// accountTotal 是模型冷却占比条的分母
const accountTotal = computed(() =>
  Math.max(props.accounts.length, ...modelGroups.value.map((group) => group.accounts.size), 1),
)

const requestStateKeys: Record<RequestState, TranslationKey> = {
  queued: 'state.queued',
  running: 'state.running',
  completed: 'state.completed',
  cancelled: 'state.cancelled',
  failed: 'state.failed',
}

const requestStateTones: Record<RequestState, string> = {
  queued: 'text-gray-400',
  running: 'text-blue-400',
  completed: 'text-green-400',
  cancelled: 'text-gray-500',
  failed: 'text-red-400',
}

// isActive 判断请求是否仍在排队或运行
function isActive(request: RequestSummary): boolean {
  return request.state === 'queued' || request.state === 'running'
}

// activeRequests 是进行中的请求，最早开始的在前，便于发现卡住的请求
const activeRequests = computed(() =>
  props.requests
    .filter(isActive)
    .sort((left, right) => Date.parse(left.started_at) - Date.parse(right.started_at)),
)
// finishedRequests 是已结束的请求，最近开始的在前
const finishedRequests = computed(() =>
  props.requests
    .filter((request) => !isActive(request))
    .sort((left, right) => Date.parse(right.started_at) - Date.parse(left.started_at)),
)
const activeCount = computed(() => activeRequests.value.length)
const queuedCount = computed(
  () => activeRequests.value.filter((request) => request.state === 'queued').length,
)
const failedCount = computed(
  () => finishedRequests.value.filter((request) => request.state === 'failed').length,
)
const { page: activePage, pages: activePages, paged: pagedActive } = usePaged(activeRequests, 50)
const {
  page: finishedPage,
  pages: finishedPages,
  paged: pagedFinished,
} = usePaged(finishedRequests, 30)
// requestSections 是请求摘要的两段列表：进行中与最近结束
const requestSections = computed(() => [
  {
    id: 'active',
    title: t('requests.live'),
    total: activeRequests.value.length,
    items: pagedActive.value,
    page: activePage,
    pages: activePages.value,
  },
  {
    id: 'finished',
    title: t('requests.finished'),
    total: finishedRequests.value.length,
    items: pagedFinished.value,
    page: finishedPage,
    pages: finishedPages.value,
  },
])

const accountLabels = computed(
  () => new Map(props.accounts.map((account) => [account.id, account.label])),
)

// accountLabel 将稳定账户 ID 映射为用户显示名称
function accountLabel(id: string, label: string): string {
  return label || accountLabels.value.get(id) || '—'
}

const timeFormat = computed(
  () =>
    new Intl.DateTimeFormat(locale.value, {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }),
)

// formatTime 根据当前语言显示请求时间
function formatTime(value: string): string {
  return timeFormat.value.format(new Date(value))
}

// refresh 请求刷新数据，图标短暂旋转确认点击
function refresh(): void {
  refreshing.value = true
  emit('refresh')
  window.setTimeout(() => (refreshing.value = false), 600)
}

// cancelRequest 停止活动请求并刷新摘要
async function cancelRequest(request: RequestSummary): Promise<void> {
  cancelling.value = request.id
  try {
    await api.cancelRequest(request.id)
    emit('refresh')
  } catch (error) {
    emit('notice', errorText(error), 'error')
  } finally {
    cancelling.value = ''
  }
}
</script>

<template>
  <section class="page-panel flex min-h-0 flex-1 flex-col overflow-auto">
    <PageHeader
      :title="t('section.requests.title')"
      :description="t('section.requests.description')"
    >
      <button class="ui-button" type="button" :aria-busy="refreshing" @click="refresh">
        <UiIcon name="refresh" :size="15" :class="{ 'animate-spin': refreshing }" />
        {{ t('app.refresh') }}
      </button>
    </PageHeader>
    <div class="mx-auto w-full max-w-6xl p-4 md:p-8">
      <div class="summary-grid">
        <div>
          <span>{{ t('cooldowns.title') }}</span
          ><strong :class="{ '!text-yellow-400': cooldowns.length }">{{ cooldowns.length }}</strong>
        </div>
        <div>
          <span>{{ t('requests.account') }}</span
          ><strong>{{ coolingAccounts }}</strong>
        </div>
        <div>
          <span>{{ t('requests.live') }}</span
          ><strong class="!text-blue-400">{{ activeCount }}</strong>
        </div>
      </div>
      <div class="grid gap-5 lg:grid-cols-2">
        <article
          class="flex min-h-0 flex-col overflow-hidden rounded-xl border border-line bg-panel"
        >
          <div class="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
            <h3 class="font-bold text-gray-300">{{ t('cooldowns.title') }}</h3>
            <span class="font-mono text-xs text-gray-500">{{ cooldowns.length }}</span>
            <div v-if="categories.length > 1" class="ml-auto flex flex-wrap gap-1">
              <button
                v-for="[category, count] in [[null, cooldowns.length] as const, ...categories]"
                :key="category === null ? 'all' : `c:${category}`"
                type="button"
                class="rounded px-2 py-0.5 text-xs transition"
                :class="
                  activeCategory === category
                    ? 'bg-blue-600 text-white'
                    : 'bg-raised text-gray-400 hover:text-gray-200'
                "
                :aria-pressed="activeCategory === category"
                @click="selectedCategory = category"
              >
                {{ category === null ? t('cooldowns.all') : categoryLabel(category) }} {{ count }}
              </button>
            </div>
          </div>
          <p class="border-b border-line px-4 py-3 text-xs leading-5 text-gray-400">
            {{ t('ui.cooldownScope') }}
          </p>
          <div
            v-if="cooldownError"
            class="m-4 rounded border border-red-500/40 bg-red-500/10 p-3 text-red-300"
          >
            {{ cooldownError }}
          </div>
          <div v-else-if="loading" class="py-8 text-center text-gray-500">
            {{ t('common.loading') }}
          </div>
          <div
            v-else-if="cooldowns.length === 0"
            class="flex flex-col items-center gap-3 px-4 py-12 text-center text-sm text-gray-400"
          >
            <UiIcon name="success" :size="28" class="text-green-400" />
            {{ t('cooldowns.empty') }}
          </div>
          <div v-else class="max-h-[65vh] min-h-0 flex-1 overflow-y-auto">
            <template v-for="group in modelGroups" :key="group.model">
              <button
                type="button"
                class="flex w-full items-center gap-3 border-b border-line/60 px-4 py-3 text-left transition hover:bg-raised"
                :aria-expanded="expandedModel === group.model"
                @click="expandedModel = expandedModel === group.model ? '' : group.model"
              >
                <UiIcon
                  :name="expandedModel === group.model ? 'chevronDown' : 'chevronRight'"
                  :size="14"
                  class="shrink-0 text-gray-500"
                />
                <div class="min-w-0 flex-1">
                  <span
                    v-tooltip.overflow="group.model"
                    class="block truncate text-sm font-medium text-gray-200"
                    >{{ group.model || t('ui.all') }}</span
                  >
                  <span class="mt-2 block h-1 overflow-hidden rounded-full bg-raised">
                    <span
                      class="block h-full rounded-full bg-yellow-400/70"
                      :style="{ width: `${(group.accounts.size / accountTotal) * 100}%` }"
                    />
                  </span>
                </div>
                <div class="shrink-0 text-right">
                  <span class="block text-sm font-semibold text-yellow-400 tabular-nums">{{
                    t('cooldowns.accountCount').replace('{count}', String(group.accounts.size))
                  }}</span>
                  <span class="block text-xs text-gray-500"
                    >{{ t('cooldowns.earliest') }} <RelativeTime :until="group.earliest"
                  /></span>
                </div>
              </button>
              <div
                v-if="expandedModel === group.model"
                class="border-b border-line/60 bg-canvas/40"
              >
                <button
                  v-for="cooldown in pagedCooldowns"
                  :key="`${cooldown.account_id}:${cooldown.channel}`"
                  type="button"
                  class="flex w-full items-center gap-3 py-2.5 pr-4 pl-11 text-left text-xs transition hover:bg-raised"
                  @click="detail = cooldown"
                >
                  <span
                    v-tooltip.overflow="
                      `${accountLabel(cooldown.account_id, cooldown.account_label)} · ${t(channelLabelKey(cooldown.channel))}`
                    "
                    class="min-w-0 flex-1 truncate text-gray-300"
                    >{{ accountLabel(cooldown.account_id, cooldown.account_label) }} ·
                    {{ t(channelLabelKey(cooldown.channel)) }}</span
                  >
                  <span class="shrink-0 text-gray-500">{{
                    categoryLabel(splitReason(cooldown.reason).category)
                  }}</span>
                  <time
                    :title="formatTime(cooldown.until)"
                    class="w-20 shrink-0 text-right text-yellow-400"
                    ><RelativeTime :until="cooldown.until"
                  /></time>
                </button>
                <div class="px-4 pb-3">
                  <UiPager v-model="page" :pages="pages" :total="group.items.length" />
                </div>
              </div>
            </template>
          </div>
        </article>

        <article
          class="flex min-h-0 flex-col overflow-hidden rounded-xl border border-line bg-panel"
        >
          <div class="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
            <h3 class="mr-auto font-bold text-gray-300">{{ t('requests.history') }}</h3>
            <span class="status-pill text-blue-400 tabular-nums">
              <span
                class="size-1.5 rounded-full bg-current"
                :class="{ 'animate-pulse': activeCount > queuedCount }"
              />{{ t('state.running') }} {{ activeCount - queuedCount }}
            </span>
            <span v-if="queuedCount" class="status-pill text-gray-400 tabular-nums"
              >{{ t('state.queued') }} {{ queuedCount }}</span
            >
            <span v-if="failedCount" class="status-pill text-red-400 tabular-nums"
              >{{ t('state.failed') }} {{ failedCount }}</span
            >
          </div>
          <div
            v-if="requestError"
            class="m-4 rounded border border-red-500/40 bg-red-500/10 p-3 text-red-300"
          >
            {{ requestError }}
          </div>
          <div v-else-if="loading" class="py-8 text-center text-gray-500">
            {{ t('common.loading') }}
          </div>
          <div v-else-if="requests.length === 0" class="py-8 text-center text-xs text-gray-600">
            {{ t('requests.empty') }}
          </div>
          <div v-else class="max-h-[65vh] min-h-0 flex-1 overflow-y-auto">
            <template v-for="section in requestSections" :key="section.id">
              <template v-if="section.total">
                <h4
                  class="sticky top-0 z-[1] border-b border-line/60 bg-panel px-4 py-1.5 text-[11px] font-medium text-gray-500"
                >
                  {{ section.title }} · {{ section.total }}
                </h4>
                <div
                  v-for="request in section.items"
                  :key="request.id"
                  class="flex items-center gap-3 border-b border-line/60 px-4 py-2.5 transition hover:bg-raised"
                >
                  <div class="min-w-0 flex-1">
                    <div class="flex min-w-0 items-center gap-2">
                      <span
                        v-tooltip.overflow="request.model"
                        class="min-w-0 truncate text-sm text-gray-200"
                        >{{ request.model }}</span
                      >
                      <span
                        class="status-pill ml-auto shrink-0 !py-0"
                        :class="requestStateTones[request.state]"
                        >{{ t(requestStateKeys[request.state]) }}</span
                      >
                    </div>
                    <div class="mt-0.5 flex min-w-0 items-center gap-2 text-xs text-gray-500">
                      <span
                        v-tooltip.overflow="
                          request.channel
                            ? `${accountLabel(request.account_id, request.account_label)} · ${t(channelLabelKey(request.channel))}`
                            : accountLabel(request.account_id, request.account_label)
                        "
                        class="min-w-0 truncate"
                        >{{ accountLabel(request.account_id, request.account_label)
                        }}<template v-if="request.channel">
                          · {{ t(channelLabelKey(request.channel)) }}</template
                        ></span
                      >
                      <time
                        :title="formatTime(request.started_at)"
                        class="ml-auto shrink-0 tabular-nums"
                        ><RelativeTime :since="request.started_at"
                      /></time>
                    </div>
                  </div>
                  <button
                    v-if="isActive(request)"
                    class="ui-button ui-button-danger !min-h-7 shrink-0 !px-2.5 !py-1 !text-xs"
                    type="button"
                    :disabled="cancelling !== ''"
                    :aria-busy="cancelling === request.id"
                    @click="cancelRequest(request)"
                  >
                    {{ t('requests.stop') }}
                  </button>
                </div>
                <div v-if="section.pages > 1" class="px-4 pb-3">
                  <UiPager
                    v-model="section.page.value"
                    :pages="section.pages"
                    :total="section.total"
                  />
                </div>
              </template>
            </template>
          </div>
        </article>
      </div>
    </div>

    <DetailDialog
      :open="detail !== null"
      :title="detail?.model_id ?? ''"
      :description="
        detail
          ? `${accountLabel(detail.account_id, detail.account_label)} · ${t(channelLabelKey(detail.channel))}`
          : ''
      "
      :copy-text="detail?.reason"
      @close="detail = null"
    >
      <template v-if="detail">
        <dl class="mb-4 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
          <dt class="text-gray-500">{{ t('cooldowns.category') }}</dt>
          <dd class="text-yellow-300">{{ categoryLabel(splitReason(detail.reason).category) }}</dd>
          <dt class="text-gray-500">{{ t('cooldowns.until') }}</dt>
          <dd class="text-gray-200">
            <RelativeTime :until="detail.until" /> · {{ formatTime(detail.until) }}
          </dd>
        </dl>
        <pre
          class="rounded-md border border-line bg-canvas p-3 font-mono text-xs leading-5 whitespace-pre-wrap [overflow-wrap:anywhere] text-gray-300"
          >{{ splitReason(detail.reason).text || '—' }}</pre>
      </template>
    </DetailDialog>
  </section>
</template>
