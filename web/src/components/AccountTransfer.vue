<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { Account, AccountImportResult } from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ accounts: Account[]; selected: string[] }>()
const emit = defineEmits<{
  'update:selected': [ids: string[]]
  delete: []
  refresh: []
  notice: [message: string, tone: 'success' | 'error']
}>()
const { t, errorText } = useI18n()
const picker = ref<HTMLInputElement | null>(null)
const busy = ref(false)
const dragging = ref(0)
const results = ref<AccountImportResult[]>([])
const allSelected = computed(
  () => props.accounts.length > 0 && props.selected.length === props.accounts.length,
)
const counts = computed(() => ({
  imported: results.value.filter((result) => result.status === 'imported').length,
  skipped: results.value.filter((result) => result.status === 'skipped').length,
  failed: results.value.filter((result) => result.status === 'failed').length,
}))

async function importFiles(files: File[]): Promise<void> {
  dragging.value = 0
  if (busy.value || files.length === 0) return
  if (
    files.some((file) => !/\.(json|zip)$/i.test(file.name)) ||
    files.reduce((sum, file) => sum + file.size, 0) > 32 * 1024 * 1024
  ) {
    emit('notice', t('accounts.importLimit'), 'error')
    return
  }
  busy.value = true
  results.value = []
  try {
    results.value = await api.importAccounts(files)
    emit('refresh')
  } catch (error) {
    emit('notice', errorText(error), 'error')
  } finally {
    busy.value = false
    if (picker.value) picker.value.value = ''
  }
}

async function exportAccounts(): Promise<void> {
  busy.value = true
  try {
    await api.exportAccounts(
      props.selected.length ? props.selected : props.accounts.map((account) => account.id),
    )
  } catch (error) {
    emit('notice', errorText(error), 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div
    class="mb-4 rounded-xl border border-dashed p-3 transition sm:p-4"
    :class="dragging ? 'border-blue-400 bg-blue-500/10' : 'border-line bg-panel'"
    @dragenter.prevent="dragging++"
    @dragover.prevent
    @dragleave.prevent="dragging = Math.max(0, dragging - 1)"
    @drop.prevent="importFiles(Array.from($event.dataTransfer?.files ?? []))"
  >
    <div class="flex flex-wrap items-center gap-3">
      <label class="flex items-center gap-2 text-sm text-gray-300">
        <input
          type="checkbox"
          class="h-4 w-4 accent-blue-500"
          :checked="allSelected"
          :indeterminate="selected.length > 0 && !allSelected"
          :disabled="busy || accounts.length === 0"
          @change="
            emit('update:selected', allSelected ? [] : accounts.map((account) => account.id))
          "
        />
        {{ t('accounts.selectAll') }}
        <span class="text-xs text-gray-500">{{ selected.length }} / {{ accounts.length }}</span>
      </label>
      <div class="flex w-full flex-wrap gap-2 sm:ml-auto sm:w-auto">
        <input
          ref="picker"
          class="hidden"
          type="file"
          accept=".json,.zip"
          multiple
          @change="importFiles(Array.from(picker?.files ?? []))"
        />
        <button
          type="button"
          class="ui-button flex-1 sm:flex-none"
          :disabled="busy"
          @click="picker?.click()"
        >
          <UiIcon
            :name="busy ? 'spinner' : 'import'"
            :size="15"
            :class="{ 'animate-spin': busy }"
          />
          {{ t(busy ? 'common.loading' : 'accounts.importFiles') }}
        </button>
        <button
          type="button"
          class="ui-button flex-1 sm:flex-none"
          :disabled="busy || accounts.length === 0"
          @click="exportAccounts"
        >
          <UiIcon name="download" :size="15" />
          {{ t(selected.length ? 'accounts.exportSelected' : 'accounts.exportAll') }}
        </button>
        <button
          v-if="selected.length"
          type="button"
          class="ui-button ui-button-danger w-full sm:w-auto"
          :disabled="busy"
          @click="emit('delete')"
        >
          <UiIcon name="trash" :size="15" />
          {{ t('accounts.deleteSelected').replace('{count}', String(selected.length)) }}
        </button>
      </div>
    </div>
    <p class="mt-3 text-xs text-gray-400">{{ t('accounts.dropHint') }}</p>
    <p class="mt-2 flex items-start gap-1.5 text-xs leading-5 text-gray-400">
      <UiIcon name="security" :size="13" class="mt-0.5 shrink-0" />{{ t('accounts.backupWarning') }}
    </p>
    <details v-if="results.length" class="mt-3 text-xs">
      <summary class="cursor-pointer text-gray-300">
        {{ t('accounts.imported') }} {{ counts.imported }} · {{ t('accounts.skipped') }}
        {{ counts.skipped }} · {{ t('accounts.failed') }} {{ counts.failed }}
      </summary>
      <ul class="mt-2 max-h-48 space-y-1 overflow-auto">
        <li
          v-for="(result, index) in results"
          :key="index"
          :class="result.status === 'failed' ? 'text-red-400' : 'text-gray-400'"
        >
          {{ result.account || result.file }} — {{ t(`accounts.${result.status}`)
          }}<span v-if="result.error">：{{ result.error }}</span>
        </li>
      </ul>
    </details>
  </div>
</template>
