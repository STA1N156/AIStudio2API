import {
  computed,
  effectScope,
  hasInjectionContext,
  inject,
  markRaw,
  provide,
  reactive,
  ref,
  shallowReactive,
  toRaw,
  watch,
  type InjectionKey,
} from 'vue'
import { api } from '@/api'
import { useConsole } from '@/console'
import { keepPage } from '@/desktop'
import { useI18n } from '@/i18n'
import type { Model, SystemInstructionPreset } from '@/types'
import { attachmentBlob, copyAttachmentBlob } from './attachment'
import {
  agentTools,
  Capability,
  defaultChatModel,
  defaultOn,
  hasCode,
  isAgent,
  toolCodes,
  toolCompatible,
  type Tool,
} from './catalog'
import { countVideoFrames, driveFileExists, uploadDriveFile } from './drive'
import { readImage } from './media/api'
import { generate, protocolsFor } from './protocols'
import {
  agentFields,
  apiKey,
  apiPart,
  appendParts,
  buildRequest,
  continuation,
  countTokens,
  countTokensDetailed,
  failureText,
  type TokenCountResult,
} from './request'
import type { PaneData, Part, RunSettings, SafetyCategory, SafetyThreshold, Turn } from './types'
import type { ZeroStarter } from './zero-configs'

// safetyCategories 是官网安全设置对话框的类别顺序
export const safetyCategories: SafetyCategory[] = [
  'HARM_CATEGORY_HARASSMENT',
  'HARM_CATEGORY_HATE_SPEECH',
  'HARM_CATEGORY_SEXUALLY_EXPLICIT',
  'HARM_CATEGORY_DANGEROUS_CONTENT',
]

// safetyThresholds 是安全设置滑块从关闭到最严格的五档
export const safetyThresholds: SafetyThreshold[] = [
  'OFF',
  'BLOCK_NONE',
  'BLOCK_ONLY_HIGH',
  'BLOCK_MEDIUM_AND_ABOVE',
  'BLOCK_LOW_AND_ABOVE',
]

// outputLimit 返回模型输出长度上限，自动续写模型为输入上限
export function outputLimit(model: Model | undefined): number | undefined {
  return model?.capabilities?.output_continuation
    ? model.input_token_limit
    : model?.output_token_limit
}

// researchDefaults 是 Deep Research 的思考摘要、可视化与协作规划默认值，与官网一样取模型目录 field 82 默认开启的能力码
function researchDefaults(model: Model | undefined) {
  return {
    thinkingSummaries: defaultOn(model, Capability.ThinkingSummaries),
    visualization: defaultOn(model, Capability.Visualization),
    collaborativePlanning: defaultOn(model, Capability.CollaborativePlanning),
  }
}

// modelDefaults 返回随模型变化的运行设置默认值，官网面板隐藏的采样参数默认不发送，agent 打开官网默认的工具
function modelDefaults(model: Model | undefined): Partial<RunSettings> {
  return {
    ...(isAgent(model) && { tools: agentTools(model!) }),
    model: model?.id ?? '',
    temperature: hasCode(model, Capability.Temperature) ? (model?.temperature ?? null) : null,
    topP: hasCode(model, Capability.TopP) ? (model?.top_p ?? null) : null,
    topK: hasCode(model, Capability.TopK) ? (model?.top_k ?? null) : null,
    maxOutputTokens: model?.output_token_limit ?? null,
    thinkingLevel: model?.default_thinking_level ?? null,
    thinkingBudget: -1,
    aspectRatio: '',
    imageSize: defaultImageSize(model),
    videoAspectRatio: '',
    videoDuration: null,
    videoResolution: model?.capability_options?.video_output_resolutions?.[0] ?? '',
    videoTask: '',
    ...researchDefaults(model),
  }
}

// defaultImageSize 与官网一致：模型可选分辨率中有 1K 时默认 1K，否则为第一项
function defaultImageSize(model: Model | undefined): string {
  const sizes = model?.capability_options?.image_output_resolutions ?? []
  return sizes.includes('1K') ? '1K' : (sizes[0] ?? '')
}

// defaultSettings 返回新对话的运行设置：模型默认参数、安全设置全部关闭，agent 之外的模型工具全部关闭
export function defaultSettings(model: Model | undefined): RunSettings {
  return {
    model: '',
    protocol: 'gemini',
    stream: true,
    systemInstruction: '',
    temperature: null,
    topP: null,
    topK: null,
    maxOutputTokens: null,
    stopSequences: [],
    thinkingLevel: null,
    thinkingBudget: -1,
    mediaResolution: '',
    safety: Object.fromEntries(
      safetyCategories.map((category) => [category, 'OFF']),
    ) as RunSettings['safety'],
    tools: [],
    responseSchema: '',
    functionDeclarations: '',
    imageOutput: 'IMAGE_TEXT',
    aspectRatio: '',
    imageSize: '',
    videoAspectRatio: '',
    videoDuration: null,
    videoResolution: '',
    videoTask: '',
    imageCount: 1,
    openAISize: 'auto',
    imageQuality: 'auto',
    agentModel: '',
    maxTotalTokens: null,
    environment: { type: 'new', id: '', sources: [], rules: [] },
    ...researchDefaults(undefined),
    ...modelDefaults(model),
  }
}

// newTurnID 生成 32 位十六进制的轮次与对话 ID
export function newTurnID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, '0'),
  )
    .join('')
    .toUpperCase()
}

// uploadedVideo 表示片段是附件加入时已上传 Drive 的视频，与官网一样以抽帧统计 token 数
function uploadedVideo(part: Part): boolean {
  return part.fileData !== undefined && part.inlineData?.mimeType.startsWith('video/') === true
}

// renewUploads 在发送前把 Files API 上传文件已到期或已不存在的视频以对话中保留的原视频重新上传，片段改为引用新文件；signal 中止时检查与上传随之中止
async function renewUploads(
  turns: readonly Turn[],
  signal: AbortSignal | null = null,
): Promise<void> {
  const videos = turns.flatMap((turn) => turn.parts).filter(uploadedVideo)
  for (const part of videos) {
    if (Date.now() < part.fileExpires! && (await driveFileExists(part.fileData!.fileUri, signal)))
      continue
    const uploaded = await uploadDriveFile(attachmentBlob(part), part.fileName!, signal)
    part.fileData = { mimeType: uploaded.mimeType, fileUri: uploaded.uri }
    part.fileExpires = Date.parse(uploaded.expirationTime)
  }
}

// framedVideo 表示片段是已按抽帧统计过 token 数的视频
function framedVideo(part: Part): boolean {
  return uploadedVideo(part) && part.tokenCount !== undefined
}

// attachmentCounts 记录每个附件最近一次 token 计数请求，只接收最近一次请求的结果
const attachmentCounts = new WeakMap<Part, object>()

// cloneData 复制对话数据为普通对象，内联附件复用原 Base64 字符串与缓存的 Blob
function cloneData<T>(value: T): T {
  const raw = toRaw(value)
  if (raw === null || typeof raw !== 'object') return raw
  if (Array.isArray(raw)) return raw.map((item) => cloneData(item)) as T
  const copy: Record<string, unknown> = {}
  for (const [key, item] of Object.entries(raw as Record<string, unknown>)) {
    if (key === 'inlineData' && item !== null && typeof item === 'object') {
      const source = toRaw(item) as NonNullable<Part['inlineData']>
      const target = markRaw({ ...source })
      copyAttachmentBlob(source, target)
      copy[key] = target
    } else {
      copy[key] = cloneData(item)
    }
  }
  return copy as T
}

// models 返回实时模型目录
function models(): Model[] {
  return useConsole().models.value
}

// SessionLinks 让对比窗格共用主输入框。
interface SessionLinks {
  draft?: () => { text: string; attachments: Part[] }
}

// draftCountDelay 与官网一致：输入框停止输入 500 ms 后才统计输入框文本的 token
const draftCountDelay = 500

// createSession 创建仅保留在当前页面中的对话。
function createSession(links: SessionLinks = {}, initial?: PaneData) {
  const session = reactive({
    title: '',
    settings: initial ? cloneData(initial.settings) : defaultSettings(undefined),
    turns: initial ? cloneData(initial.turns) : ([] as Turn[]),
    draft: '',
    attachments: [] as Part[],
    running: false,
    started: 0,
    rawMode: false,
    promptTokens: null as number | null,
    draftTokens: 0,
    systemTokens: null as number | null,
  })

  let controller: AbortController | undefined
  let tokenController: AbortController | undefined
  let draftController: AbortController | undefined
  let draftTimer = 0
  let tokenTimer = 0
  // lastRun 是对话末尾模型轮次那次运行的输入与输出 token 数，取自生成用量或整段统计的逐轮明细
  const lastRun = ref<{ id: string; input: number; output: number } | null>(null)
  const scope = effectScope(true)

  // currentModel 返回当前所选模型
  const currentModel = scope.run(() =>
    computed(() => models().find((model) => model.id === session.settings.model)),
  )!

  // draftSource 返回本窗格使用的输入框文本与附件，对比模式的右侧窗格共用主输入框
  const draftSource = () =>
    links.draft ? links.draft() : { text: session.draft, attachments: session.attachments }

  // usage 是标题栏显示的 token 用量：总数为对话、输入框文本与输入框附件之和；输入框为空且末尾是模型轮次时，输入与输出取那次运行，否则全部记为下一次运行的输入
  const usage = scope.run(() =>
    computed(() => {
      const pending =
        session.draftTokens +
        draftSource().attachments.reduce((sum, part) => sum + (part.tokenCount ?? 0), 0)
      const total = (session.promptTokens ?? 0) + pending
      const last = session.turns.at(-1)
      const run =
        pending === 0 && last !== undefined && lastRun.value?.id === last.id ? lastRun.value : null
      return {
        total,
        input: run ? run.input : total,
        output: run ? run.output : 0,
        cached: run ? (last?.cachedTokens ?? 0) : 0,
      }
    }),
  )!

  // newChat 结束当前生成，开始一个空白对话。
  function newChat(systemInstruction = ''): void {
    discard()
    Object.assign(session, {
      title: '',
      settings: { ...defaultSettings(defaultChatModel(models())), systemInstruction },
      turns: [],
      draft: '',
      attachments: [],
      running: false,
      promptTokens: null,
      draftTokens: 0,
      systemTokens: null,
      rawMode: false,
    })
    lastRun.value = null
    if (systemInstruction.trim() !== '') scheduleTokens()
  }

  // ensureModel 在目录到达后为没有模型的对话补上默认模型
  function ensureModel(): void {
    if (session.settings.model === '' && models().length > 0) {
      const model = defaultChatModel(models())
      session.settings = {
        ...defaultSettings(model),
        systemInstruction: session.settings.systemInstruction,
      }
    }
  }

  // selectModel 切换模型：参数换为新模型默认值，去掉新模型不支持的工具
  function selectModel(id: string): void {
    const model = models().find((item) => item.id === id)
    Object.assign(session.settings, modelDefaults(model))
    session.settings.tools = session.settings.tools.filter((tool) =>
      hasCode(model, toolCodes[tool]),
    )
    const protocols = protocolsFor(model)
    if (!protocols.some((item) => item.value === session.settings.protocol))
      session.settings.protocol = protocols[0]!.value
  }

  // promptTitle 以首条用户输入的第一行作为对话标题
  function promptTitle(): string {
    const first = session.turns.find((turn) => turn.role === 'user')
    const text =
      first?.parts
        .find((part) => part.text)
        ?.text?.trim()
        .split('\n')[0] ?? ''
    return text.length > 60 ? `${text.slice(0, 60)}…` : text
  }

  // discard 取消 token 统计并中止生成。
  function discard(): void {
    window.clearTimeout(tokenTimer)
    window.clearTimeout(draftTimer)
    tokenTimer = draftTimer = 0
    tokenController = draftController = undefined
    controller?.abort()
  }

  // hasSystemInput 表示当前模型支持系统指令且系统指令不为空
  function hasSystemInput(): boolean {
    return (
      session.settings.systemInstruction.trim() !== '' &&
      hasCode(currentModel.value, Capability.SystemInstruction)
    )
  }

  // countPrompt 与官网一致统计若干轮次与系统指令的输入 token：已按抽帧计数的视频取其 token 数，其余内容用 countTokens 统计，turns 为实际统计的轮次
  async function countPrompt(
    model: Model,
    source: Turn[],
    hasSystem: boolean,
    signal: AbortSignal,
  ) {
    const framed = source.flatMap((turn) => turn.parts.filter(framedVideo))
    const turns = source
      .map((turn) => ({ ...turn, parts: turn.parts.filter((part) => !framedVideo(part)) }))
      .filter((turn) => turn.parts.length > 0)
    const counted: TokenCountResult =
      turns.length === 0 && !hasSystem
        ? { totalTokens: 0 }
        : await countTokensDetailed(
            model.id,
            apiKey(),
            buildRequest(session.settings, model, turns),
            signal,
          )
    const count = framed.reduce((sum, part) => sum + part.tokenCount!, counted.totalTokens)
    return { count, counted, turns }
  }

  // refreshTokens 统计当前对话与系统指令的输入 token，系统指令与末尾模型轮次的 token 取统计的逐轮明细；统计返回前有保存写入时，返回后再保存一次
  async function refreshTokens(): Promise<void> {
    window.clearTimeout(tokenTimer)
    tokenTimer = 0
    tokenController = undefined
    const model = currentModel.value
    const hasSystem = hasSystemInput()
    if (!hasSystem) session.systemTokens = null
    if (model === undefined || (session.turns.length === 0 && !hasSystem)) {
      session.promptTokens = null
      return
    }
    const counter = new AbortController()
    tokenController = counter
    try {
      const { count, counted, turns } = await countPrompt(
        model,
        session.turns,
        hasSystem,
        counter.signal,
      )
      if (tokenController !== counter) return
      tokenController = undefined
      session.promptTokens = count
      session.systemTokens = hasSystem ? (counted.systemTokens ?? null) : null
      const last = session.turns.at(-1)
      // tail 是末尾模型轮次正文的 token 数，CountTokens 不给逐轮明细时取该轮生成用量中思考以外的部分
      const tail =
        counted.contentTokens?.at(-1) ??
        (last?.tokens === undefined ? undefined : last.tokens - (last.thoughtTokens ?? 0))
      if (last?.role === 'model' && turns.at(-1)?.id === last.id && tail !== undefined)
        lastRun.value = { id: last.id, input: count - tail, output: last.tokens ?? tail }
      else if (last?.role !== 'model') lastRun.value = null
    } catch {
      // 统计失败时保留上一次的数字
      if (tokenController === counter) tokenController = undefined
    }
  }

  // scheduleTokens 在 delay 毫秒后重新统计对话与系统指令，到时前的多次调用合并为一次
  function scheduleTokens(delay = 0): void {
    window.clearTimeout(tokenTimer)
    tokenTimer = window.setTimeout(() => {
      tokenTimer = 0
      if (!session.running) void refreshTokens()
    }, delay)
  }

  // scheduleDraftTokens 与官网一致：输入框文本停止变化 500 ms 后只统计输入框文本，文本清空时立即归零
  function scheduleDraftTokens(): void {
    window.clearTimeout(draftTimer)
    draftTimer = 0
    draftController = undefined
    const text = draftSource().text
    const model = currentModel.value
    if (text.trim() === '' || model === undefined) {
      session.draftTokens = 0
      return
    }
    draftTimer = window.setTimeout(async () => {
      draftTimer = 0
      const counter = new AbortController()
      draftController = counter
      try {
        const count = await countTokens(
          model.id,
          apiKey(),
          { contents: [{ role: 'user', parts: [{ text }] }], generationConfig: {} },
          counter.signal,
        )
        if (draftController === counter) session.draftTokens = count
      } catch {
        // 统计失败时保留上一次的数字
      }
    }, draftCountDelay)
  }

  // countAttachment 与官网一致：附件加入时单独统计它的 token 数，写入片段的 tokenCount；已上传 Drive 的视频按视频设置抽帧计数
  async function countAttachment(part: Part): Promise<void> {
    const model = currentModel.value
    if (model === undefined) return
    const request = {}
    attachmentCounts.set(toRaw(part), request)
    try {
      if (uploadedVideo(part)) {
        await renewUploads([{ id: '', role: 'user', parts: [part], created: 0 }])
      }
      const count = uploadedVideo(part)
        ? await countVideoFrames(model.id, part.fileData!.fileUri, part.videoMetadata)
        : await countTokens(
            model.id,
            apiKey(),
            { contents: [{ role: 'user', parts: [apiPart(part)] }], generationConfig: {} },
            new AbortController().signal,
          )
      if (attachmentCounts.get(toRaw(part)) === request) part.tokenCount = count
    } catch {
      // 统计失败时附件不显示 token 数
    }
  }

  // append 把输入框内容作为用户轮次加入对话，输入为空时返回 false。
  function append(): boolean {
    const text = session.draft.trim()
    if (text === '' && session.attachments.length === 0) return false
    const parts: Part[] = [...session.attachments]
    if (text !== '') parts.push({ text: session.draft })
    session.turns.push({ id: newTurnID(), role: 'user', parts, created: Date.now() })
    session.draft = ''
    session.attachments = []
    if (session.title === '') session.title = promptTitle()
    return true
  }

  // submit 发送输入框内容；输入为空且最后一轮为用户轮次时直接运行
  async function submit(): Promise<void> {
    if (!append() && session.turns.at(-1)?.role !== 'user') return
    await run()
  }

  // run 以当前对话生成一个模型轮次
  async function run(): Promise<void> {
    const model = currentModel.value
    if (model === undefined || session.running) return
    const input = continuation(session.settings.protocol, session.turns)
    // sent 是本次发送的轮次，不含随后加入的模型轮次
    const sent = [...input.turns]
    const turn = reactive<Turn>({
      id: newTurnID(),
      role: 'model',
      parts: [],
      created: Date.now(),
      ...(session.settings.tools.includes('code_execution') && { agent: true }),
    })
    // owner 是本次生成所属对话的轮次，打开其他对话或开始新对话时被整体替换
    const owner = session.turns
    owner.push(turn)
    session.running = true
    session.started = performance.now()
    controller = new AbortController()
    const signal = controller.signal
    try {
      await renewUploads(sent, signal)
      void refreshTokens()
      await generate({
        protocol: session.settings.protocol,
        stream: session.settings.stream,
        model,
        modelID: model.id,
        apiKey: apiKey(),
        request: buildRequest(session.settings, model, sent),
        images: {
          count: session.settings.imageCount,
          size: session.settings.openAISize,
          quality: session.settings.imageQuality,
        },
        agent: agentFields(session.settings, model),
        previous: input.previous,
        signal,
        onUpdate: (update) => {
          const elapsed = Math.round(performance.now() - session.started)
          if (turn.latency === undefined && update.parts.length > 0) turn.latency = elapsed
          if (
            turn.thinking === undefined &&
            turn.parts.some((part) => part.thought) &&
            update.parts.some(
              (part) => !part.thought && !(part.text === '' && part.thoughtSignature),
            )
          )
            turn.thinking = elapsed
          appendParts(turn.parts, update.parts)
          if (update.interactionId) turn.interactionId = update.interactionId
          if (update.environmentId)
            Object.assign(session.settings.environment, {
              type: 'existing',
              id: update.environmentId,
              produced: update.environmentId,
            })
          if (update.finishReason) turn.finishReason = update.finishReason
          if (update.tokens !== undefined) turn.tokens = update.tokens
          if (update.thoughtTokens !== undefined) turn.thoughtTokens = update.thoughtTokens
          if (update.cachedTokens !== undefined) turn.cachedTokens = update.cachedTokens
          if (update.promptTokens !== undefined) {
            tokenController = undefined
            session.promptTokens = update.promptTokens + (turn.tokens ?? 0)
            lastRun.value = { id: turn.id, input: update.promptTokens, output: turn.tokens ?? 0 }
          }
          if (update.sources) turn.sources = update.sources
          if (update.supports) turn.supports = update.supports
          if (update.searchQueries) turn.searchQueries = update.searchQueries
        },
      })
    } catch (error) {
      if (!signal.aborted) {
        turn.error = failureText(error, useI18n().t('pg.chatUnreachable'))
        useConsole().showNotice(useI18n().t('pg.generateFailed'), 'error')
      }
    } finally {
      turn.duration = Math.round(performance.now() - session.started)
      if (turn.thinking === undefined && turn.parts.some((part) => part.thought))
        turn.thinking = turn.duration
      session.running = false
      controller = undefined
      const index = session.turns.indexOf(turn)
      if (
        index !== -1 &&
        turn.parts.length === 0 &&
        turn.error === undefined &&
        turn.finishReason === undefined
      )
        session.turns.splice(index, 1)
    }
  }

  // stop 中止当前生成
  function stop(): void {
    controller?.abort()
  }

  // rerun 从某一轮重新生成：模型轮次替换自身，用户轮次替换其后的回复
  function rerun(index: number): void {
    if (session.running) return
    session.turns.splice(session.turns[index]!.role === 'model' ? index : index + 1)
    void run()
  }

  // respond 以用户填写的函数响应继续生成：响应为 JSON 对象时原样发送，否则作为 result 字段发送
  function respond(call: NonNullable<Part['functionCall']>, text: string): void {
    let response: Record<string, unknown>
    try {
      const value: unknown = JSON.parse(text)
      response =
        typeof value === 'object' && value !== null && !Array.isArray(value)
          ? (value as Record<string, unknown>)
          : { result: value }
    } catch {
      response = { result: text }
    }
    session.turns.push({
      id: newTurnID(),
      role: 'user',
      parts: [
        { functionResponse: { name: call.name, response, ...(call.id ? { id: call.id } : {}) } },
      ],
      created: Date.now(),
    })
    void run()
  }

  // forgetInteractions 去掉从 index 起各模型轮的交互 ID 与缓存命中数：这些交互的上游上下文含有被修改的轮次，之后不从它们续接
  function forgetInteractions(index: number): void {
    for (const turn of session.turns.slice(index)) {
      delete turn.interactionId
      delete turn.cachedTokens
    }
  }

  // editTurn 用新文本替换某一轮的正文：新文本放在首个正文片段的位置，其他片段保持原有顺序；文本有变化时其后的交互不再用于续接，模型轮次的生成用量失效，重新统计 token
  function editTurn(index: number, text: string): void {
    const turn = session.turns[index]!
    const body = (part: Part) => part.text !== undefined && !part.thought
    const current = turn.parts
      .filter(body)
      .map((part) => part.text)
      .join('')
    const changed = text !== current
    if (changed) {
      forgetInteractions(index)
      delete turn.tokens
      delete turn.thoughtTokens
    }
    const first = turn.parts.findIndex(body)
    turn.parts = turn.parts.flatMap((part, at) =>
      !body(part) ? [part] : at === first && text !== '' ? [{ text }] : [],
    )
    if (first === -1 && text !== '') turn.parts.push({ text })
    if (changed) scheduleTokens()
  }

  // deleteTurn 删除一轮并重新统计 token，其后的交互不再用于续接
  function deleteTurn(index: number): void {
    session.turns.splice(index, 1)
    forgetInteractions(index)
    scheduleTokens()
  }

  // branch 在当前页面保留指定轮次，作为新分支继续测试。
  function branch(index: number): void {
    discard()
    session.turns = cloneData(session.turns.slice(0, index + 1))
    session.running = false
    lastRun.value = null
    scheduleTokens()
  }

  // applyStarter 按官网空态示例填写输入框：提示词、参考图片与搜索、代码执行开关，模型支持时思考档位设为 HIGH
  async function applyStarter(item: ZeroStarter): Promise<void> {
    const model = currentModel.value
    if (model?.thinking_levels?.includes('HIGH')) session.settings.thinkingLevel = 'HIGH'
    const enable = (tool: Tool) => {
      if (hasCode(model, toolCodes[tool]) && toolCompatible(model, tool, session.settings.tools))
        session.settings.tools = [...new Set([...session.settings.tools, tool])]
    }
    if (item.search) enable('google_search')
    if (item.codeExecution) enable('code_execution')
    session.draft = item.prompt
    session.attachments = []
    try {
      for (const url of item.images ?? []) {
        const response = await fetch(url)
        if (!response.ok) throw new Error(`${response.status} ${url}`)
        session.attachments.push({ inlineData: await readImage(await response.blob()) })
        void countAttachment(session.attachments.at(-1)!)
      }
    } catch (error) {
      const failed = useI18n().t('pg.sampleImageFailed')
      useConsole().showNotice(failureText(error, failed, failed), 'error')
    }
  }

  // dispose 中止生成并停止监听。
  function dispose(): void {
    discard()
    scope.stop()
  }

  scope.run(() => {
    watch(
      () => currentModel.value?.id,
      () => {
        scheduleTokens()
        scheduleDraftTokens()
      },
      { immediate: true },
    )
    watch(() => draftSource().text, scheduleDraftTokens)
    watch(
      () => [session.settings, session.settings.systemInstruction] as const,
      ([settings], [previous]) => {
        if (settings === previous) scheduleTokens(draftCountDelay)
      },
    )
  })

  return {
    session,
    currentModel,
    usage,
    newChat,
    ensureModel,
    selectModel,
    append,
    submit,
    run,
    stop,
    rerun,
    respond,
    editTurn,
    deleteTurn,
    branch,
    applyStarter,
    refreshTokens,
    countAttachment,
    dispose,
  }
}

// Playground 是一个对话实例与页面级操作
export type Playground = ReturnType<typeof createSession>

// main 是页面对话，对比模式下为左侧窗格。
const main = createSession()

// newChat 开始一个空白对话并退出对比模式。
function newChat(systemInstruction = ''): void {
  leaveCompare()
  main.newChat(systemInstruction)
}

// page 为对话实例加上页面级操作
function page(instance: ReturnType<typeof createSession>): Playground {
  return { ...instance, newChat }
}

// primary 是页面对话及其操作
const primary = page(main)

// panes 是并排显示的对话，单对话时只有 primary
const panes = shallowReactive<Playground[]>([primary])

// comparing 表示处于对比模式
const comparing = computed(() => panes.length > 1)

// 关闭桌面窗口时保留有对话、正在生成或尚未发送的页面。
keepPage(() =>
  panes.some(
    ({ session }) =>
      session.running ||
      session.turns.length > 0 ||
      session.draft.trim() !== '' ||
      session.attachments.length > 0,
  ),
)

// syncInstructions 与官网一致表示对比模式两侧的系统指令同步，进入对比模式时开启
const syncInstructions = ref(false)

// 同步系统指令时，一侧的系统指令变化后写入其他窗格
watch(
  () => panes.map((pane) => pane.session.settings.systemInstruction),
  (values, previous) => {
    if (!comparing.value || !syncInstructions.value) return
    const changed = values.findIndex((value, index) => value !== previous[index])
    const text = values[changed]
    if (text === undefined) return
    for (const pane of panes) pane.session.settings.systemInstruction = text
  },
)

// setSync 切换系统指令同步，开启时把 index 窗格的指令写入其他窗格。
function setSync(index: number, value: boolean): void {
  syncInstructions.value = value
  if (!value) return
  const text = panes[index]!.session.settings.systemInstruction
  for (const pane of panes.slice(1)) pane.session.settings.systemInstruction = text
}

// running 表示有窗格正在生成
const running = computed(() => panes.some((pane) => pane.session.running))

// addPane 以 data 的运行设置与对话加入右侧窗格。
function addPane(data: PaneData): Playground {
  const pane = page(
    createSession(
      {
        draft: () => ({ text: main.session.draft, attachments: main.session.attachments }),
      },
      data,
    ),
  )
  panes.push(pane)
  return pane
}

// enterCompare 与官网一致：复制当前对话与运行设置作为右侧窗格
function enterCompare(): void {
  if (comparing.value) return
  const pane = addPane({
    settings: main.session.settings,
    turns: main.session.turns.map((turn) => ({ ...turn, id: newTurnID() })),
  })
  pane.session.promptTokens = main.session.promptTokens
  pane.session.systemTokens = main.session.systemTokens
  pane.session.rawMode = main.session.rawMode
  syncInstructions.value = true
}

// leaveCompare 关闭右侧窗格回到单对话
function leaveCompare(): void {
  for (const pane of panes.splice(1)) pane.dispose()
}

// closePane 关闭一个窗格：关闭左侧时右侧的对话与运行设置成为页面对话
function closePane(index: number): void {
  const right = panes[1]!
  if (index === 0) {
    main.stop()
    right.stop()
    Object.assign(main.session, {
      settings: right.session.settings,
      turns: right.session.turns,
      promptTokens: right.session.promptTokens,
    })
  }
  leaveCompare()
}

// shareDraft 把输入框内容复制到其他窗格
function shareDraft(): void {
  for (const pane of panes.slice(1)) {
    pane.session.draft = main.session.draft
    pane.session.attachments = cloneData(main.session.attachments)
  }
}

// submitAll 把输入框内容作为用户轮次同时发往每个窗格
async function submitAll(): Promise<void> {
  shareDraft()
  await Promise.all(panes.map((pane) => pane.submit()))
}

// appendAll 把输入框内容作为用户轮次加入每个窗格但不运行
function appendAll(): void {
  shareDraft()
  for (const pane of panes) pane.append()
}

// stopAll 中止所有窗格的生成
function stopAll(): void {
  for (const pane of panes) pane.stop()
}

// toggleRaw 切换所有窗格的原始模式
function toggleRaw(): void {
  const raw = !main.session.rawMode
  for (const pane of panes) pane.session.rawMode = raw
}

// playgroundKey 是窗格向子组件提供对话实例的注入键
const playgroundKey: InjectionKey<Playground> = Symbol('playground')

// providePlayground 让子组件的 usePlayground 返回该窗格的对话
export function providePlayground(instance: Playground): void {
  provide(playgroundKey, instance)
}

// usePlayground 返回所在窗格的对话与操作，窗格外为页面对话
export function usePlayground(): Playground {
  return hasInjectionContext() ? inject(playgroundKey, primary) : primary
}

// useCompare 返回对比模式的窗格与操作
export function useCompare() {
  return {
    panes,
    comparing,
    running,
    enterCompare,
    closePane,
    syncInstructions,
    setSync,
    submitAll,
    appendAll,
    stopAll,
    toggleRaw,
  }
}

// localPresetsKey 是浏览器本机保存系统指令预设的键，读取时并入对话库后删除
const localPresetsKey = 'playground_system_instructions'

// useInstructionPresets 管理保存在对话库中的系统指令预设
export function useInstructionPresets(
  open: () => boolean,
  getText: () => string,
  setText: (value: string) => void,
) {
  const presets = ref<SystemInstructionPreset[]>([])
  const selected = ref('')
  const title = ref('')
  const saved = ref(true)
  const saveError = ref(false)
  const { errorText } = useI18n()
  const { showNotice } = useConsole()
  let saveTimer = 0

  // currentPreset 是当前选中的系统指令预设
  const currentPreset = computed(() => presets.value.find((preset) => preset.id === selected.value))

  // syncSelection 按当前系统指令匹配预设选中态
  function syncSelection(): void {
    const match = presets.value.find((preset) => preset.text === getText())
    selected.value = match?.id ?? ''
    title.value = match?.title ?? ''
  }

  // persist 等待一次预设写入并更新保存状态
  async function persist(write: Promise<void>): Promise<void> {
    saved.value = false
    saveError.value = false
    try {
      await write
      saved.value = saveTimer === 0
    } catch (cause) {
      saveError.value = true
      showNotice(errorText(cause), 'error')
    }
  }

  // commit 把当前标题与系统指令写入预设列表并保存
  function commit(): void {
    const text = getText()
    if (text.trim() === '' && title.value.trim() === '') {
      saved.value = true
      return
    }
    let preset = currentPreset.value
    if (preset === undefined) {
      preset = { id: newTurnID(), title: '', text: '' }
      presets.value.push(preset)
      selected.value = preset.id
    }
    preset.title = title.value
    preset.text = text
    void persist(api.saveSystemInstruction(preset))
  }

  // flush 立即提交待保存的系统指令预设
  function flush(): void {
    if (saveTimer === 0) return
    window.clearTimeout(saveTimer)
    saveTimer = 0
    commit()
  }

  // remember 在编辑后合并保存当前指令到预设
  function remember(): void {
    saved.value = false
    saveError.value = false
    window.clearTimeout(saveTimer)
    saveTimer = window.setTimeout(() => {
      saveTimer = 0
      commit()
    }, 500)
  }

  // choose 切换到另一条预设或新建空白指令
  function choose(id: string | number | undefined): void {
    flush()
    selected.value = String(id ?? '')
    const preset = currentPreset.value
    title.value = preset?.title ?? ''
    setText(preset?.text ?? '')
  }

  // remove 删除当前预设并清空指令
  function remove(): void {
    window.clearTimeout(saveTimer)
    saveTimer = 0
    const id = selected.value
    presets.value = presets.value.filter((preset) => preset.id !== id)
    void persist(api.deleteSystemInstruction(id))
    choose('')
  }

  // refresh 从服务端读取预设，并入浏览器本机保存的预设后匹配当前指令
  async function refresh(): Promise<void> {
    try {
      presets.value = await api.systemInstructions()
      const local = localStorage.getItem(localPresetsKey)
      if (local !== null) {
        const remote = presets.value
        const missing = (JSON.parse(local) as SystemInstructionPreset[]).filter(
          (item) =>
            !remote.some(
              (preset) =>
                preset.id === item.id || (preset.title === item.title && preset.text === item.text),
            ),
        )
        for (const preset of missing) await api.saveSystemInstruction(preset)
        presets.value = [...remote, ...missing]
        localStorage.removeItem(localPresetsKey)
      }
      syncSelection()
    } catch (cause) {
      showNotice(errorText(cause), 'error')
    }
  }

  watch(open, (value) => {
    if (value) void refresh()
    else flush()
  })

  return { presets, selected, title, saved, saveError, currentPreset, remember, choose, remove }
}
