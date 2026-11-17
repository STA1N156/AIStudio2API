<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api'
import { channelLabelKey, useI18n, type TranslationKey } from '@/i18n'
import { usePaged } from '@/paging'
import type { Account, Cooldown, RequestState, RequestSummary } from '@/types'
import UiIcon from './UiIcon.vue'
import UiPager from './UiPager.vue'
import UiSelect, { keepSelected } from './UiSelect.vue'

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
const cancelling = ref('')
const refreshing = ref(false)
const selectedCategory = ref('')
const cooldownModel = ref('')
const requestModel = ref('')
const cooldownModels = computed(() =>
  keepSelected([...new Set(props.cooldowns.map((item) => item.model_id))], cooldownModel.value, ''),
)
const requestModels = computed(() =>
  keepSelected([...new Set(props.requests.map((item) => item.model))], requestModel.value, ''),
)
const modelCooldowns = computed(() =>
  props.cooldowns.filter(
    (item) => cooldownModel.value === '' || item.model_id === cooldownModel.value,
  ),
)
// visibleRequests 是所选模型的请求，最近开始的在前
const visibleRequests = computed(() =>
  props.requests
    .filter((item) => requestModel.value === '' || item.model === requestModel.value)
    .sort((left, right) => Date.parse(right.started_at) - Date.parse(left.started_at)),
)
const expandedModels = ref(new Set<string>())
const tiers = ['Ultra', 'Pro', 'Free'] as const
const tierColors = { Ultra: 'bg-amber-500', Pro: 'bg-blue-500', Free: 'bg-emerald-500' }

const accountTiers = computed(
  () =>
    new Map(
      props.accounts.map((account) => [
        account.id,
        /^ultra/i.test(account.benefit_tier) ? 'Ultra' : account.benefit_tier,
      ]),
    ),
)

function toggleModel(model: string): void {
  if (expandedModels.value.has(model)) expandedModels.value.delete(model)
  else expandedModels.value.add(model)
}

function reasonCategory(reason = ''): string {
  const separator = reason.indexOf(': ')
  return separator > 0 ? reason.slice(0, separator) : ''
}

// categoryLabel 返回冷却类型的当前语言名称，没有类型时为其他
function categoryLabel(category: string): string {
  return category ? logText(category) : t('cooldowns.other')
}

// categories 是全部与各冷却类型的数量，类型按数量从多到少排列
const categories = computed(() => {
  const counts = new Map<string, number>()
  for (const cooldown of modelCooldowns.value) {
    const category = reasonCategory(cooldown.reason)
    counts.set(category, (counts.get(category) ?? 0) + 1)
  }
  return [...counts.entries()].sort((left, right) => right[1] - left[1])
})

// activeCategory 是当前生效的冷却类型，所选类型已无冷却时为全部
const activeCategory = computed(() =>
  categories.value.length > 1 &&
  categories.value.some(([category]) => category === selectedCategory.value)
    ? selectedCategory.value
    : '',
)

// visibleCooldowns 是所选类型的冷却记录。
const visibleCooldowns = computed(() =>
  modelCooldowns.value.filter(
    (cooldown) =>
      activeCategory.value === '' || reasonCategory(cooldown.reason) === activeCategory.value,
  ),
)

// 每个模型按账号去重，分母直接使用后端的启用与模型资格统计。
const cooldownGroups = computed(() => {
  const groups = new Map<
    string,
    {
      model: string
      accounts: Set<string>
      eligible: number
      rows: { tier: (typeof tiers)[number]; accounts: Set<string>; total: number }[]
    }
  >()
  for (const cooldown of visibleCooldowns.value) {
    let group = groups.get(cooldown.model_id)
    if (!group) {
      group = {
        model: cooldown.model_id,
        accounts: new Set(),
        eligible: cooldown.eligible_accounts,
        rows: tiers.map((tier) => ({
          tier,
          accounts: new Set<string>(),
          total: cooldown.eligible_tiers?.[tier] ?? 0,
        })),
      }
      groups.set(group.model, group)
    }
    group.accounts.add(cooldown.account_id)
    group.rows
      .find((row) => row.tier === accountTiers.value.get(cooldown.account_id))
      ?.accounts.add(cooldown.account_id)
  }
  return [...groups.values()].sort((left, right) => left.model.localeCompare(right.model))
})

// 冷却与请求列表每页 50 条，筛选变化时回到第一页
const pageSize = 50
const {
  page: cooldownPage,
  pages: cooldownPages,
  paged: pagedCooldowns,
} = usePaged(cooldownGroups, pageSize)
const {
  page: requestPage,
  pages: requestPages,
  paged: pagedRequests,
} = usePaged(visibleRequests, pageSize)
watch([cooldownModel, activeCategory], () => (cooldownPage.value = 1))
watch(requestModel, () => (requestPage.value = 1))
const cooldownList = ref<HTMLElement>()
const requestList = ref<HTMLElement>()
// 翻页后列表回到顶部
watch(cooldownPage, () => cooldownList.value?.scrollTo({ top: 0 }))
watch(requestPage, () => requestList.value?.scrollTo({ top: 0 }))

const requestStateKeys: Record<RequestState, TranslationKey> = {
  queued: 'state.queued',
  running: 'state.running',
  completed: 'state.completed',
  cancelled: 'state.cancelled',
  failed: 'state.failed',
}

const activeCount = computed(
  () =>
    visibleRequests.value.filter(
      (request) => request.state === 'queued' || request.state === 'running',
    ).length,
)

const accountLabels = computed(
  () => new Map(props.accounts.map((account) => [account.id, account.label])),
)

// accountLabel 将稳定账户 ID 映射为用户显示名称
function accountLabel(id: string, label: string): string {
  return label || accountLabels.value.get(id) || '—'
}

// timeFormat 是当前语言的请求时间格式
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
  <section
    class="flex min-h-0 flex-1 flex-col overflow-auto max-md:[scrollbar-gutter:stable] md:overflow-hidden"
  >
    <header
      class="page-x flex flex-wrap items-center gap-3 border-b border-line py-4 md:overflow-hidden md:[scrollbar-gutter:stable]"
    >
      <div class="min-w-0 flex-1">
        <h2 class="text-xl font-semibold text-white">{{ t('section.requests.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.requests.description') }}</p>
      </div>
      <button
        class="flex items-center gap-2 rounded border border-line bg-raised px-4 py-2 text-sm font-medium text-gray-200 transition hover:bg-line disabled:opacity-50"
        type="button"
        :aria-busy="refreshing"
        @click="refresh"
      >
        <UiIcon name="refresh" :size="15" :class="{ 'animate-spin': refreshing }" />
        {{ t('app.refresh') }}
      </button>
    </header>
    <div
      class="page-x grid min-h-0 flex-1 gap-4 py-4 max-md:shrink-0 max-md:basis-auto md:grid-rows-[minmax(0,3fr)_minmax(0,2fr)] md:overflow-hidden md:py-8 md:[scrollbar-gutter:stable] lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] lg:grid-rows-1"
    >
      <article
        class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-line bg-panel max-md:max-h-[70vh]"
      >
        <div class="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
          <h3 class="text-sm font-semibold text-gray-200">{{ t('cooldowns.title') }}</h3>
          <span class="min-w-[2ch] font-mono text-xs text-gray-500">{{
            cooldownGroups.length
          }}</span>
          <UiSelect
            v-model="cooldownModel"
            :aria-label="t('requests.filterModel')"
            class="min-w-0 max-w-64 rounded-md border border-line bg-canvas px-2 py-1 text-xs text-gray-200"
          >
            <option value="">{{ t('requests.allModels') }}</option>
            <option v-for="model in cooldownModels" :key="model" :value="model">{{ model }}</option>
          </UiSelect>
          <div v-if="categories.length > 1" class="ml-auto flex flex-wrap gap-1">
            <button
              v-for="[category, count] in [['', modelCooldowns.length] as const, ...categories]"
              :key="category || 'all'"
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
              {{ category === '' ? t('cooldowns.all') : categoryLabel(category) }} {{ count }}
            </button>
          </div>
        </div>
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
          v-else-if="visibleCooldowns.length === 0"
          class="py-8 text-center text-xs text-gray-600"
        >
          {{ t('cooldowns.empty') }}
        </div>
        <div
          v-else
          ref="cooldownList"
          class="min-h-0 flex-1 overflow-y-auto [scrollbar-gutter:stable]"
        >
          <div v-for="group in pagedCooldowns" :key="group.model" class="border-b border-line/60">
            <button
              type="button"
              class="flex w-full items-center gap-3 px-4 py-3 text-left transition hover:bg-raised"
              :aria-expanded="expandedModels.has(group.model)"
              :aria-controls="`cooldown-${group.model}`"
              @click="toggleModel(group.model)"
            >
              <span class="min-w-0 flex-1 break-words text-sm text-gray-200">{{
                group.model
              }}</span>
              <span class="shrink-0 font-mono text-xs text-gray-400"
                >{{ group.accounts.size }} / {{ group.eligible }}</span
              >
              <UiIcon
                name="chevronRight"
                :size="14"
                class="shrink-0 text-gray-500 transition-transform"
                :class="{ 'rotate-90': expandedModels.has(group.model) }"
              />
            </button>
            <div class="px-4 pb-3">
              <div
                role="progressbar"
                :aria-label="`${group.model} ${t('cooldowns.title')}`"
                :aria-valuemin="0"
                :aria-valuemax="Math.max(1, group.eligible)"
                :aria-valuenow="group.accounts.size"
                :aria-valuetext="`${group.accounts.size} / ${group.eligible}`"
                class="h-1.5 overflow-hidden rounded-full bg-raised"
              >
                <div
                  class="h-full rounded-full bg-yellow-500 transition-[width]"
                  :style="{
                    width: `${group.eligible ? Math.min(100, (group.accounts.size / group.eligible) * 100) : 0}%`,
                  }"
                ></div>
              </div>
            </div>
            <div
              v-if="expandedModels.has(group.model)"
              :id="`cooldown-${group.model}`"
              class="space-y-3 bg-canvas/40 px-4 pt-1 pb-4"
            >
              <div v-for="row in group.rows" :key="row.tier" class="space-y-1.5">
                <div class="flex items-center justify-between gap-3 text-xs">
                  <span class="font-medium text-gray-300">{{ row.tier }}</span>
                  <span class="font-mono text-gray-400"
                    >{{ row.accounts.size }} / {{ row.total }}</span
                  >
                </div>
                <div
                  role="progressbar"
                  :aria-label="`${group.model} ${row.tier} ${t('state.cooldown')}`"
                  :aria-valuemin="0"
                  :aria-valuemax="Math.max(1, row.total)"
                  :aria-valuenow="row.accounts.size"
                  :aria-valuetext="`${row.accounts.size} / ${row.total}`"
                  class="h-1.5 overflow-hidden rounded-full bg-raised"
                >
                  <div
                    class="h-full rounded-full transition-[width]"
                    :class="tierColors[row.tier]"
                    :style="{
                      width: `${row.total ? Math.min(100, (row.accounts.size / row.total) * 100) : 0}%`,
                    }"
                  ></div>
                </div>
              </div>
            </div>
          </div>
        </div>
        <UiPager v-if="!cooldownError && !loading" v-model="cooldownPage" :pages="cooldownPages" />
      </article>

      <article
        class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-line bg-panel max-md:max-h-[70vh]"
      >
        <div
          class="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-3"
        >
          <h3 class="text-sm font-semibold text-gray-200">{{ t('requests.history') }}</h3>
          <span class="text-xs text-green-400"> {{ t('requests.live') }}: {{ activeCount }} </span>
          <UiSelect
            v-model="requestModel"
            :aria-label="t('requests.filterModel')"
            class="min-w-0 max-w-64 rounded-md border border-line bg-canvas px-2 py-1 text-xs text-gray-200"
          >
            <option value="">{{ t('requests.allModels') }}</option>
            <option v-for="model in requestModels" :key="model" :value="model">{{ model }}</option>
          </UiSelect>
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
        <div
          v-else-if="visibleRequests.length === 0"
          class="py-8 text-center text-xs text-gray-600"
        >
          {{ t('requests.empty') }}
        </div>
        <div
          v-else
          ref="requestList"
          class="min-h-0 flex-1 overflow-y-auto [scrollbar-gutter:stable]"
        >
          <div
            v-for="request in pagedRequests"
            :key="request.id"
            class="flex items-center gap-3 border-b border-line/60 px-4 py-2 transition hover:bg-raised"
          >
            <div class="min-w-0 flex-1">
              <div class="flex min-w-0 items-center gap-2">
                <span v-tooltip.overflow="request.model" class="truncate text-sm text-gray-200">{{
                  request.model
                }}</span>
                <span class="shrink-0 text-xs text-gray-500">{{
                  t(requestStateKeys[request.state])
                }}</span>
              </div>
              <span
                v-tooltip.overflow="
                  request.channel
                    ? `${accountLabel(request.account_id, request.account_label)} · ${t(channelLabelKey(request.channel))}`
                    : accountLabel(request.account_id, request.account_label)
                "
                class="block truncate text-xs text-gray-500"
                >{{ accountLabel(request.account_id, request.account_label)
                }}<template v-if="request.channel">
                  · {{ t(channelLabelKey(request.channel)) }}</template
                ></span
              >
            </div>
            <time class="shrink-0 font-mono text-xs whitespace-nowrap text-gray-500">{{
              formatTime(request.started_at)
            }}</time>
            <button
              v-if="request.state === 'queued' || request.state === 'running'"
              class="shrink-0 rounded border border-red-900/50 bg-red-900/30 px-3 py-1 text-xs text-red-400 transition hover:bg-red-900/50 disabled:opacity-50"
              type="button"
              :disabled="cancelling !== ''"
              :aria-busy="cancelling === request.id"
              @click="cancelRequest(request)"
            >
              {{ t('requests.stop') }}
            </button>
          </div>
        </div>
        <UiPager v-if="!requestError && !loading" v-model="requestPage" :pages="requestPages" />
      </article>
    </div>
  </section>
</template>
