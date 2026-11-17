<script lang="ts">
// 标题字体在布局块求值时开始载入，不等首次排版用到字体时才发起
void document.fonts.load('1em "Inter Tight Variable"').catch(() => [])
</script>

<script setup lang="ts">
import '@/hljs.css'
import { onActivated, onDeactivated, provide, ref, shallowReactive, watch } from 'vue'
import { RouterView, routeLocationKey, useRoute } from 'vue-router'
import CommandPalette, { type PaletteMode } from '@/playground/shell/CommandPalette.vue'
import AgentModelPanel from '@/playground/AgentModelPanel.vue'
import ModelSelector from '@/playground/ModelSelector.vue'
import { editable } from '@/playground/shortcuts'
import { useTabTitle } from '@/playground/tab-title'

defineOptions({ name: 'StudioLayout' })

// studioRoute 在管理端显示期间保留试用页及其子组件的路由
const route = useRoute()
const studioRoute = shallowReactive({ ...route })
provide(routeLocationKey, studioRoute)
watch(
  () => route.fullPath,
  () => {
    if (route.meta.studio) Object.assign(studioRoute, route)
  },
)
useTabTitle(route)

const paletteOpen = ref(false)
const paletteMode = ref<PaletteMode>('root')

// openPalette 打开命令面板
function openPalette(mode: PaletteMode): void {
  paletteMode.value = mode
  paletteOpen.value = true
}

// onKeydown 处理官网全局快捷键：Ctrl+/ 打开命令面板，Ctrl+Shift+/ 打开快捷键列表；输入框获得焦点时 Ctrl+/ 不打开
function onKeydown(event: KeyboardEvent): void {
  if (event.code !== 'Slash' || !(event.ctrlKey || event.metaKey) || event.altKey) return
  if (!event.shiftKey && editable(event.target)) return
  event.preventDefault()
  openPalette(event.shiftKey ? 'shortcuts' : 'root')
}

onActivated(() => {
  document.documentElement.classList.add('studio')
  window.addEventListener('keydown', onKeydown)
})

onDeactivated(() => {
  document.documentElement.classList.remove('studio')
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div class="makersuite-layout">
    <div class="layout-main">
      <RouterView v-if="route.meta.studio || studioRoute.name === 'prompt'" :route="studioRoute" />
    </div>

    <CommandPalette v-model:open="paletteOpen" v-model:mode="paletteMode" />
    <ModelSelector />
    <AgentModelPanel />
  </div>
</template>

<style scoped>
.makersuite-layout {
  display: flex;
  flex: 1;
  width: 100%;
  height: 100%;
  min-height: 0;
  background: var(--color-v3-surface);
}

.layout-main {
  position: relative;
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  background: var(--color-v3-surface);
}
</style>
