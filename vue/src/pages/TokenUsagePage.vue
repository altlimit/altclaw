<script setup lang="ts">
import { onMounted, ref, watch, nextTick } from 'vue'

interface UsageRow {
  id: string        // "YYYY-MM-DD"
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  request_count: number
  date: string
}

interface Limits {
  rate_limit: number
  daily_prompt_cap: number
  daily_completion_cap: number
}

interface ProviderOption {
  id: number
  name: string
}

const rows = ref<UsageRow[]>([])
const today = ref<UsageRow | null>(null)
const limits = ref<Limits>({ rate_limit: 0, daily_prompt_cap: 0, daily_completion_cap: 0 })
const loading = ref(true)
const canvasRef = ref<HTMLCanvasElement | null>(null)
const providers = ref<ProviderOption[]>([])
const selectedProvider = ref<number>(0)  // 0 = All

const effectiveRPM = 10
const effectivePromptCap = 1_000_000
const effectiveCompletionCap = 100_000

function displayCap(v: number, def: number) { return v > 0 ? v : def }

async function load() {
  loading.value = true
  try {
    const url = selectedProvider.value > 0
      ? `/api/token-usage?provider_id=${selectedProvider.value}`
      : '/api/token-usage'
    const resp = await fetch(url)
    if (!resp.ok) throw new Error(await resp.text())
    const data = await resp.json()
    rows.value = data.rows || []
    today.value = data.today || null
    limits.value = data.limits || { rate_limit: 0, daily_prompt_cap: 0, daily_completion_cap: 0 }
  } finally {
    loading.value = false
    setTimeout(() => { requestAnimationFrame(drawChart) }, 50)
  }
}

watch(selectedProvider, load)

function formatNum(n: number) {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k'
  return String(n)
}

async function drawChart() {
  const canvas = canvasRef.value
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  const dpr = window.devicePixelRatio || 1
  const w = canvas.offsetWidth
  const h = canvas.offsetHeight
  canvas.width = w * dpr
  canvas.height = h * dpr
  ctx.scale(dpr, dpr)

  // Build last 14 days slots
  const days = 14
  const slots: { label: string; prompt: number; completion: number; total: number }[] = []
  const rowMap = new Map(rows.value.map(r => [r.id, r]))
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date()
    d.setUTCDate(d.getUTCDate() - i)
    const key = d.toISOString().slice(0, 10)
    const row = rowMap.get(key)
    slots.push({
      label: d.toLocaleDateString('en', { month: 'short', day: 'numeric' }),
      prompt: row?.prompt_tokens || 0,
      completion: row?.completion_tokens || 0,
      total: row?.total_tokens || 0,
    })
  }

  const maxVal = Math.max(...slots.map(s => s.total), 1)
  const padL = 48, padR = 16, padT = 16, padB = 36
  const chartW = w - padL - padR
  const chartH = h - padT - padB
  const barW = chartW / slots.length
  const barGap = barW * 0.25

  ctx.clearRect(0, 0, w, h)

  // Grid lines
  ctx.strokeStyle = 'rgba(99,102,241,0.12)'
  ctx.lineWidth = 1
  const gridLines = 4
  for (let i = 0; i <= gridLines; i++) {
    const y = padT + (chartH / gridLines) * i
    ctx.beginPath(); ctx.moveTo(padL, y); ctx.lineTo(w - padR, y); ctx.stroke()
    const val = Math.round(maxVal * (1 - i / gridLines))
    ctx.fillStyle = 'rgba(148,163,184,0.7)'
    ctx.font = '10px Inter, sans-serif'
    ctx.textAlign = 'right'
    ctx.fillText(formatNum(val), padL - 4, y + 4)
  }

  // Bars
  slots.forEach((s, i) => {
    const x = padL + i * barW + barGap / 2
    const bw = barW - barGap

    // Prompt (bottom)
    if (s.prompt > 0) {
      const bh = (s.prompt / maxVal) * chartH
      const grad = ctx.createLinearGradient(0, padT + chartH - bh, 0, padT + chartH)
      grad.addColorStop(0, 'rgba(99,102,241,0.8)')
      grad.addColorStop(1, 'rgba(99,102,241,0.4)')
      ctx.fillStyle = grad
      ctx.beginPath()
      ctx.roundRect(x, padT + chartH - bh, bw, bh, [3, 3, 0, 0])
      ctx.fill()
    }

    // Completion stacked on top
    if (s.completion > 0) {
      const promptH = (s.prompt / maxVal) * chartH
      const bh = (s.completion / maxVal) * chartH
      const grad = ctx.createLinearGradient(0, padT + chartH - promptH - bh, 0, padT + chartH - promptH)
      grad.addColorStop(0, 'rgba(168,85,247,0.85)')
      grad.addColorStop(1, 'rgba(168,85,247,0.45)')
      ctx.fillStyle = grad
      ctx.beginPath()
      ctx.roundRect(x, padT + chartH - promptH - bh, bw, bh, [3, 3, 0, 0])
      ctx.fill()
    }

    // X label
    ctx.fillStyle = 'rgba(148,163,184,0.7)'
    ctx.font = '10px Inter, sans-serif'
    ctx.textAlign = 'center'
    ctx.fillText(s.label, x + bw / 2, h - 8)
  })
}

onMounted(async () => {
  // Load providers for the filter dropdown
  try {
    const resp = await fetch('/api/providers')
    if (resp.ok) {
      const data = await resp.json()
      providers.value = (data || []).map((p: any) => ({ id: p.id, name: p.name }))
    }
  } catch {}
  await load()
})
</script>

<template>
  <div class="container">
    <div class="page-header">
      <h2 class="page-title">Token Usage</h2>
      <select v-if="providers.length" v-model.number="selectedProvider" class="provider-filter">
        <option :value="0">All Providers</option>
        <option v-for="p in providers" :key="p.id" :value="p.id">{{ p.name }}</option>
      </select>
    </div>

    <div v-if="loading" class="loading">Loading…</div>

    <template v-else>
      <!-- Summary cards -->
      <div class="cards">
        <div class="card">
          <div class="card-label">Today's Tokens</div>
          <div class="card-value">{{ formatNum(today?.total_tokens || 0) }}</div>
          <div class="card-sub">{{ formatNum(today?.prompt_tokens || 0) }} prompt · {{ formatNum(today?.completion_tokens || 0) }} completion</div>
        </div>
        <div class="card">
          <div class="card-label">Today's Requests</div>
          <div class="card-value">{{ today?.request_count || 0 }}</div>
          <div class="card-sub">Rate limit: {{ displayCap(limits.rate_limit, effectiveRPM) }} req/min</div>
        </div>
        <div class="card">
          <div class="card-label">Daily Cap</div>
          <div class="card-value">{{ formatNum(displayCap(limits.daily_completion_cap, effectiveCompletionCap)) }}<span style="font-size:13px;font-weight:400;color:var(--text-muted)"> out</span></div>
          <div class="card-sub">
            <div style="margin-bottom:2px;font-size:11px">📥 Input {{ formatNum(today?.prompt_tokens || 0) }} / {{ formatNum(displayCap(limits.daily_prompt_cap, effectivePromptCap)) }}</div>
            <div class="cap-bar">
              <div
                class="cap-fill"
                :style="{ width: Math.min(100, ((today?.prompt_tokens || 0) / displayCap(limits.daily_prompt_cap, effectivePromptCap)) * 100) + '%' }"
                :class="{ warn: (today?.prompt_tokens || 0) / displayCap(limits.daily_prompt_cap, effectivePromptCap) > 0.8 }"
              ></div>
            </div>
            <div style="margin-bottom:2px;font-size:11px">📤 Output {{ formatNum(today?.completion_tokens || 0) }} / {{ formatNum(displayCap(limits.daily_completion_cap, effectiveCompletionCap)) }}</div>
            <div class="cap-bar">
              <div
                class="cap-fill completion"
                :style="{ width: Math.min(100, ((today?.completion_tokens || 0) / displayCap(limits.daily_completion_cap, effectiveCompletionCap)) * 100) + '%' }"
                :class="{ warn: (today?.completion_tokens || 0) / displayCap(limits.daily_completion_cap, effectiveCompletionCap) > 0.8 }"
              ></div>
            </div>
          </div>
        </div>
      </div>

      <!-- Chart -->
      <div class="section">
        <div class="section-title">14-Day Usage</div>
        <div class="legend">
          <span class="legend-dot prompt"></span> Prompt
          <span class="legend-dot completion"></span> Completion
        </div>
        <div class="chart-wrap">
          <canvas ref="canvasRef"></canvas>
        </div>
      </div>

      <!-- Table -->
      <div class="section">
        <div class="section-title">History (30 days)</div>
        <table class="usage-table">
          <thead>
            <tr><th>Date</th><th>Requests</th><th>Prompt</th><th>Completion</th><th>Total</th></tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="row.id">
              <td>{{ row.id }}</td>
              <td>{{ row.request_count }}</td>
              <td>{{ formatNum(row.prompt_tokens) }}</td>
              <td>{{ formatNum(row.completion_tokens) }}</td>
              <td><strong>{{ formatNum(row.total_tokens) }}</strong></td>
            </tr>
            <tr v-if="rows.length === 0">
              <td colspan="5" class="empty">No usage recorded yet.</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="refresh-row">
        <button class="btn btn-ghost btn-sm" @click="load">↻ Refresh</button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.page-title {
  font-size: 20px;
  font-weight: 700;
  margin-bottom: 0;
}
.provider-filter {
  font-size: 13px;
  padding: 5px 10px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--bg-secondary);
  color: var(--text);
  cursor: pointer;
}
.loading {
  color: var(--text-muted);
  padding: 24px 0;
}
.cards {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
  margin-bottom: 24px;
}
.card {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 16px;
}
.card-label {
  font-size: 12px;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 6px;
}
.card-value {
  font-size: 28px;
  font-weight: 700;
  color: var(--accent-light);
  margin-bottom: 4px;
}
.card-sub {
  font-size: 12px;
  color: var(--text-muted);
}
.cap-bar {
  height: 4px;
  background: var(--border);
  border-radius: 2px;
  overflow: hidden;
  margin-bottom: 4px;
}
.cap-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 2px;
  transition: width 0.4s ease;
}
.cap-fill.warn { background: #f59e0b; }
.cap-fill.completion { background: rgba(168,85,247,0.8); }

.chart-wrap {
  height: 180px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 8px;
  margin-top: 8px;
}
.chart-wrap canvas {
  width: 100%;
  height: 100%;
  display: block;
}
.legend {
  display: flex;
  gap: 16px;
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 4px;
  align-items: center;
}
.legend-dot {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 2px;
  margin-right: 4px;
}
.legend-dot.prompt { background: rgba(99,102,241,0.8); }
.legend-dot.completion { background: rgba(168,85,247,0.8); }
.usage-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  margin-top: 8px;
}
.usage-table th {
  text-align: left;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  color: var(--text-muted);
  font-weight: 500;
  font-size: 12px;
}
.usage-table td {
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  color: var(--text-primary);
}
.usage-table tr:last-child td { border-bottom: none; }
.usage-table tr:hover td { background: rgba(99,102,241,0.04); }
.empty {
  text-align: center;
  color: var(--text-muted);
  padding: 24px !important;
}
.refresh-row {
  margin-top: 16px;
}
@media (max-width: 768px) {
  .cards {
    grid-template-columns: 1fr;
  }
}
</style>
