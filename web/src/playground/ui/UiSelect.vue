<script setup lang="ts">
import { keepPrimarySelect, useSelectOptions } from '@/components/UiSelect.vue'
import {
  SelectContent,
  SelectItem,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectViewport,
} from 'reka-ui'
import { ref } from 'vue'
import type { IconName } from './icons'
import UiIcon from './UiIcon.vue'

defineOptions({ inheritAttrs: false })

const model = defineModel<string | number>()
defineProps<{ disabled?: boolean; icon?: IconName }>()
const { options, selectedLabel, selectedKey } = useSelectOptions(model)
// primed 在触发器首次获得焦点或首次打开后为真，此前不挂载选项列表
const primed = ref(false)
</script>

<template>
  <SelectRoot v-model="selectedKey" :disabled="disabled" @update:open="primed = true">
    <SelectTrigger v-bind="$attrs" class="ms-select-trigger" @focus="primed = true">
      <UiIcon v-if="icon" :name="icon" :size="18" />
      <span class="ms-select-value">{{ selectedLabel }}</span>
      <UiIcon name="chevronDown" :size="20" class="ms-select-arrow" />
    </SelectTrigger>
    <SelectPortal v-if="primed">
      <SelectContent position="popper" :side-offset="0" class="ms-select-panel">
        <SelectViewport @pointerup.capture="keepPrimarySelect">
          <SelectItem
            v-for="(option, index) in options"
            :key="`${index}:${option.value}`"
            v-tooltip.left="option.title"
            :value="String(index)"
            :disabled="option.disabled"
            class="ms-select-option"
          >
            <SelectItemText v-tooltip.overflow="option.label" class="ms-select-option-text">{{
              option.label
            }}</SelectItemText>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
