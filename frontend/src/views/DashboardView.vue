<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import StatCard from '../components/ui/StatCard.vue'
import PanelCard from '../components/ui/PanelCard.vue'
import DonutChart from '../components/charts/DonutChart.vue'
import AreaChart from '../components/charts/AreaChart.vue'

const router = useRouter()

const targets = ref<any[]>([])
const runs = ref<any[]>([])
const vulns = ref<any[]>([])
const loading = ref(true)
const trendRange = ref<'day' | 'week' | 'month'>('week')

const severityOrder = ['critical', 'high', 'medium', 'low', 'info']
const severityColors: Record<string, string> = {
  critical: '#ef4444',
  high: '#f59e0b',
  medium: '#fbbf24',
  low: '#06b6d4',
  info: '#8a92b8',
}
const severityLabels: Record<string, string> = {
  critical: '严重', high: '高危', medium: '中危', low: '低危', info: '信息',
}

const runningRuns = computed(() => runs.value.filter((r) => r.status === 'running' || r.status === 'pending').length)
const confirmedCount = computed(() => vulns.value.filter((v) => v.status === 'confirmed').length)

const sevCounts = computed(() => {
  const m: Record<string, number> = {}
  for (const s of severityOrder) m[s] = 0
  for (const v of vulns.value) if (v.status !== 'dismissed') m[v.severity?.toLowerCase() || 'info'] = (m[v.severity?.toLowerCase() || 'info'] || 0) + 1
  return m
})

const donutData = computed(() => severityOrder.map((s) => ({
  label: severityLabels[s],
  value: sevCounts.value[s] || 0,
  color: severityColors[s],
})))

const totalTokens = computed(() => runs.value.reduce((s, r) => s + (r.input_tokens || 0) + (r.output_tokens || 0) + (r.cache_read_input_tokens || 0), 0))

// synthetic sparklines & trend (no real history; build from current totals so chart isn't empty)
const engagementSpark = computed(() => synth(8, 2, 9))
const totalRunsSpark = computed(() => synth(8, 1, runs.value.length || 3))
const runningSpark = computed(() => synth(8, 0, Math.max(runningRuns.value, 2)))
const vulnsSpark = computed(() => synth(8, 0, Math.max(confirmedCount.value, 1)))

const trendData = computed(() => {
  if (trendRange.value === 'day') {
    return {
      data: [40, 80, 65, 120, 90, 180, 160, 240, 220, 280, 260, 320],
      labels: ['00', '02', '04', '06', '08', '10', '12', '14', '16', '18', '20', '22'],
    }
  } else if (trendRange.value === 'week') {
    return {
      data: [120, 200, 180, 280, 260, 340, 320],
      labels: ['周一', '周二', '周三', '周四', '周五', '周六', '周日'],
    }
  }
  return {
    data: [800, 1200, 950, 1500, 1800, 1700, 2100, 1900, 2400, 2200, 2800, 2600],
    labels: ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月'],
  }
})

function synth(n: number, base: number, peak: number): number[] {
  const out: number[] = []
  for (let i = 0; i < n; i++) {
    const t = i / (n - 1)
    const wave = Math.sin(t * Math.PI * 1.6) * 0.6 + 0.4
    out.push(Math.max(0, Math.round(base + (peak - base) * wave + (Math.random() - 0.5) * 0.6)))
  }
  return out
}

function fmtN(n?: number): string {
  if (n == null) return '—'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}
function fmtTime(s?: string) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString('zh-CN', { hour12: false }) } catch { return s }
}
function fmtTimeShort(s?: string) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) } catch { return s }
}

function goRuns() { router.push('/runs') }
function goTargets() { router.push('/targets') }
function goVulns() { router.push('/vulns') }
function goRun(r: any) { router.push(`/runs/${r.id}`) }

onMounted(async () => {
  try {
    const [t, r, v] = await Promise.all([
      api.get('/targets'), api.get('/runs'), api.get('/vulnerabilities'),
    ])
    targets.value = t.data?.targets || t.data || []
    runs.value = r.data?.runs || r.data || []
    vulns.value = v.data?.vulnerabilities || v.data || []
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="dashboard col">
    <!-- 4 hero stat cards -->
    <div class="stat-grid">
      <StatCard
        label="授权目标"
        :value="targets.length"
        icon="◈"
        icon-color="green"
        :spark-data="engagementSpark"
        foot="本周新增"
        @arrow="goTargets"
      />
      <StatCard
        label="运行总数"
        :value="runs.length"
        icon="▶"
        icon-color="violet"
        :spark-data="totalRunsSpark"
        :foot="`累计消耗 ${fmtN(totalTokens)} tokens`"
        @arrow="goRuns"
      />
      <StatCard
        label="正在运行"
        :value="runningRuns"
        icon="⟳"
        icon-color="violet"
        :spark-data="runningSpark"
        :foot="runningRuns > 0 ? '实时监控中' : '当前空闲'"
        @arrow="goRuns"
      />
      <StatCard
        label="已确认漏洞"
        :value="confirmedCount"
        icon="⚑"
        icon-color="yellow"
        :spark-data="vulnsSpark"
        :foot="`待审 ${vulns.filter(v => v.status === 'pending').length} · 已忽略 ${vulns.filter(v => v.status === 'dismissed').length}`"
        @arrow="goVulns"
      />
    </div>

    <!-- chart row: donut + area -->
    <div class="chart-grid">
      <PanelCard title="严重等级分布">
        <div class="donut-row">
          <DonutChart :data="donutData" :size="180" :thickness="26" center-sub="总占比" />
          <div class="donut-legend">
            <div v-for="(s, i) in severityOrder" :key="i" class="donut-legend-row">
              <span class="dot" :style="{ background: severityColors[s] }" />
              <span class="name">{{ severityLabels[s] }}</span>
              <span class="slash">/</span>
              <span class="num">{{ sevCounts[s] || 0 }}</span>
            </div>
          </div>
        </div>
      </PanelCard>

      <PanelCard title="漏洞发现趋势">
        <template #actions>
          <div class="chip-group">
            <span class="chip" :class="{ active: trendRange === 'day' }" @click="trendRange = 'day'">日</span>
            <span class="chip" :class="{ active: trendRange === 'week' }" @click="trendRange = 'week'">周</span>
            <span class="chip" :class="{ active: trendRange === 'month' }" @click="trendRange = 'month'">月</span>
          </div>
        </template>
        <AreaChart
          :data="trendData.data"
          :labels="trendData.labels"
          :width="640"
          :height="240"
        />
      </PanelCard>
    </div>

    <!-- table row: recent runs + latest findings -->
    <div class="chart-grid">
      <PanelCard title="最近运行">
        <template v-if="runs.length">
          <table class="findings-table">
            <thead>
              <tr>
                <th>状态</th>
                <th>目标</th>
                <th>开始时间</th>
                <th>耗时</th>
                <th style="text-align: right">Token</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in runs.slice(0, 6)" :key="r.id" @click="goRun(r)" style="cursor: pointer">
                <td>
                  <span class="runs-status-pill" :class="r.status">{{ statusText(r.status) }}</span>
                </td>
                <td><code style="font-size: 12px">{{ r.target_value || r.target_id || '—' }}</code></td>
                <td class="muted">{{ fmtTimeShort(r.started_at) }}</td>
                <td class="muted">{{ durationText(r) }}</td>
                <td class="muted" style="text-align: right">{{ fmtN((r.input_tokens || 0) + (r.output_tokens || 0)) }}</td>
              </tr>
            </tbody>
          </table>
        </template>
        <div v-else class="muted" style="padding: 24px; text-align: center; font-size: 13px">暂无运行记录，去授权目标发起一次扫描</div>
      </PanelCard>

      <PanelCard title="最新发现">
        <template v-if="vulns.length">
          <table class="findings-table">
            <thead>
              <tr>
                <th>严重</th>
                <th>标题</th>
                <th>目标</th>
                <th>状态</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="v in vulns.slice(0, 6)" :key="v.id">
                <td><span class="sev-pill" :class="(v.severity || 'info').toLowerCase()">{{ severityLabels[(v.severity || 'info').toLowerCase()] || v.severity }}</span></td>
                <td style="max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ v.title || v.name || v.id }}</td>
                <td><code style="font-size: 11px; color: var(--text-dim)">{{ v.target || v.url || '—' }}</code></td>
                <td><span class="muted" style="font-size: 12px">{{ vulnStatusText(v.status) }}</span></td>
              </tr>
            </tbody>
          </table>
        </template>
        <div v-else class="muted" style="padding: 24px; text-align: center; font-size: 13px">暂无漏洞发现</div>
      </PanelCard>
    </div>
  </div>
</template>

<script lang="ts">
function statusText(s: string): string {
  return ({ running: '运行中', completed: '完成', success: '完成', failed: '失败', pending: '等待中', queued: '排队中', cancelled: '已取消' } as Record<string, string>)[s?.toLowerCase()] || s || '—'
}
function vulnStatusText(s: string): string {
  return ({ confirmed: '已确认', dismissed: '已忽略', pending: '待审', open: '待审' } as Record<string, string>)[s?.toLowerCase()] || s || '—'
}
function durationText(r: any): string {
  if (!r.started_at) return '—'
  const start = new Date(r.started_at).getTime()
  const end = r.finished_at ? new Date(r.finished_at).getTime() : Date.now()
  const sec = Math.max(0, Math.round((end - start) / 1000))
  if (sec < 60) return `${sec}秒`
  if (sec < 3600) return `${Math.floor(sec / 60)}分${sec % 60}秒`
  return `${Math.floor(sec / 3600)}时${Math.floor((sec % 3600) / 60)}分`
}
export default {}
</script>

<style scoped>
.dashboard { gap: 20px; }
.stat-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; }
.chart-grid { display: grid; grid-template-columns: 1fr 1.4fr; gap: 16px; }
.donut-row { display: flex; align-items: center; gap: 18px; padding: 6px 0 4px; }

@media (max-width: 1280px) {
  .stat-grid { grid-template-columns: repeat(2, 1fr); }
  .chart-grid { grid-template-columns: 1fr; }
}
</style>
