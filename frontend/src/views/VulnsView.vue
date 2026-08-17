<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import SeverityBadge from '../components/SeverityBadge.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiModal from '../components/ui/UiModal.vue'
import UiSkeleton from '../components/ui/UiSkeleton.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import Icon from '../components/icons/Icon.vue'

interface Vuln {
  id: string; run_id?: string; target_id?: string; target?: string; url?: string; title?: string; name?: string; severity: string;
  status?: string; evidence?: any; description?: string; impact?: string; recommendation?: string; reproduction?: string;
}

const router = useRouter()
const vulns = ref<Vuln[]>([])
const runs = ref<any[]>([])
const targets = ref<any[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const filterSeverity = ref('')
const filterRun = ref('')
const filterStatus = ref('')
const detail = ref<Vuln | null>(null)

const severities = ['critical', 'high', 'medium', 'low', 'info']
const statuses = ['confirmed', 'dismissed', 'pending', 'open']

// Resolve the human-readable target name for a vulnerability by walking
// run_id → run → target_id → target. Falls back to the raw target/url on
// the vuln record, or the target_id slice, or a final "—".
function targetName(v: Vuln): string {
  if (v.target && !/^[a-z0-9-]{8,}$/i.test(v.target)) return v.target  // not a bare id
  if (v.run_id) {
    const run = runs.value.find((r) => r.id === v.run_id)
    if (run?.target_value) return run.target_value
    if (run?.target_id) {
      const t = targets.value.find((x) => x.id === run.target_id)
      if (t) return t.name || t.value || t.normalized || t.id.slice(0, 8)
    }
  }
  if (v.target_id) {
    const t = targets.value.find((x) => x.id === v.target_id)
    if (t) return t.name || t.value || t.normalized || t.id.slice(0, 8)
  }
  if (v.target) return v.target.slice(0, 8)
  if (v.url) return v.url
  return '—'
}

const filtered = computed(() => vulns.value.filter((v) => {
  if (filterSeverity.value && v.severity?.toLowerCase() !== filterSeverity.value) return false
  if (filterRun.value && v.run_id !== filterRun.value) return false
  if (filterStatus.value && v.status !== filterStatus.value) return false
  return true
}))
const confirmedCount = computed(() => vulns.value.filter((v) => v.status === 'confirmed').length)
const isFilteredEmpty = computed(() => filtered.value.length === 0 && !loading.value)

function fmtEvidence(e: any): string {
  if (e == null) return ''
  if (typeof e === 'string') return e
  try { return JSON.stringify(e, null, 2) } catch { return String(e) }
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const [vRes, rRes, tRes] = await Promise.all([
      api.get('/vulnerabilities'), api.get('/runs'), api.get('/targets'),
    ])
    vulns.value = Array.isArray(vRes.data) ? vRes.data : vRes.data?.vulnerabilities || []
    runs.value = Array.isArray(rRes.data) ? rRes.data : rRes.data?.runs || []
    targets.value = Array.isArray(tRes.data) ? tRes.data : tRes.data?.targets || []
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || '加载失败，请检查后端服务是否运行'
  } finally { loading.value = false }
}

onMounted(load)
</script>

<template>
  <div class="col">
    <div class="vulns-head">
      <div>
        <h2 class="page-title">漏洞库</h2>
        <div class="muted" style="font-size: 13px; margin-top: 2px">共 {{ confirmedCount }} 个已确认漏洞，跨所有授权目标</div>
      </div>
      <button @click="load" :disabled="loading" aria-label="刷新">
        <Icon name="refresh" :size="14" />
        <span style="margin-left: 6px">刷新</span>
      </button>
    </div>

    <div class="card row filters">
      <select v-model="filterSeverity" style="min-width: 130px" aria-label="按严重度筛选">
        <option value="">全部严重度</option>
        <option v-for="s in severities" :key="s" :value="s">{{ ({ critical: '严重', high: '高危', medium: '中危', low: '低危', info: '信息' } as Record<string,string>)[s] || s }}</option>
      </select>
      <select v-model="filterStatus" style="min-width: 130px" aria-label="按状态筛选">
        <option value="">全部状态</option>
        <option v-for="s in statuses" :key="s" :value="s">{{ ({ confirmed: '已确认', dismissed: '已忽略', pending: '待审', open: '待审' } as Record<string,string>)[s] || s }}</option>
      </select>
      <select v-model="filterRun" style="min-width: 200px" aria-label="按运行筛选">
        <option value="">全部运行</option>
        <option v-for="r in runs" :key="r.id" :value="r.id">{{ r.id.slice(0, 8) }}{{ r.target_value ? ` — ${r.target_value}` : '' }}</option>
      </select>
      <div class="spacer" />
      <span class="muted" style="font-size: 12px; font-family: var(--font-mono); font-variant-numeric: tabular-nums">{{ filtered.length }} / {{ vulns.length }}</span>
    </div>

    <div v-if="error" class="error-state">
      <Icon name="alert" :size="18" />
      <div>
        <div class="error-title">加载失败</div>
        <div class="error-msg">{{ error }}</div>
      </div>
    </div>

    <div v-else-if="loading" class="card" style="padding: 18px">
      <div class="sk-list">
        <UiSkeleton v-for="i in 5" :key="i" block height="44px" radius="8px" />
      </div>
    </div>

    <div v-else-if="filtered.length" class="card" style="padding: 0">
      <table class="stagger">
        <thead>
          <tr><th>严重度</th><th>标题</th><th>目标</th><th>运行</th><th>状态</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="v in filtered" :key="v.id">
            <td><SeverityBadge :severity="v.severity || 'info'" /></td>
            <td>
              <div style="font-size: 13px">{{ v.title || v.name || v.id }}</div>
              <div v-if="v.description" class="muted" style="font-size: 11px; margin-top: 2px">{{ v.description.slice(0, 140) }}{{ v.description.length > 140 ? '…' : '' }}</div>
            </td>
            <td>
              <span class="target-cell" @click="v.run_id && router.push(`/runs/${v.run_id}`)" :class="{ link: !!v.run_id }">
                {{ targetName(v) }}
              </span>
            </td>
            <td>
              <a v-if="v.run_id" class="run-id" :href="`/runs/${v.run_id}`" @click.prevent="router.push(`/runs/${v.run_id}`)">
                <Icon name="play" :size="11" />
                {{ v.run_id.slice(0, 8) }}
              </a>
              <span v-else class="muted">—</span>
            </td>
            <td><UiBadge kind="status" :value="v.status || 'open'" /></td>
            <td><button class="ghost-btn" @click="detail = v">详情</button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else-if="isFilteredEmpty" class="card" style="padding: 0">
      <EmptyState
        :icon="vulns.length === 0 ? 'shield' : 'search'"
        :title="vulns.length === 0 ? '漏洞库是空的' : '没有匹配当前筛选的漏洞'"
        :description="vulns.length === 0
          ? 'AI 还没有在已运行的评估中找到可被利用的问题。授权目标并启动一次扫描，漏洞会自动入库。'
          : '试试切换更宽松的严重度或状态，或者直接点「清空筛选」看全部。'"
        :primary-label="vulns.length === 0 ? '去授权目标' : '清空筛选'"
        :secondary-label="vulns.length === 0 ? '查看运行记录' : undefined"
        @primary="vulns.length === 0 ? router.push('/targets') : (filterSeverity = '', filterStatus = '', filterRun = '')"
        @secondary="router.push('/runs')"
      />
    </div>

    <UiModal :open="!!detail" title="漏洞详情" @close="detail = null">
      <div v-if="detail" class="detail">
        <div class="row">
          <SeverityBadge :severity="detail.severity || 'info'" />
          <UiBadge kind="status" :value="detail.status || 'open'" />
        </div>
        <h3 style="font-size: 15px; margin: 12px 0 6px">{{ detail.title }}</h3>
        <div class="muted" style="font-size: 12px; margin-bottom: 12px"><code>{{ detail.target || detail.url || detail.id }}</code></div>
        <template v-if="detail.description"><h4 class="sec">漏洞描述</h4><p>{{ detail.description }}</p></template>
        <template v-if="detail.impact"><h4 class="sec">影响范围</h4><p>{{ detail.impact }}</p></template>
        <template v-if="detail.recommendation"><h4 class="sec">修复建议</h4><p>{{ detail.recommendation }}</p></template>
        <template v-if="detail.reproduction">
          <h4 class="sec">复现步骤</h4>
          <pre style="max-height: 260px; overflow: auto"><code>{{ detail.reproduction }}</code></pre>
        </template>
        <template v-if="detail.evidence">
          <h4 class="sec">证据</h4>
          <pre style="max-height: 260px; overflow: auto"><code>{{ fmtEvidence(detail.evidence) }}</code></pre>
        </template>
      </div>
    </UiModal>
  </div>
</template>

<style scoped>
.vulns-head { display: flex; justify-content: space-between; align-items: flex-start; }
.filters { flex-wrap: wrap; }
.sk-list { display: flex; flex-direction: column; gap: 8px; }
.detail .sec { font-size: 12px; color: var(--text-dim); text-transform: uppercase; letter-spacing: 0.05em; margin: 14px 0 6px; }
.detail p { font-size: 13px; line-height: 1.6; }

/* clickable target cell — was just "—" before, now shows project name and is a link */
.target-cell { font-size: 12.5px; color: var(--text-dim); }
.target-cell.link { color: var(--text); cursor: pointer; transition: color 0.15s; }
.target-cell.link:hover { color: var(--stellar-bright); text-decoration: underline; text-decoration-color: rgba(125, 146, 232, 0.4); text-underline-offset: 3px; }

/* run id — small chip-like link */
.run-id {
  display: inline-flex; align-items: center; gap: 4px;
  font-family: var(--font-mono);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  color: var(--text-dim);
  padding: 2px 7px;
  border-radius: 4px;
  background: rgba(125, 146, 232, 0.08);
  border: 1px solid transparent;
  text-decoration: none;
  transition: all 0.15s;
}
.run-id:hover {
  color: var(--stellar-bright);
  background: rgba(125, 146, 232, 0.16);
  border-color: var(--border-bright);
}
.run-id > svg { opacity: 0.6; }

/* ghost button for 详情 — soft, doesn't fight the row */
.ghost-btn {
  min-height: 28px; padding: 0 12px; font-size: 12px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--text-dim);
  cursor: pointer;
  transition: all 0.15s;
}
.ghost-btn:hover {
  color: var(--text);
  border-color: var(--border-bright);
  background: rgba(125, 146, 232, 0.08);
}

.error-state {
  display: flex; align-items: flex-start; gap: 12px;
  padding: 16px 18px;
  background: rgba(226, 100, 114, 0.08);
  border: 1px solid rgba(226, 100, 114, 0.28);
  border-radius: var(--radius);
  color: var(--text);
}
.error-state > svg { color: var(--sev-critical); margin-top: 2px; }
.error-title { font-size: 13px; font-weight: 600; color: var(--text); }
.error-msg { font-size: 12px; color: var(--text-dim); margin-top: 2px; }
</style>
