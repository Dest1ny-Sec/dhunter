<script setup lang="ts">
/**
 * Library — MCP 扩展 / Skills / 模板 三合一入口
 *
 * Layout: top tabs (MCP / Skills / 模板) + inside each tab a left
 * sidebar of categories (官方/社区/已装/自定义) + a right card grid.
 *
 * Style: stargaze design — dark, multi-hue star accents, generous
 * breathing room, hover glow on cards.
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import Icon from '../components/icons/Icon.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiModal from '../components/ui/UiModal.vue'
import MCPExtensions from '../components/MCPExtensions.vue'

const route = useRoute()
const router = useRouter()

// --- tab state --------------------------------------------------------
type TabKey = 'mcp' | 'skills' | 'templates'
const tabs: { key: TabKey; label: string; icon: string; subtitle: string }[] = [
  { key: 'mcp',       label: 'MCP 扩展', icon: 'plug',     subtitle: '把你自己的 MCP server 接进来，工具以 <server>::<tool> 命名空间暴露' },
  { key: 'skills',    label: 'Skills',  icon: 'puzzle',   subtitle: 'agent 的可复用工作流（系统 prompt / 提示策略），按场景启用' },
  { key: 'templates', label: '模板',     icon: 'template', subtitle: '行业场景包：OWASP Top 10、API Fuzz、CTF 入门等一键导入' },
]
const activeTab = ref<TabKey>(((route.query.tab as TabKey) || 'mcp') as TabKey)
watch(activeTab, (t) => router.replace({ query: { ...route.query, tab: t } }))
// Switching tab resets the sidebar category to "官方" — the user's
// intent in each tab is independent (templates don't have a "已装"
// bucket the same way skills do).
watch(activeTab, () => { activeCat.value = 'builtin' })

// --- shared left-sidebar categories -----------------------------------
type CatKey = 'builtin' | 'community' | 'installed' | 'custom'
const categories: { key: CatKey; label: string; hint: string }[] = [
  { key: 'builtin',   label: '官方',     hint: 'Dhunter 团队精选' },
  { key: 'community', label: '社区',     hint: '社区贡献（即将开放）' },
  { key: 'installed', label: '已装',     hint: '全部已启用的扩展' },
  { key: 'custom',    label: '自定义',   hint: '你自己的扩展' },
]
const activeCat = ref<CatKey>('builtin')

// --- skills data ------------------------------------------------------
interface Skill {
  id: string
  name: string
  title: string
  description: string
  content: string
  category: string
  tags: string
  enabled: boolean
  source: 'builtin' | 'community' | 'custom'
  updated_at: string
}
const skills = ref<Skill[]>([])
const skillBusy = ref(false)
async function loadSkills() {
  skillBusy.value = true
  try {
    const r = await api.get('/skills')
    skills.value = r.data?.skills || []
  } catch (e) {
    console.error('load skills', e)
  } finally {
    skillBusy.value = false
  }
}

// --- derived: skills per category -------------------------------------
const skillsByCategory = computed(() => {
  const out: Record<CatKey, Skill[]> = { builtin: [], community: [], installed: [], custom: [] }
  for (const s of skills.value) {
    if (s.source === 'builtin') out.builtin.push(s)
    else if (s.source === 'community') out.community.push(s)
    else if (s.source === 'custom') out.custom.push(s)
    if (s.enabled) out.installed.push(s)
  }
  return out
})

const visibleSkills = computed(() => {
  if (activeCat.value === 'installed') return skillsByCategory.value.installed
  return skillsByCategory.value[activeCat.value] || []
})

// --- skill detail modal -----------------------------------------------
const showSkill = ref<Skill | null>(null)
const editingSkill = ref<Skill | null>(null)
const skillForm = ref({ name: '', title: '', description: '', content: '', category: 'general', tags: '' })
const skillError = ref('')
const skillSaving = ref(false)

function openCreateSkill() {
  editingSkill.value = null
  skillForm.value = { name: '', title: '', description: '', content: '', category: 'general', tags: '' }
  skillError.value = ''
  showSkill.value = { id: '', name: '', title: '新建 Skill', description: '', content: '', category: 'general', tags: '', enabled: true, source: 'custom', updated_at: '' }
}
function openEditSkill(s: Skill) {
  editingSkill.value = s
  skillForm.value = { name: s.name, title: s.title, description: s.description, content: s.content, category: s.category, tags: s.tags }
  skillError.value = ''
  showSkill.value = s
}
async function saveSkill() {
  if (!editingSkill.value && !showSkill.value) return
  skillError.value = ''
  if (!/^[a-z0-9][a-z0-9_.\-]{0,63}$/.test(skillForm.value.name.trim())) {
    skillError.value = 'name 必须 ^[a-z0-9][a-z0-9_.-]{0,63}$'
    return
  }
  skillSaving.value = true
  try {
    if (editingSkill.value) {
      await api.put(`/skills/${editingSkill.value.id}`, skillForm.value)
    } else {
      await api.post('/skills', { ...skillForm.value, source: 'custom', enabled: true })
    }
    showSkill.value = null
    await loadSkills()
  } catch (e: any) {
    skillError.value = e?.response?.data?.error || e?.message || '保存失败'
  } finally {
    skillSaving.value = false
  }
}
async function toggleSkill(s: Skill) {
  try {
    await api.post(`/skills/${s.id}/toggle`, { enabled: !s.enabled })
    s.enabled = !s.enabled
  } catch (e) {
    console.error('toggle', e)
  }
}
async function deleteSkill(s: Skill) {
  if (!confirm(`删除自定义 skill「${s.title || s.name}」？`)) return
  try {
    await api.delete(`/skills/${s.id}`)
    await loadSkills()
  } catch (e: any) {
    alert('删除失败: ' + (e?.response?.data?.error || e?.message))
  }
}

// --- templates (static for v0.7.1) ------------------------------------
interface Template {
  id: string
  title: string
  desc: string
  category: string
  author: string
  status: 'available' | 'soon'
  tools: number
}
const templates: Template[] = [
  { id: 'owasp-top10-2025', title: 'OWASP Top 10 · 2025', desc: '按 A01–A10 顺序逐项排查 web 目标', category: 'web', author: '官方', status: 'available', tools: 14 },
  { id: 'api-fuzz-starter', title: 'API Fuzz 入门', desc: '认证绕过 + IDOR + 参数污染的标准路径', category: 'api', author: '官方', status: 'available', tools: 8 },
  { id: 'phishing-suite', title: '钓鱼套件分析', desc: '对一段钓鱼邮件 / 二维码做静态 + 链接追溯', category: 'social', author: '社区', status: 'soon', tools: 6 },
  { id: 'js-deep-audit', title: 'JS 深度审计', desc: 'fetch_js + 敏感模式匹配 + 凭据提取', category: 'web', author: '官方', status: 'available', tools: 7 },
  { id: 'ctf-starter', title: 'CTF 入门', desc: 'Web / Misc / Crypto 三类基础题目模板', category: 'ctf', author: '社区', status: 'soon', tools: 10 },
]
const visibleTemplates = computed(() => {
  if (activeCat.value === 'installed') return templates.filter((t) => t.status === 'available')
  if (activeCat.value === 'builtin') return templates.filter((t) => t.author === '官方')
  if (activeCat.value === 'community') return templates.filter((t) => t.author === '社区')
  if (activeCat.value === 'custom') return [] // user templates live in custom skills
  return templates
})

// --- count badge per category -----------------------------------------
function catCount(key: CatKey): number {
  if (activeTab.value === 'skills') return skillsByCategory.value[key]?.length || 0
  if (activeTab.value === 'templates') {
    if (key === 'installed') return templates.filter((t) => t.status === 'available').length
    if (key === 'builtin') return templates.filter((t) => t.author === '官方').length
    if (key === 'community') return templates.filter((t) => t.author === '社区').length
    if (key === 'custom') return 0
  }
  // MCP: we don't break out per-source server-side; show "全部" count.
  return 0
}

onMounted(() => {
  if (activeTab.value === 'skills') loadSkills()
})
watch(activeTab, (t) => { if (t === 'skills') loadSkills() })
</script>

<template>
  <div class="lib-shell">
    <header class="lib-head">
      <h2 class="page-title">库</h2>
      <p class="lead">{{ tabs.find((t) => t.key === activeTab)?.subtitle }}</p>
    </header>

    <!-- top tabs -->
    <div class="lib-tabs">
      <button
        v-for="t in tabs"
        :key="t.key"
        class="lib-tab"
        :class="{ active: activeTab === t.key }"
        @click="activeTab = t.key"
      >
        <Icon :name="t.icon" :size="16" />
        <span>{{ t.label }}</span>
      </button>
    </div>

    <!-- MCP tab: full-width extension center, no sidebar (the
         extension center IS the focused tool for MCP) -->
    <div v-if="activeTab === 'mcp'" class="lib-mcp">
      <MCPExtensions />
    </div>

    <!-- Skills / Templates: sidebar + grid -->
    <div v-else class="lib-layout">
      <aside class="lib-side">
        <button
          v-for="c in categories"
          :key="c.key"
          class="cat-btn"
          :class="{ active: activeCat === c.key }"
          @click="activeCat = c.key"
        >
          <div class="cat-row">
            <span class="cat-label">{{ c.label }}</span>
            <span v-if="catCount(c.key) > 0" class="cat-count">{{ catCount(c.key) }}</span>
          </div>
          <span class="cat-hint">{{ c.hint }}</span>
        </button>
      </aside>

      <div class="lib-main">
        <!-- SKILLS GRID -->
        <template v-if="activeTab === 'skills'">
          <div v-if="activeCat !== 'community'" class="toolbar">
            <span class="toolbar-text">{{ visibleSkills.length }} 个 skill</span>
            <UiButton variant="primary" @click="openCreateSkill" v-if="activeCat === 'custom' || activeCat === 'builtin'">
              <Icon name="plus" :size="13" />
              <span>{{ activeCat === 'custom' ? '新建自定义' : '基于此创建' }}</span>
            </UiButton>
          </div>
          <div v-if="!visibleSkills.length" class="cat-empty">
            <Icon name="inbox" :size="28" />
            <span>{{ activeCat === 'community' ? '社区仓库即将开放' : '这里还空着' }}</span>
          </div>
          <div v-else class="cards">
            <article v-for="s in visibleSkills" :key="s.id" class="skill-card" :class="{ off: !s.enabled }">
              <div class="card-head">
                <div class="card-titles">
                  <span class="card-title">{{ s.title || s.name }}</span>
                  <code class="card-name">{{ s.name }}</code>
                </div>
                <UiBadge :kind="s.source === 'builtin' ? 'asset' : s.source === 'custom' ? 'status' : 'status'" :value="s.source" />
              </div>
              <p class="card-desc">{{ s.description || '—' }}</p>
              <div class="card-meta">
                <span class="tag" v-for="t in s.tags.split(',').filter(Boolean)" :key="t">{{ t.trim() }}</span>
              </div>
              <div class="card-actions">
                <button class="toggle" :class="{ on: s.enabled }" @click="toggleSkill(s)">
                  <span class="toggle-knob" />
                  <span class="toggle-text">{{ s.enabled ? '已启用' : '已停用' }}</span>
                </button>
                <button class="ghost-btn" @click="openEditSkill(s)" :title="s.source === 'builtin' ? '查看 + 切换状态' : '编辑'">
                  <Icon name="edit" :size="13" />
                </button>
                <button v-if="s.source !== 'builtin'" class="ghost-btn danger" @click="deleteSkill(s)" title="删除">
                  <Icon name="trash" :size="13" />
                </button>
              </div>
            </article>
          </div>
        </template>

        <!-- TEMPLATES GRID -->
        <template v-else-if="activeTab === 'templates'">
          <div class="toolbar">
            <span class="toolbar-text">{{ visibleTemplates.length }} 个模板</span>
            <UiButton variant="primary" disabled>
              <Icon name="plus" :size="13" />
              <span>提交模板 (即将开放)</span>
            </UiButton>
          </div>
          <div v-if="!visibleTemplates.length" class="cat-empty">
            <Icon name="inbox" :size="28" />
            <span>这里还空着</span>
          </div>
          <div v-else class="cards">
            <article v-for="t in visibleTemplates" :key="t.id" class="tpl-card" :class="{ soon: t.status === 'soon' }">
              <div class="card-head">
                <Icon name="template" :size="16" />
                <span class="card-title">{{ t.title }}</span>
                <UiBadge kind="status" :value="t.status === 'available' ? 'available' : 'soon'" />
              </div>
              <p class="card-desc">{{ t.desc }}</p>
              <div class="card-meta">
                <span class="tag">{{ t.category }}</span>
                <span class="tag">{{ t.tools }} 工具</span>
                <span class="tag">{{ t.author }}</span>
              </div>
              <div class="card-actions">
                <UiButton size="sm" :disabled="t.status !== 'available'">
                  {{ t.status === 'available' ? '导入' : '敬请期待' }}
                </UiButton>
              </div>
            </article>
          </div>
        </template>
      </div>
    </div>

    <!-- Skill create/edit modal -->
    <UiModal :open="!!showSkill" :title="editingSkill ? `编辑 · ${editingSkill.name}` : '新建 Skill'" width="680px" @close="showSkill = null">
      <form class="form" @submit.prevent="saveSkill">
        <div class="form-grid">
          <div>
            <label class="field-label">name（kebab/snake）</label>
            <input v-model="skillForm.name" :disabled="!!editingSkill && editingSkill.source === 'builtin'" placeholder="my-pentest-flow" />
            <div class="hint">用于引用与匹配；只允许 [a-z0-9_.-]，最长 64 字</div>
          </div>
          <div>
            <label class="field-label">显示标题</label>
            <input v-model="skillForm.title" :disabled="!!editingSkill && editingSkill.source === 'builtin'" placeholder="我的 Pentest 工作流" />
          </div>
        </div>
        <div>
          <label class="field-label">一句话描述</label>
          <input v-model="skillForm.description" :disabled="!!editingSkill && editingSkill.source === 'builtin'" />
        </div>
        <div class="form-grid">
          <div>
            <label class="field-label">分类</label>
            <input v-model="skillForm.category" :disabled="!!editingSkill && editingSkill.source === 'builtin'" placeholder="web" />
          </div>
          <div>
            <label class="field-label">标签（逗号分隔）</label>
            <input v-model="skillForm.tags" :disabled="!!editingSkill && editingSkill.source === 'builtin'" placeholder="recon,fuzz" />
          </div>
        </div>
        <div>
          <label class="field-label">Skill 内容（Markdown）</label>
          <textarea v-model="skillForm.content" :disabled="!!editingSkill && editingSkill.source === 'builtin'" rows="14" placeholder="# 标题&#10;&#10;1. 第一步&#10;2. 第二步" class="code-area" />
          <div v-if="editingSkill && editingSkill.source === 'builtin'" class="hint warn">⚠ 官方 skill 的内容随版本固定；如需调整可「基于此创建」一份自定义副本</div>
        </div>
        <div v-if="skillError" class="error-msg">{{ skillError }}</div>
        <div class="form-actions">
          <UiButton @click="showSkill = null" type="button">取消</UiButton>
          <UiButton variant="primary" type="submit" :disabled="skillSaving">
            {{ skillSaving ? '保存中…' : '保存' }}
          </UiButton>
        </div>
      </form>
    </UiModal>
  </div>
</template>

<style scoped>
/* ----- top-level shell ------------------------------------------- */
.lib-shell { display: flex; flex-direction: column; gap: 20px; }
.lib-head { margin-bottom: 4px; }
.lead { font-size: 13px; color: var(--text-dim); max-width: 70ch; line-height: 1.55; margin: 6px 0 0; }

/* ----- top tabs --------------------------------------------------- */
.lib-tabs { display: flex; gap: 4px; padding: 4px; background: var(--bg-elev); border: 1px solid var(--border); border-radius: var(--radius-md); width: fit-content; }
.lib-tab {
  display: flex; align-items: center; gap: 6px;
  padding: 8px 16px;
  background: transparent; border: 0;
  color: var(--text-dim);
  font-size: 13px; font-weight: 500;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: all 0.15s ease;
}
.lib-tab:hover { color: var(--text); background: var(--bg-elev-2); }
.lib-tab.active { color: var(--stellar-bright); background: var(--bg-elev-3); box-shadow: inset 0 0 0 1px var(--border-bright); }

/* ----- MCP tab: full-bleed --------------------------------------- */
.lib-mcp { }

/* ----- Skills / Templates: sidebar + main ----------------------- */
.lib-layout { display: grid; grid-template-columns: 220px 1fr; gap: 24px; align-items: start; }
@media (max-width: 980px) { .lib-layout { grid-template-columns: 1fr; } }

.lib-side { display: flex; flex-direction: column; gap: 4px; position: sticky; top: 16px; }
.cat-btn {
  display: flex; flex-direction: column; align-items: flex-start; gap: 2px;
  background: transparent; border: 1px solid transparent;
  padding: 10px 14px;
  border-radius: var(--radius-md);
  cursor: pointer;
  text-align: left;
  transition: all 0.15s ease;
  color: var(--text-dim);
}
.cat-btn:hover { background: var(--bg-elev); color: var(--text); }
.cat-btn.active { background: var(--bg-elev-2); border-color: var(--border-bright); color: var(--text); }
.cat-row { display: flex; align-items: center; justify-content: space-between; width: 100%; }
.cat-label { font-size: 13px; font-weight: 500; }
.cat-count { font-family: 'JetBrains Mono', monospace; font-size: 10.5px; padding: 1px 7px; border-radius: 999px; background: var(--bg-elev-3); color: var(--text-faint); }
.cat-btn.active .cat-count { background: rgba(125, 146, 232, 0.18); color: var(--stellar-bright); }
.cat-hint { font-size: 11.5px; color: var(--text-faint); }

.lib-main { display: flex; flex-direction: column; gap: 16px; min-width: 0; }
.toolbar { display: flex; align-items: center; justify-content: space-between; }
.toolbar-text { font-size: 12.5px; color: var(--text-dim); font-family: 'JetBrains Mono', monospace; }

.cat-empty {
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 8px; padding: 56px 24px;
  background: var(--bg-elev); border: 1px dashed var(--border);
  border-radius: var(--radius-md);
  color: var(--text-faint);
  font-size: 13px;
}

/* ----- card grid ------------------------------------------------- */
.cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px; }
.skill-card, .tpl-card {
  display: flex; flex-direction: column; gap: 10px;
  padding: 16px 18px;
  background: var(--bg-elev-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  transition: border-color 0.15s ease, transform 0.15s ease, box-shadow 0.15s ease;
}
.skill-card:hover, .tpl-card:hover {
  border-color: var(--border-bright);
  transform: translateY(-1px);
  box-shadow: 0 4px 24px -8px rgba(125, 146, 232, 0.18);
}
.skill-card.off { opacity: 0.55; }
.tpl-card.soon { opacity: 0.7; }

.card-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; }
.card-titles { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.card-title { font-size: 14px; font-weight: 500; color: var(--text); }
.card-name { font-family: 'JetBrains Mono', monospace; font-size: 11px; color: var(--text-faint); }

.card-desc { font-size: 12.5px; color: var(--text-dim); line-height: 1.55; margin: 0; min-height: 38px; }

.card-meta { display: flex; flex-wrap: wrap; gap: 4px; }
.tag {
  font-family: 'JetBrains Mono', monospace; font-size: 10.5px;
  padding: 2px 8px; border-radius: 999px;
  background: var(--bg-elev-3); color: var(--text-faint);
  border: 1px solid var(--border-soft);
}

.card-actions { display: flex; align-items: center; gap: 8px; margin-top: 4px; }

/* toggle switch (Aurora for on, gray for off) */
.toggle {
  display: inline-flex; align-items: center; gap: 8px;
  background: transparent; border: 0; padding: 0; cursor: pointer;
  font-size: 12px; color: var(--text-dim);
  font-family: 'JetBrains Mono', monospace;
}
.toggle-knob {
  position: relative;
  width: 30px; height: 16px;
  background: var(--bg-elev-3); border-radius: 999px;
  transition: background 0.18s ease;
}
.toggle-knob::after {
  content: ''; position: absolute;
  width: 12px; height: 12px; top: 2px; left: 2px;
  background: var(--text-faint); border-radius: 50%;
  transition: all 0.18s ease;
}
.toggle.on .toggle-knob { background: rgba(95, 200, 212, 0.25); box-shadow: 0 0 0 3px rgba(95, 200, 212, 0.08); }
.toggle.on .toggle-knob::after { left: 16px; background: var(--aurora-bright); }
.toggle.on { color: var(--aurora-bright); }

/* small ghost icon button */
.ghost-btn {
  display: inline-flex; align-items: center; justify-content: center;
  width: 28px; height: 28px;
  background: transparent; border: 1px solid transparent;
  border-radius: var(--radius-sm);
  color: var(--text-dim); cursor: pointer;
  transition: all 0.12s ease;
}
.ghost-btn:hover { color: var(--text); background: var(--bg-elev-3); border-color: var(--border); }
.ghost-btn.danger:hover { color: var(--danger); border-color: rgba(232, 89, 89, 0.3); }

/* ----- form ------------------------------------------------------ */
.form { display: flex; flex-direction: column; gap: 14px; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.field-label { font-size: 12px; color: var(--text-dim); margin-bottom: 4px; display: block; }
.hint { font-size: 11.5px; color: var(--text-faint); margin-top: 4px; }
.hint.warn { color: var(--warn); }
.code-area {
  font-family: 'JetBrains Mono', monospace; font-size: 12.5px;
  width: 100%; resize: vertical;
  background: var(--bg-elev-2); color: var(--text);
  border: 1px solid var(--border); border-radius: var(--radius-sm);
  padding: 10px 12px;
}
.form-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }
.error-msg { font-size: 12.5px; color: var(--danger); }
</style>
