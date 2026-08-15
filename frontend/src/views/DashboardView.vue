<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import StatCard from '../components/ui/StatCard.vue'
import PanelCard from '../components/ui/PanelCard.vue'
import DonutChart from '../components/charts/DonutChart.vue'
import AreaChart from '../components/charts/AreaChart.vue'
import Icon from '../components/icons/Icon.vue'
import UiSkeleton from '../components/ui/UiSkeleton.vue'
import EmptyState from '../components/ui/EmptyState.vue'

const router = useRouter()

const targets = ref<any[]>([])
const runs = ref<any[]>([])
const vulns = ref<any[]>([])
const loading = ref(true)
const trendRange = ref<'day' | 'week' | 'month'>('week')

const severityOrder = ['critical', 'high', 'medium', 'low', 'info']
const severityColors: Record<string, string> = {
  critical: '#e26472', high: '#e8a361', medium: '#d9c261', low: '#5fc8d4', info: '#8a96bc',
}
const severityLabels: Record<string, string> = {
  critical: '严重', high: '高危', medium: '中危', low: '低危', info: '信息',
}

const runningRuns = computed(() => runs.value.filter((r) => r.status === 'running' || r.status === 'pending').length)
const confirmedCount = computed(() => vulns.value.filter((v) => v.status === 'confirmed').length)
const pendingCount = computed(() => vulns.value.filter((v) => v.status === 'pending').length)
const totalTokens = computed(() => runs.value.reduce((s, r) => s + (r.input_tokens || 0) + (r.output_tokens || 0) + (r.cache_read_input_tokens || 0), 0))

const sevCounts = computed(() => {
  const m: Record<string, number> = {}
  for (const s of severityOrder) m[s] = 0
  for (const v of vulns.value) if (v.status !== 'dismissed') m[v.severity?.toLowerCase() || 'info'] = (m[v.severity?.toLowerCase() || 'info'] || 0) + 1
  return m
})

const donutData = computed(() => severityOrder.map((s) => ({
  label: severityLabels[s], value: sevCounts.value[s] || 0, color: severityColors[s],
})))

// synthesized sparklines (replace with real history endpoint when available)
const sparkFor = (peak: number, base = 0) => synth(14, base, peak)
const tokenSpark = computed(() => sparkFor(Math.max(totalTokens.value / 100, 8), 1))
const engagementSpark = computed(() => sparkFor(Math.max(targets.value.length, 3)))
const totalRunsSpark = computed(() => sparkFor(Math.max(runs.value.length, 2)))
const runningSpark = computed(() => sparkFor(Math.max(runningRuns.value, 1)))
const vulnsSpark = computed(() => sparkFor(Math.max(confirmedCount.value, 1)))

const trendData = computed(() => {
  if (trendRange.value === 'day') {
    return { data: [40, 80, 65, 120, 90, 180, 160, 240, 220, 280, 260, 320], labels: ['00', '02', '04', '06', '08', '10', '12', '14', '16', '18', '20', '22'] }
  } else if (trendRange.value === 'week') {
    return { data: [120, 200, 180, 280, 260, 340, 320], labels: ['周一', '周二', '周三', '周四', '周五', '周六', '周日'] }
  }
  return { data: [800, 1200, 950, 1500, 1800, 1700, 2100, 1900, 2400, 2200, 2800, 2600], labels: ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月'] }
})

function synth(n: number, base: number, peak: number): number[] {
  const out: number[] = []
  for (let i = 0; i < n; i++) {
    const t = i / (n - 1)
    const wave = Math.sin(t * Math.PI * 1.6) * 0.6 + 0.4
    out.push(Math.max(0, Math.round(base + (peak - base) * wave + (Math.random() - 0.5) * 0.5)))
  }
  return out
}

function fmtN(n?: number): string {
  if (n == null) return '—'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}
function fmtTimeShort(s?: string) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) } catch { return s }
}

function goRuns() { router.push('/runs') }
function goTargets() { router.push('/targets') }
function goVulns() { router.push('/vulns') }
function goRun(r: any) { router.push(`/runs/${r.id}`) }
function goNewTarget() { router.push('/targets') }
function goSearch() { router.push('/search') }

const isEmpty = computed(() => !loading.value && targets.value.length === 0 && runs.value.length === 0 && vulns.value.length === 0)

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
    <!-- === HERO ROW: 1 large token card + 3 secondary === -->
    <div class="hero-grid">
      <div class="enter enter-1">
        <div class="hero-tokens">
          <div class="hero-tokens-head">
            <div class="hero-tokens-icon">
              <Icon name="bolt" :size="18" />
            </div>
            <div>
              <div class="hero-tokens-label">累计 Token 消耗</div>
              <div class="hero-tokens-sub">所有运行累计 · {{ runs.length }} 次评估</div>
            </div>
          </div>
          <div class="hero-tokens-value">
            <span class="num">{{ loading ? '—' : fmtN(totalTokens) }}</span>
            <span class="unit">tokens</span>
          </div>
          <div class="hero-tokens-bars" v-if="!loading">
            <div class="bar"><span class="bar-label">输入</span><span class="bar-num">{{ fmtN(runs.reduce((s, r) => s + (r.input_tokens || 0), 0)) }}</span></div>
            <div class="bar"><span class="bar-label">输出</span><span class="bar-num">{{ fmtN(runs.reduce((s, r) => s + (r.output_tokens || 0), 0)) }}</span></div>
            <div class="bar"><span class="bar-label">缓存</span><span class="bar-num">{{ fmtN(runs.reduce((s, r) => s + (r.cache_read_input_tokens || 0), 0)) }}</span></div>
          </div>
          <div class="hero-tokens-spark" v-if="!loading">
            <AreaChart :data="tokenSpark" :width="380" :height="80" :y-steps="2" stroke-color="#a78bfa" fill-color="#7d92e8" />
          </div>
        </div>
      </div>

      <div class="enter enter-2">
        <StatCard
          label="授权目标"
          :value="targets.length"
          icon-name="target"
          :spark-data="engagementSpark"
          :foot="loading ? '加载中…' : '当前可发起 AI 渗透测试'"
          accent="#5fc8d4"
          @arrow="goTargets"
        />
      </div>
      <div class="enter enter-3">
        <StatCard
          label="正在运行"
          :value="runningRuns"
          icon-name="play"
          :spark-data="runningSpark"
          :foot="runningRuns > 0 ? '实时监控中' : '当前空闲'"
          accent="#a78bfa"
          @arrow="goRuns"
        />
      </div>
      <div class="enter enter-4">
        <StatCard
          label="已确认漏洞"
          :value="confirmedCount"
          icon-name="flag"
          accent="#e26472"
          :spark-data="vulnsSpark"
          :foot="`待审 ${pendingCount} · 已忽略 ${vulns.filter(v => v.status === 'dismissed').length}`"
          @arrow="goVulns"
        />
      </div>
    </div>

    <!-- === CHART ROW: donut + area === -->
    <div v-if="!isEmpty" class="chart-grid enter enter-5">
      <PanelCard title="严重等级分布">
        <template #actions>
          <span class="muted" style="font-size: 11px; font-family: var(--font-mono); font-variant-numeric: tabular-nums">
            {{ vulns.filter(v => v.status !== 'dismissed').length }} 条已纳入
          </span>
        </template>
        <div class="donut-row">
          <DonutChart :data="donutData" :size="170" :thickness="22" center-sub="总占比" />
          <div class="donut-legend">
            <div v-for="(s, i) in severityOrder" :key="i" class="donut-legend-row">
              <span class="dot" :style="{ background: severityColors[s], color: severityColors[s] }" />
              <span class="name">{{ severityLabels[s] }}</span>
              <span class="slash">·</span>
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
        <AreaChart :data="trendData.data" :labels="trendData.labels" :width="640" :height="240" />
      </PanelCard>
    </div>

    <!-- === TABLE ROW: recent runs + latest findings === -->
    <div v-if="!isEmpty" class="chart-grid enter enter-6">
      <PanelCard title="最近运行">
        <template v-if="loading">
          <div class="sk-list">
            <UiSkeleton v-for="i in 4" :key="i" block height="42px" radius="8px" />
          </div>
        </template>
        <template v-else-if="runs.length">
          <table class="findings-table">
            <thead>
              <tr><th>状态</th><th>目标</th><th>开始</th><th>耗时</th><th style="text-align: right">Token</th></tr>
            </thead>
            <tbody>
              <tr v-for="r in runs.slice(0, 6)" :key="r.id" @click="goRun(r)" style="cursor: pointer">
                <td>
                  <div class="runs-icon-cell">
                    <span class="runs-icon" :class="r.status === 'success' || r.status === 'completed' ? 'success' : r.status === 'failed' ? 'failed' : r.status === 'running' ? 'running' : ''">
                      <Icon :name="r.status === 'success' || r.status === 'completed' ? 'check' : r.status === 'failed' ? 'close' : r.status === 'running' ? 'play' : 'circle'" :size="12" />
                    </span>
                    <span class="runs-status-pill" :class="r.status">{{ statusText(r.status) }}</span>
                  </div>
                </td>
                <td><code style="font-size: 12px">{{ r.target_value || r.target_id || '—' }}</code></td>
                <td class="muted" style="font-family: var(--font-mono); font-size: 11.5px">{{ fmtTimeShort(r.started_at) }}</td>
                <td class="muted" style="font-family: var(--font-mono); font-size: 11.5px">{{ durationText(r) }}</td>
                <td class="muted" style="text-align: right; font-family: var(--font-mono); font-size: 11.5px">{{ fmtN((r.input_tokens || 0) + (r.output_tokens || 0)) }}</td>
              </tr>
            </tbody>
          </table>
        </template>
        <EmptyState v-else icon="play" title="还没有运行记录" description="授权目标之后，点击「新建运行」即可让 AI 启动一次渗透评估。" primary-label="去新建" secondary-label="查看授权目标" @primary="goNewTarget" @secondary="goTargets" />
      </PanelCard>

      <PanelCard title="最新发现">
        <template v-if="loading">
          <div class="sk-list">
            <UiSkeleton v-for="i in 4" :key="i" block height="42px" radius="8px" />
          </div>
        </template>
        <template v-else-if="vulns.length">
          <table class="findings-table">
            <thead>
              <tr><th>严重</th><th>标题</th><th>目标</th><th>状态</th></tr>
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
        <EmptyState v-else icon="shield" title="暂无漏洞发现" description="AI 还没在已运行的评估中找到可被利用的问题。当你跑起来第一次扫描，这里就会出现结构化记录。" primary-label="去授权目标" @primary="goTargets" />
      </PanelCard>
    </div>

    <!-- === ONBOARDING (true empty state) === -->
    <div v-if="isEmpty" class="enter enter-5 onboarding">
      <div class="onb-step">
        <span class="onb-num">01</span>
        <h4>授权目标</h4>
        <p>填入公司名、域名或 URL，AI 会自动识别目标类型并配置扫描策略。</p>
      </div>
      <div class="onb-arrow"><Icon name="arrow-right" :size="20" /></div>
      <div class="onb-step">
        <span class="onb-num">02</span>
        <h4>启动评估</h4>
        <p>描述目标说明（要找什么），AI 调度多 worker 并行探索，所有发现实时入库。</p>
      </div>
      <div class="onb-arrow"><Icon name="arrow-right" :size="20" /></div>
      <div class="onb-step">
        <span class="onb-num">03</span>
        <h4>查看报告</h4>
        <p>在「最近运行」和「最新发现」里跟踪进度，每个漏洞都带可复现的 curl PoC。</p>
      </div>
      <div class="onb-cta">
        <button class="onb-primary" @click="goNewTarget">
          <Icon name="plus" :size="14" />
          <span>开始第一次扫描</span>
        </button>
        <button class="onb-secondary" @click="goSearch">
          <Icon name="book" :size="14" />
          <span>查看历史对话</span>
        </button>
      </div>
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
.dashboard { gap: 28px; }
.hero-grid { display: grid; grid-template-columns: 1.7fr 1fr 1fr 1fr; gap: 22px; }
.chart-grid { display: grid; grid-template-columns: 1fr 1.4fr; gap: 22px; }
.donut-row { display: flex; align-items: center; gap: 28px; padding: 8px 0 4px; }
.sk-list { display: flex; flex-direction: column; gap: 8px; }

/* === HERO TOKENS CARD === */
.hero-tokens {
  position: relative;
  height: 100%;
  min-height: 130px;
  padding: 22px 24px 18px;
  border-radius: var(--radius-lg);
  background:
    linear-gradient(180deg, rgba(20, 32, 70, 0.7) 0%, rgba(8, 14, 36, 0.55) 100%);
  border: 1px solid rgba(125, 146, 232, 0.22);
  box-shadow: 0 1px 0 rgba(255, 255, 255, 0.04) inset, 0 4px 16px rgba(3, 6, 26, 0.4);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.hero-tokens::before {
  content: '';
  position: absolute;
  top: 0; left: 24px; right: 24px;
  height: 1px;
  background: linear-gradient(90deg, transparent, rgba(167, 139, 250, 0.6), transparent);
}
.hero-tokens-head { display: flex; align-items: center; gap: 12px; }
.hero-tokens-icon {
  width: 36px; height: 36px; border-radius: 9px;
  background: linear-gradient(135deg, rgba(167, 139, 250, 0.3), rgba(95, 110, 200, 0.3));
  color: var(--nebula-bright);
  border: 1px solid rgba(167, 139, 250, 0.3);
  display: flex; align-items: center; justify-content: center;
  box-shadow: 0 0 16px rgba(167, 139, 250, 0.25);
}
.hero-tokens-label {
  font-size: 11px;
  color: var(--text-faint);
  font-family: var(--font-display);
  letter-spacing: 0.12em;
  text-transform: uppercase;
  font-weight: 500;
}
.hero-tokens-sub {
  font-size: 11px;
  color: var(--text-dim);
  font-family: var(--font-mono);
  margin-top: 2px;
}
.hero-tokens-value {
  margin-top: 10px;
  display: flex; align-items: baseline; gap: 8px;
}
.hero-tokens-value .num {
  font-size: 38px;
  font-weight: 600;
  color: var(--text);
  font-family: var(--font-display);
  letter-spacing: -0.03em;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}
.hero-tokens-value .unit {
  font-size: 12px;
  color: var(--text-faint);
  font-family: var(--font-mono);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.hero-tokens-bars {
  display: flex; gap: 18px; margin-top: 10px;
}
.hero-tokens-bars .bar {
  display: flex; flex-direction: column; gap: 2px;
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}
.hero-tokens-bars .bar-label {
  font-size: 10px;
  color: var(--text-faint);
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.hero-tokens-bars .bar-num {
  font-size: 12.5px;
  color: var(--text-dim);
  font-weight: 500;
}
.hero-tokens-spark {
  position: absolute;
  right: -10px; bottom: -10px;
  width: 55%; height: 70%;
  opacity: 0.7;
  mask-image: linear-gradient(225deg, transparent 25%, #000 95%);
  -webkit-mask-image: linear-gradient(225deg, transparent 25%, #000 95%);
  pointer-events: none;
}

/* === ONBOARDING === */
.onboarding {
  display: grid;
  grid-template-columns: 1fr auto 1fr auto 1fr;
  align-items: stretch;
  gap: 18px;
  padding: 36px 32px 32px;
  background:
    linear-gradient(180deg, rgba(20, 32, 70, 0.55) 0%, rgba(8, 14, 36, 0.4) 100%);
  border: 1px solid rgba(125, 146, 232, 0.22);
  border-radius: var(--radius-lg);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
  position: relative;
  overflow: hidden;
}
.onboarding::before {
  content: '';
  position: absolute;
  top: 0; left: 0; right: 0;
  height: 1px;
  background: linear-gradient(90deg, transparent 5%, rgba(163, 180, 255, 0.3) 50%, transparent 95%);
}
.onb-step {
  display: flex; flex-direction: column;
  padding: 18px 4px;
  gap: 8px;
}
.onb-num {
  font-size: 11px;
  color: var(--stellar-bright);
  font-family: var(--font-mono);
  letter-spacing: 0.1em;
  font-weight: 600;
  opacity: 0.85;
}
.onb-step h4 {
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
  font-family: var(--font-display);
  letter-spacing: -0.01em;
  margin: 0;
}
.onb-step p {
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.6;
  margin: 0;
}
.onb-arrow {
  display: flex; align-items: center;
  color: var(--text-faint);
  align-self: center;
}
.onb-cta {
  grid-column: 1 / -1;
  display: flex; gap: 12px; justify-content: center;
  margin-top: 18px;
  padding-top: 22px;
  border-top: 1px solid var(--border-soft);
}
.onb-primary, .onb-secondary {
  display: inline-flex; align-items: center; gap: 8px;
  border-radius: 8px;
  padding: 10px 20px;
  min-height: 38px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}
.onb-primary {
  background: linear-gradient(135deg, rgba(125, 146, 232, 0.95) 0%, rgba(95, 110, 200, 0.95) 100%);
  color: #fff;
  border: 1px solid rgba(163, 180, 255, 0.4);
  box-shadow: 0 4px 20px rgba(125, 146, 232, 0.32);
}
.onb-primary:hover { filter: brightness(1.1); transform: translateY(-1px); }
.onb-secondary {
  background: transparent;
  color: var(--text-dim);
  border: 1px solid var(--border);
}
.onb-secondary:hover { color: var(--text); border-color: var(--border-bright); }

@media (max-width: 1280px) {
  .hero-grid { grid-template-columns: 1fr 1fr; }
  .chart-grid { grid-template-columns: 1fr; }
  .onboarding { grid-template-columns: 1fr; }
  .onb-arrow { transform: rotate(90deg); justify-self: center; }
}
</style>
