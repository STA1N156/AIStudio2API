<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useConsole } from '@/console'
import ChatSession from './ChatSession.vue'
import ComparePane from './ComparePane.vue'
import PlaygroundToolbar from './PlaygroundToolbar.vue'
import PromptBox from './PromptBox.vue'
import RunSettings from './RunSettings.vue'
import ZeroState from './ZeroState.vue'
import { defaultImageModel, isChatModel, modelMatches } from './catalog'
import type { ZeroStarter } from './zero-configs'
import { useCompare, usePlayground } from './session'
import { useRunSettingsPanel } from './shortcuts'

defineOptions({ name: 'PlaygroundPage' })

const route = useRoute()
const router = useRouter()
const store = useConsole()
const playground = usePlayground()
const { session } = playground
const { panes, comparing, toggleRaw } = useCompare()

const promptBox = ref<InstanceType<typeof PromptBox>>()
// narrow 与 settingsOpen 是运行设置面板的状态，/ 聚焦输入框，Shift+R 切换原始模式
const { narrow, settingsOpen } = useRunSettingsPanel(
  () => {
    promptBox.value!.focus()
    return true
  },
  (event) => {
    if (event.shiftKey && event.key === 'R') {
      event.preventDefault()
      toggleRaw()
    }
  },
)

// useStarter 用空态示例填写输入框并聚焦
function useStarter(item: ZeroStarter): void {
  promptBox.value!.focus()
  void playground.applyStarter(item)
}
watch(
  () => store.models.value,
  () => playground.ensureModel(),
  { immediate: true },
)
// 地址中的模型为目录别名时与官网一样把地址改写为目录中的模型名
watch(
  () => [route.query.model, store.models.value] as const,
  ([id, models]) => {
    if (typeof id !== 'string' || route.params.id !== undefined || session.turns.length > 0) return
    const model = models.find((model) => modelMatches(model, id) && isChatModel(model))
    if (model === undefined) return
    if (model.id !== id) void router.replace({ query: { ...route.query, model: model.id } })
    if (session.settings.model !== model.id) playground.selectModel(model.id)
  },
  { immediate: true },
)
// 图片页没有指定模型时与官网一样使用目录中的图片模型；从空白图片页回到对话页时恢复对话模型
watch(
  () => [route.name, route.query.model, store.models.value] as const,
  ([name, id, models], previous) => {
    if (id !== undefined || session.turns.length > 0) return
    if (name !== 'image') {
      if (previous?.[0] === 'image') playground.newChat()
      return
    }
    const model = defaultImageModel(models)
    if (model !== undefined && session.settings.model !== model.id) playground.selectModel(model.id)
  },
  { immediate: true },
)
// newChat 与官网一致打开保留当前系统指令的新对话
function newChat(): void {
  playground.newChat(session.settings.systemInstruction)
  void router.push('/prompts/new_chat')
}
</script>

<template>
  <div class="playground">
    <div class="chat-column">
      <PlaygroundToolbar
        :settings-open="settingsOpen"
        :narrow="narrow"
        @new-chat="newChat"
        @toggle-settings="settingsOpen = !settingsOpen"
      />
      <div v-if="comparing" class="chat-container">
        <ComparePane v-for="(pane, index) in panes" :key="index" :pane="pane" :index="index" />
      </div>
      <div v-else class="chat-area">
        <ZeroState v-if="session.turns.length === 0" @starter="useStarter" />
        <ChatSession v-else />
      </div>
      <PromptBox ref="promptBox" />
    </div>
    <template v-if="!comparing">
      <div
        v-if="narrow && settingsOpen"
        class="settings-overlay"
        @click="settingsOpen = false"
      ></div>
      <RunSettings :open="settingsOpen" :narrow="narrow" @close="settingsOpen = false" />
    </template>
  </div>
</template>

<style scoped>
.playground {
  position: relative;
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  background-color: var(--color-v3-surface);
}

.chat-column {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.chat-area {
  position: relative;
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}

.chat-container {
  display: flex;
  flex: 1;
  min-height: 0;
}

.settings-overlay {
  position: fixed;
  z-index: 4;
  inset: 0;
  background-color: var(--color-overlay-background);
}
</style>
