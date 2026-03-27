<script setup lang="ts">
import { useEditorStore } from '@/stores/editor';
import { useEventStore } from '@/stores/events';
import { useProviderStore } from '@/stores/providers';
import { marked } from 'marked';
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue';

// Configure marked for safe rendering with workspace file links
const renderer = new marked.Renderer()
// Escape raw HTML in AI responses (prevents forms, scripts, iframes from rendering)
renderer.html = function ({ text }: { text: string }) {
  return text.replace(/</g, '&lt;').replace(/>/g, '&gt;')
}
const origLink = renderer.link
renderer.link = function (token: any) {
  if (token.href && token.href.startsWith('ws:')) {
    const path = token.href.slice(3)
    const text = token.text || path
    return `<a class="ws-file-link" data-ws-path="${path}" title="Open ${path}">\u{1F4C4} ${text}</a>`
  }
  const html = origLink.call(this, token)
  // Open external links in new tab
  if (token.href && /^https?:\/\//.test(token.href)) {
    return html.replace('<a ', '<a target="_blank" rel="noopener noreferrer" ')
  }
  return html
}
const origImage = renderer.image
renderer.image = function (token: any) {
  if (token.href && token.href.startsWith('ws:')) {
    const path = token.href.slice(3)
    const alt = token.text || path
    const src = `/api/download?path=${encodeURIComponent(path)}`
    return `<a class="ws-file-link" data-ws-path="${path}" title="Open ${path}"><img src="${src}" alt="${alt}" style="max-width:300px;max-height:200px;border-radius:8px;cursor:pointer;" /></a>`
  }
  return origImage.call(this, token)
}
marked.setOptions({
  breaks: true,
  gfm: true,
  renderer,
})

const props = defineProps<{
  chatId: number
}>()
const emit = defineEmits<{
  (e: 'chat-created', chatId: number): void
  (e: 'open-settings'): void
}>()

const editorStore = useEditorStore()
const eventStore = useEventStore()
const providerStore = useProviderStore()

// Module-level state for event-driven chat
let assistantIdx = 0
let fullText = ''

interface HistoryEntry {
  id: number
  code: string
  result: string
  response: string
  responseHtml?: string
  iteration: number
  block: number
  provider: string
  agent_type: string
  created: string
}

interface ChatMessage {
  role: 'user' | 'assistant' | 'system' | 'log' | 'error'
  content: string
  contentHtml?: string
  historyId?: string
  fileContent?: string
  historyEntry?: HistoryEntry
  expanded?: boolean
  historyEntries?: HistoryEntry[]
  historyExpanded?: boolean
  hasHistory?: boolean
  historyLoading?: boolean
  isDoc?: boolean
  fullDoc?: string
  docExpanded?: boolean
  logEntries?: { display: string, id?: string, entry?: HistoryEntry, fileContent?: string, expanded?: boolean, isDoc?: boolean, fullDoc?: string, docExpanded?: boolean }[]
  logsExpanded?: boolean
  isCron?: boolean
  contentExpanded?: boolean
  turnId?: string
  promptTokens?: number
  completionTokens?: number
  pending?: boolean
}

async function renderMarkdown(text: string): Promise<string> {
  try {
    const resp = await fetch('/api/render-markdown', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ markdown: text }),
    })
    if (resp.ok) {
      const data = await resp.json()
      return data.html || ''
    }
  } catch { /* fall through */ }
  // Fallback to client-side marked if backend fails
  return marked.parse(text) as string
}

const activeChatId = ref(props.chatId)
const messages = ref<ChatMessage[]>([
  { role: 'system', content: 'Welcome to Altclaw! Send a message to get started.' },
])
const input = ref('')
const sending = ref(false)
const pendingAsk = ref<{ question: string } | null>(null)
const pendingConfirm = ref<{ action: string; label: string; summary: string; params: Record<string, any> } | null>(null)
const messagesEl = ref<HTMLElement | null>(null)
const inputEl = ref<HTMLTextAreaElement | null>(null)
const attachedFiles = ref<string[]>([])
const dragOver = ref(false)
const hasMore = ref(false)
let messagesLoaded = false
let chatCursor = ''

const providerOpen = ref(false)

function onClickOutsideProvider(e: MouseEvent) {
  const el = (e.target as HTMLElement).closest('.provider-dropdown')
  if (!el) providerOpen.value = false
}

// Restore messages when chatId is set
async function restoreMessages() {
  if (!activeChatId.value) return
  try {
    const resp = await fetch('/api/chats/' + activeChatId.value + '?limit=20')
    if (!resp.ok) return
    const data = await resp.json()
    const list = data.messages || data
    if (Array.isArray(list) && list.length > 0) {
      messages.value = list.map((m: { id: string; role: string; content: string; prompt_tokens?: number; completion_tokens?: number }): ChatMessage => ({
        role: m.role as ChatMessage['role'],
        content: m.content,
        hasHistory: m.role === 'assistant',
        turnId: m.role === 'assistant' ? m.id : undefined,
        promptTokens: m.prompt_tokens,
        completionTokens: m.completion_tokens,
      }))
      chatCursor = data.cursor || ''
      hasMore.value = chatCursor !== ''
      messagesLoaded = true
      scrollBottom()
      // Render markdown via backend for all restored messages
      for (const msg of messages.value) {
        if ((msg.role === 'assistant' || msg.role === 'user') && msg.content) {
          renderMarkdown(msg.content).then(html => { msg.contentHtml = html })
        }
      }
    }
  } catch { /* ignore */ }
}

async function loadMoreMessages() {
  if (!hasMore.value || !chatCursor || !activeChatId.value) return
  const el = messagesEl.value
  const prevHeight = el ? el.scrollHeight : 0
  try {
    const resp = await fetch('/api/chats/' + activeChatId.value + '?limit=20&cursor=' + encodeURIComponent(chatCursor))
    if (!resp.ok) return
    const data = await resp.json()
    const list = data.messages || []
    if (Array.isArray(list) && list.length > 0) {
      const older: ChatMessage[] = list.map((m: { id: string; role: string; content: string; prompt_tokens?: number; completion_tokens?: number }): ChatMessage => ({
        role: m.role as ChatMessage['role'],
        content: m.content,
        hasHistory: m.role === 'assistant',
        turnId: m.role === 'assistant' ? m.id : undefined,
        promptTokens: m.prompt_tokens,
        completionTokens: m.completion_tokens,
      }))
      messages.value = [...older, ...messages.value]
      // Render markdown for newly loaded messages
      for (const msg of older) {
        if ((msg.role === 'assistant' || msg.role === 'user') && msg.content) {
          renderMarkdown(msg.content).then(html => { msg.contentHtml = html })
        }
      }
      // Preserve scroll position
      nextTick(() => {
        if (el) el.scrollTop = el.scrollHeight - prevHeight
      })
    }
    chatCursor = data.cursor || ''
    hasMore.value = chatCursor !== ''
  } catch { /* ignore */ }
}

// Unified event handler for all chat events
function onChatEvent(evt: any) {
  // Helper: if events arrive without a preceding meta (reconnect), set up assistant placeholder
  function ensureSending() {
    if (!sending.value) {
      sending.value = true
      assistantIdx = messages.value.length
      fullText = ''
      messages.value.push({ role: 'assistant', content: '' })
    }
  }

  switch (evt.type) {
    case 'meta':
      if (evt.chat_id && evt.chat_id !== activeChatId.value) {
        activeChatId.value = evt.chat_id
        emit('chat-created', evt.chat_id)
      }
      ensureSending()
      break
    case 'chunk':
      ensureSending()
      fullText += evt.content
      if (assistantIdx < messages.value.length) {
        messages.value[assistantIdx] = { role: 'assistant', content: fullText }
      }
      scrollBottom()
      break
    case 'log': {
      ensureSending()
      const parsed = parseLogMessage(evt.content)
      const logEntry: any = {
        display: parsed.display,
        id: parsed.id,
        expanded: false,
      }
      // Detect === doc blocks and collapse them
      const docMatch = parsed.display.match(/=== (.+?) ===/)
      if (docMatch && parsed.display.includes('=== end ===')) {
        logEntry.isDoc = true
        logEntry.fullDoc = parsed.display
        logEntry.display = `📖 ${docMatch[1]} docs loaded`
        logEntry.docExpanded = false
      }
      // Find or create a log-group message right before the assistant placeholder
      const groupIdx = assistantIdx - 1
      let group = groupIdx >= 0 ? messages.value[groupIdx] : null
      if (!group || group.role !== 'log' || !group.logEntries) {
        // Create a new log group
        group = { role: 'log', content: logEntry.display, logEntries: [logEntry], logsExpanded: false }
        messages.value.splice(assistantIdx, 0, group)
        assistantIdx++
      } else {
        // Append to existing log group
        group.logEntries!.push(logEntry)
        group.content = logEntry.display // show latest entry as summary
      }
      scrollBottom()
      break
    }
    case 'pending_msg': {
      // Another tab injected a message — show it if not already present
      const alreadyShown = messages.value.some(m => m.role === 'user' && m.pending && m.content === evt.content)
      if (!alreadyShown) {
        const pendingMsg: ChatMessage = { role: 'user', content: evt.content, pending: true }
        messages.value.splice(assistantIdx, 0, pendingMsg)
        assistantIdx++
        renderMarkdown(evt.content).then(html => { pendingMsg.contentHtml = html })
      }
      scrollBottom()
      break
    }
    case 'done':
      ensureSending()
      pendingAsk.value = null
      if (assistantIdx < messages.value.length) {
        messages.value[assistantIdx] = { role: 'assistant', content: evt.content, hasHistory: true, turnId: evt.message_id }
        // Render final content via backend for XSS-safe HTML
        renderMarkdown(evt.content).then(html => {
          const msg = messages.value[assistantIdx]
          if (msg) {
            msg.contentHtml = html
          }
        })
      }
      // Clear pending flags on user messages
      for (const m of messages.value) {
        if (m.pending) m.pending = false
      }
      sending.value = false
      scrollBottom()
      break
    case 'error':
      ensureSending()
      pendingAsk.value = null
      // Remove the empty assistant placeholder and show a distinct error message
      if (assistantIdx < messages.value.length) {
        messages.value.splice(assistantIdx, 1)
      }
      messages.value.push({ role: 'error', content: evt.content })
      sending.value = false
      scrollBottom()
      break
    case 'cron': {
      // Cron events display on their own line without entering thinking state
      const cronMsg: ChatMessage = {
        role: 'log',
        content: evt.content,
        isCron: true,
      }
      messages.value.push(cronMsg)
      scrollBottom()
      break
    }
    case 'ask':
      ensureSending()
      pendingAsk.value = { question: evt.content }
      messages.value.splice(assistantIdx, 0, { role: 'log', content: `❓ ${evt.content}`, isCron: true })
      assistantIdx++
      scrollBottom()
      break
    case 'confirm': {
      ensureSending()
      try {
        const payload = JSON.parse(evt.content)
        pendingConfirm.value = {
          action: payload.action,
          label: payload.label,
          summary: payload.summary,
          params: payload.params || {},
        }
      } catch {
        pendingConfirm.value = { action: 'unknown', label: 'Action Requested', summary: evt.content, params: {} }
      }
      messages.value.splice(assistantIdx, 0, { role: 'log', content: `🔐 ${pendingConfirm.value.label}`, isCron: true })
      assistantIdx++
      scrollBottom()
      break
    }
  }
}
watch(() => props.chatId, (id) => {
  activeChatId.value = id
  if (sending.value) return // don't overwrite in-progress response
  pendingAsk.value = null
  messagesLoaded = false
  chatCursor = ''
  hasMore.value = false
  if (id) restoreMessages()
})

// Also restore when this chat tab becomes the active tab (v-show visibility)
const chatPath = `special://chat-${props.chatId}`
watch(() => editorStore.activeFilePath, (path) => {
  if (path === chatPath || path === `special://chat-${activeChatId.value}`) {
    scrollBottom()
    if (activeChatId.value && !messagesLoaded && !sending.value) {
      restoreMessages()
    }
  }
})

function scrollBottom() {
  nextTick(() => {
    if (messagesEl.value) {
      messagesEl.value.scrollTop = messagesEl.value.scrollHeight
    }
  })
}

function parseLogMessage(content: string): { id?: string; display: string } {
  const runMatch = content.match(/⚡ Running \[(\d+)\/(\d+)\]: #(\d+)/)
  if (runMatch) {
    const step = runMatch[1]
    const total = runMatch[2]
    return { id: runMatch[3], display: `Working on it... (step ${step}/${total})` }
  }
  return { display: content }
}

async function toggleFile(entry: any) {
  if (!entry.id || !activeChatId.value) return
  if (entry.expanded) {
    entry.expanded = false
    return
  }
  if (!entry.entry) {
    try {
      // Find the log group that contains this entry, then the next assistant message
      const logGroupIdx = messages.value?.findIndex(m => m.role === 'log' && m.logEntries?.includes(entry)) ?? -1
      const rest = logGroupIdx >= 0 ? messages.value?.slice(logGroupIdx + 1) ?? [] : []
      const assistantMsg = rest.find(m => m.role === 'assistant' && m.turnId)
      const url = assistantMsg?.turnId
        ? `/api/history/${activeChatId.value}/${assistantMsg.turnId}`
        : `/api/history/${activeChatId.value}`
      const resp = await fetch(url)
      const entries: HistoryEntry[] = await resp.json()
      const match = entries.find((e: HistoryEntry) => e.id === Number(entry.id))
      if (match) {
        entry.entry = match
        entry.fileContent = match.code || '(empty)'
        // Pre-render response markdown via backend
        if (match.response) {
          renderMarkdown(match.response).then(html => { match.responseHtml = html })
        }
      } else {
        entry.fileContent = '(not found)'
      }
    } catch (e: unknown) {
      entry.fileContent = 'Error loading: ' + (e instanceof Error ? e.message : String(e))
    }
  }
  entry.expanded = true
}

async function toggleHistory(msg: ChatMessage) {
  if (msg.historyExpanded) {
    msg.historyExpanded = false
    return
  }
  // Load on demand if not yet fetched
  if (!msg.historyEntries && activeChatId.value) {
    msg.historyLoading = true
    try {
      // If we have a turnId, fetch only that turn's entries; otherwise fall back to all
      const url = msg.turnId
        ? '/api/history/' + activeChatId.value + '/' + msg.turnId
        : '/api/history/' + activeChatId.value
      const resp = await fetch(url)
      if (resp.ok) {
        const entries: HistoryEntry[] = await resp.json()
        if (Array.isArray(entries) && entries.length > 0) {
          msg.historyEntries = [...entries].sort((a, b) => new Date(a.created).getTime() - new Date(b.created).getTime())
          // Pre-render history response markdown via backend
          for (const e of msg.historyEntries) {
            if (e.response) {
              renderMarkdown(e.response).then(html => { (e as any).responseHtml = html })
            }
          }
        } else {
          msg.historyEntries = []
        }
      }
    } catch { /* ignore */ }
    msg.historyLoading = false
  }
  msg.historyExpanded = true
}

const copiedMsgIdx = ref<number | null>(null)
function copyMessage(content: string, idx: number) {
  navigator.clipboard.writeText(content)
  copiedMsgIdx.value = idx
  setTimeout(() => { copiedMsgIdx.value = null }, 1500)
}

const copiedSteps = ref<number | null>(null)
function copySteps(msg: ChatMessage, idx: number) {
  if (!msg.historyEntries?.length) return
  const text = msg.historyEntries.map((e) => {
    const meta = [e.provider, e.agent_type].filter(Boolean).join(' · ')
    const header = `### Step ${(e.iteration ?? 0) + 1}` + (meta ? ` (${meta})` : '')
    let block = `${header}\n\`\`\`javascript\n${e.code}\n\`\`\``
    if (e.result) block += `\n**Result:** ${e.result}`
    if (e.response) block += `\n\n<details>\n<summary>AI Response</summary>\n\n${e.response}\n</details>`
    return block
  }).join('\n\n')
  const full = `## AI Response\n${msg.content}\n\n## Execution Steps\n${text}`
  navigator.clipboard.writeText(full)
  copiedSteps.value = idx
  setTimeout(() => { copiedSteps.value = null }, 1500)
}

async function stopExecution() {
  pendingAsk.value = null
  pendingConfirm.value = null
  await fetch('/api/stop', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ chat_id: activeChatId.value }),
  })
}

async function answerConfirm(approved: boolean) {
  if (!pendingConfirm.value) return
  const label = pendingConfirm.value.label
  pendingConfirm.value = null

  const answer = approved ? 'yes' : 'no'
  messages.value.splice(assistantIdx, 0, { role: 'log', content: `${approved ? '✅' : '❌'} ${label}: ${approved ? 'Approved' : 'Rejected'}`, isCron: true })
  assistantIdx++
  scrollBottom()

  try {
    const resp = await fetch('/api/chats/' + activeChatId.value + '/answer', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ answer }),
    })
    if (!resp.ok) throw new Error(await resp.text())
  } catch (e: unknown) {
    const errMsg = e instanceof Error ? e.message : String(e)
    messages.value.splice(assistantIdx, 0, { role: 'error', content: errMsg })
    assistantIdx++
  }
}

async function send() {
  if (!input.value.trim() && !attachedFiles.value.length) return
  const text = input.value.trim()
  input.value = ''

  // Inject message mid-execution if agent is running (and not in ask/confirm mode)
  if (sending.value && !pendingAsk.value && !pendingConfirm.value) {
    const pendingMsg: ChatMessage = { role: 'user', content: text, pending: true }
    messages.value.splice(assistantIdx, 0, pendingMsg)
    assistantIdx++
    renderMarkdown(text).then(html => { pendingMsg.contentHtml = html })
    scrollBottom()
    try {
      const resp = await fetch('/api/chats/' + activeChatId.value + '/inject', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: text }),
      })
      if (!resp.ok) throw new Error(await resp.text())
    } catch (e: unknown) {
      const errMsg = e instanceof Error ? e.message : String(e)
      messages.value.splice(assistantIdx, 0, { role: 'error', content: errMsg })
      assistantIdx++
    }
    return
  }

  if (pendingAsk.value) {
    const question = pendingAsk.value.question
    pendingAsk.value = null
    
    messages.value.splice(assistantIdx, 0, { role: 'log', content: `💬 ${text}`, isCron: true })
    assistantIdx++
    scrollBottom()

    try {
      const resp = await fetch('/api/chats/' + activeChatId.value + '/answer', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ answer: text }),
      })
      if (!resp.ok) throw new Error(await resp.text())
    } catch (e: unknown) {
      const errMsg = e instanceof Error ? e.message : String(e)
      messages.value.splice(assistantIdx, 0, { role: 'error', content: errMsg })
      assistantIdx++
    }
    return
  }

  const files = [...attachedFiles.value]
  attachedFiles.value = []

  // Handle commands
  if (text === '/clear') {
    await fetch('/api/chats/' + activeChatId.value + '/clear')
    messages.value = [{ role: 'system', content: 'Chat cleared.' }]
    return
  }
  if (text === '/help') {
    messages.value.push({
      role: 'system',
      content: 'Commands: /clear — clear chat & history, /help — show this',
    })
    return
  }

  // Build the full message with attached files
  let fullMessage = text
  if (files.length) {
    fullMessage = `[Attached files: ${files.join(', ')}]\n${text}`
  }

  messages.value.push({ role: 'user', content: fullMessage })
  scrollBottom()

  sending.value = true
  assistantIdx = messages.value.length
  fullText = ''
  messages.value.push({ role: 'assistant', content: '' })

  try {
    const resp = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message: fullMessage, chat_id: activeChatId.value, provider: providerStore.selectedProvider }),
    })
    if (!resp.ok) throw new Error(await resp.text())
    const data = await resp.json()
    if (data.chat_id && data.chat_id !== activeChatId.value) {
      activeChatId.value = data.chat_id
      eventStore.updateChatId(onChatEvent, data.chat_id)
      emit('chat-created', data.chat_id)
    }
  } catch (e: unknown) {
    const errMsg = e instanceof Error ? e.message : String(e)
    if (assistantIdx < messages.value.length) {
      messages.value.splice(assistantIdx, 1)
    }
    messages.value.push({ role: 'error', content: errMsg })
    sending.value = false
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

function onDrop(e: DragEvent) {
  e.preventDefault()
  dragOver.value = false
  if (!e.dataTransfer) return
  const filePath = e.dataTransfer.getData('application/x-workspace-file')
  if (filePath && !attachedFiles.value.includes(filePath)) {
    attachedFiles.value.push(filePath)
  }
}

function onDragOver(e: DragEvent) {
  if (e.dataTransfer?.types.includes('application/x-workspace-file')) {
    e.preventDefault()
    dragOver.value = true
  }
}

function onDragLeave() {
  dragOver.value = false
}

function removeFile(idx: number) {
  attachedFiles.value.splice(idx, 1)
}

function handleMessageClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  const link = target.closest('.ws-file-link') as HTMLElement | null
  if (link) {
    e.preventDefault()
    const wsPath = link.dataset.wsPath
    if (wsPath) editorStore.openFile(wsPath, wsPath.split('/').pop() || wsPath)
  }
}

function handleContextAttach(e: Event) {
  const customEvent = e as CustomEvent<string>
  const filePath = customEvent.detail
  if (filePath && !attachedFiles.value.includes(filePath)) {
    attachedFiles.value.push(filePath)
    // Optional: could expand chat if it was hidden, but in current IdeLayout
    // it's a fixed pane or special tab. We assume it's open or will be opened.
  }
}


function handleFocusChatInput() {
  nextTick(() => {
    if (inputEl.value && inputEl.value.offsetParent !== null) {
      inputEl.value.focus()
    }
  })
}

async function checkRunningState() {
  if (!activeChatId.value) return
  try {
    const resp = await fetch('/api/chat-status/' + activeChatId.value)
    if (!resp.ok) return
    const data = await resp.json()
    if (data.running) {
      sending.value = true
      assistantIdx = messages.value.length
      fullText = ''
      messages.value.push({ role: 'assistant', content: '' })
      // Replay buffered events to reconstruct execution state
      if (Array.isArray(data.events)) {
        for (const evt of data.events) {
          switch (evt.type) {
            case 'ask':
              pendingAsk.value = { question: evt.content }
              messages.value.splice(assistantIdx, 0, { role: 'log', content: `❓ ${evt.content}`, isCron: true })
              assistantIdx++
              break
            case 'chunk':
              fullText += evt.content
              if (assistantIdx < messages.value.length) {
                messages.value[assistantIdx] = { role: 'assistant', content: fullText }
              }
              break
            case 'log': {
              const parsed = parseLogMessage(evt.content)
              const logEntry: any = {
                display: parsed.display,
                id: parsed.id,
                expanded: false,
              }
              const groupIdx = assistantIdx - 1
              let group = groupIdx >= 0 ? messages.value[groupIdx] : null
              if (!group || group.role !== 'log' || !group.logEntries) {
                group = { role: 'log', content: logEntry.display, logEntries: [logEntry], logsExpanded: false }
                messages.value.splice(assistantIdx, 0, group)
                assistantIdx++
              } else {
                group.logEntries!.push(logEntry)
                group.content = logEntry.display
              }
              break
            }
            case 'confirm':
              try {
                const payload = JSON.parse(evt.content)
                pendingConfirm.value = {
                  action: payload.action,
                  label: payload.label,
                  summary: payload.summary,
                  params: payload.params || {},
                }
              } catch { /* ignore */ }
              messages.value.splice(assistantIdx, 0, { role: 'log', content: `🔐 ${pendingConfirm.value?.label || 'Action Requested'}`, isCron: true })
              assistantIdx++
              break
          }
        }
      }
      scrollBottom()
    }
  } catch { /* ignore */ }
}

onMounted(async () => {
  window.addEventListener('focus-chat-input', handleFocusChatInput)
  window.addEventListener('attach-file-to-chat', handleContextAttach)
  document.addEventListener('click', onClickOutsideProvider)
  providerStore.fetchProviders()
  providerStore.listenForUpdates()
  // Only restore messages if this chat tab is currently active (v-show mounts all panels)
  const isActive = editorStore.activeFilePath === `special://chat-${activeChatId.value}` ||
                   editorStore.activeFilePath === `special://chat-${props.chatId}`
  if (activeChatId.value && isActive) {
    await restoreMessages()
    await checkRunningState()
  } else {
    scrollBottom()
  }
  const chatTypes = ['meta', 'chunk', 'log', 'done', 'error', 'cron', 'chat_agent', 'ask', 'confirm', 'pending_msg']
  for (const t of chatTypes) {
    eventStore.on(t, onChatEvent, activeChatId.value)
  }
})

onUnmounted(() => {
  window.removeEventListener('focus-chat-input', handleFocusChatInput)
  window.removeEventListener('attach-file-to-chat', handleContextAttach)
  document.removeEventListener('click', onClickOutsideProvider)
  const chatTypes = ['meta', 'chunk', 'log', 'done', 'error', 'cron', 'chat_agent', 'ask', 'confirm', 'pending_msg']
  for (const t of chatTypes) {
    eventStore.off(t, onChatEvent)
  }
})
</script>

<template>
  <div class="chat-page">
    <div class="messages" ref="messagesEl" @click="handleMessageClick">
      <button v-if="hasMore" class="load-older-btn" @click="loadMoreMessages">Load Older Messages</button>
      <template v-for="(msg, i) in messages" :key="i">
      <div v-if="msg" :class="['msg', msg.role, { pending: msg.pending }]">
        <template v-if="msg.role === 'log'">
          <!-- Standalone log (cron or single entry without group) -->
          <template v-if="msg.isCron || !msg.logEntries">
            <div class="log-line">
              <span class="log-text">{{ msg.content }}</span>
            </div>
          </template>
          <!-- Grouped execution logs (collapsible) -->
          <template v-else>
            <div class="log-group-summary log-line clickable" @click="msg.logsExpanded = !msg.logsExpanded">
              <span class="log-text">{{ msg.logsExpanded ? '⚡ Execution logs' : msg.content }}</span>
              <span v-if="msg.logEntries!.length > 1" class="log-count">({{ msg.logEntries!.length }})</span>
              <span class="expand-hint">{{ msg.logsExpanded ? 'collapse ▾' : 'expand ▸' }}</span>
            </div>
            <div v-if="msg.logsExpanded" class="log-group-entries">
              <div v-for="(entry, j) in msg.logEntries" :key="j" class="log-group-entry">
                <div
                  :class="['log-line', { clickable: entry.id || entry.isDoc }]"
                  @click.stop="entry.isDoc ? (entry.docExpanded = !entry.docExpanded) : entry.id ? toggleFile(entry) : null"
                >
                  <span v-if="entry.id" class="code-indicator">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>
                  </span>
                  <span class="log-text">{{ entry.display }}</span>
                  <span v-if="entry.isDoc" class="expand-hint">{{ entry.docExpanded ? 'hide ▾' : 'expand ▸' }}</span>
                  <span v-else-if="entry.id" class="expand-hint">{{ entry.expanded ? 'hide code ▾' : 'view code ▸' }}</span>
                </div>
                <pre v-if="entry.isDoc && entry.docExpanded" class="file-preview" style="white-space: pre-wrap; max-height: 300px; overflow-y: auto;">{{ entry.fullDoc }}</pre>
                <div v-if="entry.expanded && (entry.entry || entry.fileContent)" class="file-expand">
                  <div v-if="entry.entry" class="history-entry">
                    <div class="history-entry-header">
                      Step {{ (entry.entry.iteration ?? 0) + 1 }}
                      <span v-if="entry.entry.provider || entry.entry.agent_type" class="history-meta">
                        {{ [entry.entry.provider, entry.entry.agent_type].filter(Boolean).join(' · ') }}
                      </span>
                    </div>
                    <pre class="file-preview">{{ entry.entry.code }}</pre>
                    <div v-if="entry.entry.result" class="history-result">
                      <span class="result-label">Result:</span> {{ entry.entry.result }}
                    </div>
                    <details v-if="entry.entry.response" class="history-response">
                      <summary>AI Response</summary>
                      <div class="history-response-content md-content" v-html="entry.entry.responseHtml || marked.parse(entry.entry.response)"></div>
                    </details>
                  </div>
                  <template v-else>
                    <div class="file-name">#{{ entry.id }}</div>
                    <pre class="file-preview">{{ entry.fileContent }}</pre>
                  </template>
                </div>
              </div>
            </div>
          </template>
        </template>
        <template v-else-if="msg.role === 'error'">
          <div class="error-content">
            <svg class="error-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
            <span>{{ msg.content }}</span>
          </div>
        </template>
        <template v-else>
          <template v-if="msg.role === 'assistant' && msg.content">
            <div v-if="msg.content.length <= 4000 || msg.contentExpanded" class="md-content" v-html="msg.contentHtml || marked.parse(msg.content)"></div>
            <div v-else class="truncated-content">
              <div class="md-content" v-html="msg.contentHtml ? msg.contentHtml : marked.parse(msg.content.slice(0, 4000) + '...')"></div>
              <button class="truncate-toggle" @click="msg.contentExpanded = true">Show more ({{ Math.round(msg.content.length / 1024) }}KB)</button>
            </div>
          </template>
          <template v-else-if="msg.role === 'user' && msg.content">
            <div class="md-content" v-html="msg.contentHtml || marked.parse(msg.content)"></div>
            <span v-if="msg.pending" class="pending-badge">⏳ Pending</span>
          </template>
          <template v-else>{{ msg.content }}</template>
          <span v-if="msg.role === 'assistant' && sending && i === messages.length - 1" class="cursor" />
          <div v-if="msg.role === 'assistant' && msg.content && !sending" class="msg-actions">
            <button
              v-if="msg.historyEntries?.length || msg.hasHistory"
              class="steps-btn"
              @click="msg.historyExpanded ? (msg.historyExpanded = false) : toggleHistory(msg)"
              :title="msg.historyLoading ? 'Loading...' : msg.historyExpanded ? 'Hide steps' : 'View execution steps'"
            >
              <svg v-if="!msg.historyLoading" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>
              <span v-else class="loading-dots">···</span>
            </button>
            <button
              class="copy-btn"
              @click="copyMessage(msg.content, i)"
              :title="copiedMsgIdx === i ? 'Copied!' : 'Copy response'"
            >
              <svg v-if="copiedMsgIdx !== i" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></svg>
              <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="20 6 9 17 4 12"/></svg>
            </button>
          </div>
          <div v-if="msg.historyExpanded && msg.historyEntries?.length" class="history-entries">
            <div class="history-entries-header">
              <span>Execution Steps</span>
              <span v-if="(msg.promptTokens ?? 0) > 0 || (msg.completionTokens ?? 0) > 0" class="token-badge" title="Prompt / Completion tokens">
                <span class="tb-in">↑{{ msg.promptTokens && msg.promptTokens >= 1000 ? (msg.promptTokens/1000).toFixed(1)+'k' : (msg.promptTokens ?? 0) }}</span>
                <span class="tb-out">↓{{ msg.completionTokens && msg.completionTokens >= 1000 ? (msg.completionTokens/1000).toFixed(1)+'k' : (msg.completionTokens ?? 0) }}</span>
              </span>
              <div class="history-actions">
                <button class="copy-steps-btn" @click.stop="copySteps(msg, i)" :title="copiedSteps === i ? 'Copied!' : 'Copy all steps'">
                  <svg v-if="copiedSteps !== i" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></svg>
                  <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="20 6 9 17 4 12"/></svg>
                  {{ copiedSteps === i ? 'Copied!' : 'Copy all' }}
                </button>
                <button class="close-steps-btn" @click.stop="msg.historyExpanded = false" title="Close">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
                </button>
              </div>
            </div>
            <div v-for="entry in msg.historyEntries" :key="entry.id" class="history-entry">
              <div class="history-entry-header">
                Step {{ (entry.iteration ?? 0) + 1 }}
                <span v-if="entry.provider || entry.agent_type" class="history-meta">
                  {{ [entry.provider, entry.agent_type].filter(Boolean).join(' · ') }}
                </span>
              </div>
              <pre class="file-preview">{{ entry.code }}</pre>
              <div v-if="entry.result" class="history-result">
                <span class="result-label">Result:</span> {{ entry.result }}
              </div>
              <details v-if="entry.response" class="history-response">
                <summary>AI Response</summary>
                <div class="history-response-content md-content" v-html="entry.responseHtml || marked.parse(entry.response)"></div>
              </details>
            </div>
          </div>
        </template>
      </div>
      </template>
    </div>
    <div
      class="input-area"
      :class="{ 'drag-over': dragOver }"
      @drop="onDrop"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
    >
      <div v-if="attachedFiles.length" class="attached-files">
        <span
          v-for="(f, i) in attachedFiles"
          :key="f"
          class="file-chip"
        >
          📄 {{ f.split('/').pop() }}
          <button class="chip-remove" @click="removeFile(i)" title="Remove">×</button>
        </span>
      </div>
      <div v-if="providerStore.noProviders" class="no-provider-banner">
        ⚠️ No AI providers configured.
        <button class="cta-link" @click="editorStore.openSpecialTab('provider', 'New Provider', 0)">New Provider</button>
        to add one.
      </div>
      <div v-if="pendingConfirm" class="confirm-card">
        <div class="confirm-header">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0110 0v4"/></svg>
          <span class="confirm-title">{{ pendingConfirm.label }}</span>
        </div>
        <div class="confirm-summary">{{ pendingConfirm.summary }}</div>
        <div v-if="Object.keys(pendingConfirm.params).length" class="confirm-params">
          <div v-for="(val, key) in pendingConfirm.params" :key="key" class="confirm-param">
            <span class="param-key">{{ key }}:</span>
            <span class="param-val">{{ ['key', 'secret', 'password', 'token'].some(s => String(key).toLowerCase().includes(s)) && String(val).length > 8 ? String(val).slice(0,4) + '...' + String(val).slice(-4) : val }}</span>
          </div>
        </div>
        <div class="confirm-actions">
          <button class="btn-confirm-approve" @click="answerConfirm(true)">✓ Approve</button>
          <button class="btn-confirm-reject" @click="answerConfirm(false)">✕ Reject</button>
        </div>
      </div>
      <div v-if="pendingAsk" class="ask-banner" style="font-size: 13px; color: #F59E0B; padding: 4px 12px; font-weight: 500; background: rgba(245, 158, 11, 0.1); border-radius: 4px; margin-bottom: 8px; border: 1px solid rgba(245, 158, 11, 0.2);">
        🤖 Provide input for script: {{ pendingAsk.question }}
      </div>
      <div class="input-row">
        <textarea
          ref="inputEl"
          v-model="input"
          @keydown="onKeydown"
          :placeholder="providerStore.noProviders ? 'Configure a provider to start chatting...' : pendingAsk ? 'Type your answer and press Enter...' : attachedFiles.length ? 'Ask about files...' : 'Type a message... (/help)'"
          :disabled="providerStore.noProviders"
          rows="1"
        />
        <div v-if="providerStore.providers.length > 1 && !pendingAsk" class="provider-dropdown">
          <button class="provider-trigger" :disabled="sending" @click.stop="providerOpen = !providerOpen">
            {{ providerStore.selectedProviderLabel }}
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="6 9 12 15 18 9"/></svg>
          </button>
          <div v-if="providerOpen" class="provider-menu">
            <button
              v-for="p in providerStore.providers"
              :key="p.id"
              class="provider-option"
              :class="{ active: p.name === providerStore.selectedProvider }"
              @click.stop="providerStore.selectProvider(p.name); providerOpen = false"
            >
              <span class="provider-name">{{ p.name }}</span>
              <span class="provider-detail">{{ p.provider }}/{{ p.model }}</span>
            </button>
          </div>
        </div>
        <button v-if="sending && !pendingAsk && !pendingConfirm" class="btn btn-stop" @click="stopExecution" title="Stop execution">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="4" y="4" width="16" height="16" rx="2" /></svg>
        </button>
        <button class="btn btn-primary send-btn" :disabled="providerStore.noProviders" @click="send">▶</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.chat-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  width: 100%;
}
.messages {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.msg {
  max-width: 85%;
  min-width: 0;
  padding: 12px 16px;
  border-radius: 12px;
  font-size: 14px;
  line-height: 1.6;
  overflow-wrap: anywhere;
  word-break: break-word;
}
.msg.user {
  align-self: flex-end;
  background: var(--accent);
  color: #fff;
  border-bottom-right-radius: 4px;
}
.msg.user.pending {
  opacity: 0.7;
  border-style: dashed;
}
.pending-badge {
  display: inline-block;
  font-size: 11px;
  color: rgba(255,255,255,0.6);
  margin-top: 4px;
}
.msg.assistant {
  align-self: flex-start;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-bottom-left-radius: 4px;
}
.msg.system {
  align-self: center;
  color: var(--text-muted);
  font-size: 13px;
  padding: 4px;
}
.msg.log {
  align-self: flex-start;
  max-width: 100%;
  background: rgba(245, 158, 11, 0.08);
  border: 1px solid rgba(245, 158, 11, 0.15);
  color: #F59E0B;
  font-size: 13px;
  font-family: monospace;
  padding: 8px 12px;
}
.msg.error {
  align-self: stretch;
  background: rgba(227, 122, 113, 0.08);
  border: 1px solid rgba(227, 122, 113, 0.25);
  color: #E37A71;
  font-size: 13px;
  padding: 10px 14px;
  max-width: 100%;
}
.error-content {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.error-icon {
  flex-shrink: 0;
  margin-top: 2px;
  opacity: 0.8;
}
.log-line {
  display: flex;
  align-items: center;
  gap: 8px;
}
.log-line.clickable {
  cursor: pointer;
}
.log-line.clickable:hover .log-text {
  text-decoration: underline;
}
.expand-hint {
  font-size: 11px;
  opacity: 0.5;
  margin-left: auto;
  white-space: nowrap;
}
.log-line.clickable:hover .expand-hint {
  opacity: 0.8;
}
.code-indicator {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  opacity: 0.7;
}
.file-expand {
  margin-top: 8px;
}
.log-count {
  font-size: 11px;
  opacity: 0.5;
  white-space: nowrap;
}
.log-group-entries {
  margin-top: 6px;
  padding-left: 10px;
  border-left: 2px solid rgba(245, 158, 11, 0.2);
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.log-group-entry {
  font-size: 12px;
}
.truncated-content {
  position: relative;
}
.truncate-toggle {
  background: none;
  border: none;
  color: var(--accent-light);
  cursor: pointer;
  font-size: 12px;
  padding: 4px 0;
  opacity: 0.8;
  transition: opacity 0.15s;
}
.truncate-toggle:hover {
  opacity: 1;
  text-decoration: underline;
}
.file-name {
  font-size: 11px;
  color: var(--text-muted);
  margin-bottom: 4px;
  font-family: monospace;
}
.file-preview {
  margin-top: 8px;
  padding: 12px;
  background: var(--bg-tertiary);
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.5;
  max-height: 300px;
  overflow: auto;
  color: var(--text-primary);
  white-space: pre;
}
.input-area {
  padding: 16px 24px;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: border-color 0.2s, background 0.2s;
}
.input-area.drag-over {
  border-top-color: var(--accent);
  background: rgba(245, 158, 11, 0.05);
}
.no-provider-banner {
  padding: 8px 12px;
  background: rgba(241, 216, 130, 0.1);
  border: 1px solid rgba(241, 216, 130, 0.3);
  border-radius: 8px;
  font-size: 13px;
  color: #F1D882;
  display: flex;
  align-items: center;
  gap: 6px;
}
.cta-link {
  background: none;
  border: none;
  color: var(--accent-light);
  cursor: pointer;
  font-size: 13px;
  text-decoration: underline;
  padding: 0;
}
.input-row {
  display: flex;
  gap: 12px;
}
.provider-dropdown {
  position: relative;
  flex-shrink: 0;
}
.provider-trigger {
  display: flex;
  align-items: center;
  gap: 4px;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 6px 10px;
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
  height: 44px;
  box-sizing: border-box;
  transition: border-color 0.15s, background 0.15s;
}
.provider-trigger:hover { border-color: var(--accent); }
.provider-trigger:disabled { opacity: 0.5; cursor: default; }
.provider-menu {
  position: absolute;
  bottom: calc(100% + 6px);
  right: 0;
  min-width: 180px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 4px;
  box-shadow: 0 -4px 16px rgba(0,0,0,0.3);
  z-index: 100;
}
.provider-option {
  display: flex;
  flex-direction: column;
  width: 100%;
  padding: 8px 10px;
  background: none;
  border: none;
  color: var(--text);
  cursor: pointer;
  border-radius: 6px;
  text-align: left;
  transition: background 0.1s;
}
.provider-option:hover { background: rgba(255,255,255,0.06); }
.provider-option.active { background: rgba(245, 158, 11, 0.15); }
.provider-name { font-size: 13px; font-weight: 500; }
.provider-detail { font-size: 11px; color: var(--text-muted); margin-top: 1px; }
.attached-files {
  width: 100%;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.file-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 10px;
  background: rgba(245, 158, 11, 0.15);
  border: 1px solid rgba(245, 158, 11, 0.3);
  border-radius: 12px;
  font-size: 12px;
  color: #F59E0B;
  white-space: nowrap;
}
.chip-remove {
  background: none;
  border: none;
  color: #F59E0B;
  cursor: pointer;
  font-size: 14px;
  line-height: 1;
  padding: 0 2px;
  opacity: 0.6;
  transition: opacity 0.15s;
}
.chip-remove:hover {
  opacity: 1;
}
.input-area textarea {
  resize: none;
  min-height: 44px;
  max-height: 120px;
}
.send-btn, .btn-stop {
  flex-shrink: 0;
  height: 44px;
  width: 44px;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 18px;
}
.btn-stop {
  background: var(--error);
  color: #fff;
  border: none;
  border-radius: 8px;
  cursor: pointer;
  transition: opacity 0.15s;
}
.btn-stop:hover {
  opacity: 0.8;
}
.load-older-btn {
  display: block;
  width: 100%;
  padding: 8px 12px;
  background: transparent;
  border: none;
  border-bottom: 1px solid var(--border);
  color: var(--accent);
  font-size: 12px;
  cursor: pointer;
  transition: background 0.15s;
}
.load-older-btn:hover {
  background: var(--bg-secondary);
}
.cursor {
  display: inline-block;
  width: 2px;
  height: 14px;
  background: var(--accent-light);
  animation: blink 1s infinite;
  vertical-align: middle;
  margin-left: 2px;
}
@keyframes blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0; }
}
.loading-dots { font-size: 14px; line-height: 14px; }
.history-entries {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.history-entry-header {
  font-size: 11px;
  color: var(--text-muted);
  font-family: monospace;
  margin-bottom: 4px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.history-meta {
  opacity: 0.6;
  font-style: italic;
}
.history-result {
  font-size: 12px;
  color: var(--text-muted);
  padding: 6px 10px;
  background: rgba(255,255,255,0.03);
  border-radius: 4px;
  margin-top: 4px;
  white-space: pre-wrap;
  word-break: break-word;
}
.history-response {
  margin-top: 6px;
  border: 1px solid var(--border);
  border-radius: 6px;
  overflow: hidden;
}
.history-response summary {
  font-size: 11px;
  color: var(--text-muted);
  cursor: pointer;
  padding: 5px 10px;
  background: rgba(255,255,255,0.03);
  user-select: none;
  transition: color 0.15s, background 0.15s;
}
.history-response summary:hover {
  color: var(--text);
  background: rgba(255,255,255,0.06);
}
.history-response[open] summary {
  border-bottom: 1px solid var(--border);
}
.history-response-content {
  padding: 10px;
  font-size: 13px;
  max-height: 400px;
  overflow: auto;
}
.result-label {
  color: #5BC0EB;
  font-weight: 600;
}
.md-content :deep(p) { margin: 0 0 8px; }
.md-content :deep(p:last-child) { margin-bottom: 0; }
.md-content :deep(img) { max-width: 100%; border-radius: 8px; margin: 8px 0; }
.md-content :deep(code) { background: rgba(255,255,255,0.06); padding: 2px 5px; border-radius: 4px; font-size: 12px; }
.md-content :deep(pre) { background: rgba(0,0,0,0.3); padding: 10px 14px; border-radius: 8px; overflow-x: auto; margin: 8px 0; }
.md-content :deep(pre code) { background: none; padding: 0; }
.md-content :deep(ul), .md-content :deep(ol) { margin: 4px 0; padding-left: 20px; }
.md-content :deep(a) { color: var(--accent-light); text-decoration: underline; }
.md-content :deep(.ws-file-link) {
  color: #F59E0B;
  text-decoration: none;
  cursor: pointer;
  padding: 1px 6px;
  border-radius: 4px;
  background: rgba(245, 158, 11, 0.1);
  transition: background 0.15s;
}
.md-content :deep(.ws-file-link:hover) {
  background: rgba(245, 158, 11, 0.2);
}
.md-content :deep(h1), .md-content :deep(h2), .md-content :deep(h3) { margin: 8px 0 4px; font-size: 14px; }
.md-content :deep(blockquote) { border-left: 3px solid var(--border); padding-left: 12px; margin: 8px 0; opacity: 0.8; }
.msg.assistant { position: relative; }
.msg-actions {
  position: absolute;
  bottom: 8px;
  right: 8px;
  display: flex;
  gap: 4px;
  opacity: 0;
  transition: opacity 0.15s;
}
.msg.assistant:hover .msg-actions { opacity: 0.7; }
.copy-btn, .steps-btn {
  background: rgba(255,255,255,0.04);
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--text-muted);
  cursor: pointer;
  padding: 4px;
  display: flex;
  align-items: center;
  transition: color 0.15s, background 0.15s;
}
.copy-btn:hover { color: var(--text); background: rgba(255,255,255,0.08); }
.steps-btn:hover { color: #F59E0B; background: rgba(245, 158, 11, 0.1); }
.token-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 10px;
  color: var(--text-muted);
  background: rgba(255,255,255,0.04);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 2px 6px;
  white-space: nowrap;
  font-family: monospace;
}
.tb-in { color: rgba(91, 192, 235, 0.8); }
.tb-out { color: rgba(245, 158, 11, 0.8); }
.history-entries-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 11px;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 4px;
}
.copy-steps-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  background: none;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--text-muted);
  cursor: pointer;
  padding: 2px 8px;
  font-size: 11px;
  transition: color 0.15s;
}
.copy-steps-btn:hover { color: var(--text); }
.close-steps-btn {
  display: flex;
  align-items: center;
  background: none;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--text-muted);
  cursor: pointer;
  padding: 2px 4px;
  transition: color 0.15s;
}
.close-steps-btn:hover { color: var(--text); }
.history-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

/* Confirm card */
.confirm-card {
  background: rgba(91, 192, 235, 0.08);
  border: 1px solid rgba(91, 192, 235, 0.25);
  border-radius: 8px;
  padding: 12px;
  margin-bottom: 8px;
}
.confirm-header {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #5BC0EB;
  font-weight: 600;
  font-size: 14px;
  margin-bottom: 6px;
}
.confirm-title { flex: 1; }
.confirm-summary {
  font-size: 12px;
  color: var(--text-secondary, #888);
  margin-bottom: 8px;
}
.confirm-params {
  background: rgba(0,0,0,0.15);
  border-radius: 4px;
  padding: 6px 8px;
  margin-bottom: 10px;
  font-size: 12px;
  font-family: monospace;
}
.confirm-param {
  display: flex;
  gap: 6px;
  padding: 2px 0;
}
.param-key {
  color: var(--text-secondary, #888);
  min-width: 80px;
}
.param-val {
  color: var(--text, #ccc);
  word-break: break-all;
}
.confirm-actions {
  display: flex;
  gap: 8px;
}
.btn-confirm-approve,
.btn-confirm-reject {
  flex: 1;
  padding: 6px 12px;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s, transform 0.1s;
}
.btn-confirm-approve {
  background: rgba(91, 192, 235, 0.15);
  color: #5BC0EB;
  border: 1px solid rgba(91, 192, 235, 0.3);
}
.btn-confirm-approve:hover {
  background: rgba(91, 192, 235, 0.25);
  transform: translateY(-1px);
}
.btn-confirm-reject {
  background: rgba(227, 122, 113, 0.1);
  color: #E37A71;
  border: 1px solid rgba(227, 122, 113, 0.2);
}
.btn-confirm-reject:hover {
  background: rgba(227, 122, 113, 0.2);
  transform: translateY(-1px);
}
</style>
