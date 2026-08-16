<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import UiModal from './ui/UiModal.vue'
import UiButton from './ui/UiButton.vue'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const router = useRouter()
const step = ref(1)

interface OnboardStep { icon: string; title: string; desc: string; action?: { label: string; path: string } }

const steps: OnboardStep[] = [
  {
    icon: '👋',
    title: '欢迎使用 Dhunter',
    desc: '一个由 AI 驱动的渗透测试平台：你把「已授权的目标」交给它，它会像真人渗透测试员一样侦察、规划、主动测试，并把验证过的漏洞汇总成报告。',
  },
  {
    icon: '🤖',
    title: '第 1 步 · 配置 AI 模型',
    desc: '平台调用你自己的大模型 API（兼容 OpenAI / Anthropic 协议，支持 DeepSeek、MiniMax、Qwen、GLM、Claude 等）。在设置页填入 Base URL、模型与 API Key 并「测试连接」。',
    action: { label: '去配置模型', path: '/settings' },
  },
  {
    icon: '🎯',
    title: '第 2 步 · 授权一个目标',
    desc: '填写公司名 / 域名 / URL / IP，勾选「已获授权」，点击「启动评估」即可开始。可选的：登录会话、双账号（IDOR 测试）、红线（AI 必须遵守的边界）。',
    action: { label: '去授权目标', path: '/targets' },
  },
]

const current = computed<OnboardStep>(() => steps[step.value - 1]!)

function done() {
  localStorage.setItem('dhunter_onboarded', '1')
  emit('close')
}

function next() {
  if (step.value < steps.length) step.value++
  else done()
}

function goAction(path?: string) {
  if (path) router.push(path)
  done()
}

function skip() { done() }
</script>

<template>
  <UiModal :open="open" title="快速上手" width="520px" @close="skip">
    <div class="onboard">
      <div class="onboard-icon">{{ current.icon }}</div>
      <h3 class="onboard-title">{{ current.title }}</h3>
      <p class="onboard-desc">{{ current.desc }}</p>
      <div class="onboard-dots">
        <span v-for="i in steps.length" :key="i" class="dot" :class="{ active: i === step }" />
      </div>
      <div class="onboard-actions">
        <button class="ghost" @click="skip">跳过</button>
        <span class="spacer" />
        <UiButton v-if="current.action" variant="primary" @click="goAction(current.action.path)">
          {{ current.action.label }}
        </UiButton>
        <UiButton v-else variant="primary" @click="next">下一步 →</UiButton>
      </div>
    </div>
  </UiModal>
</template>

<style scoped>
.onboard { text-align: center; padding: 6px 4px 2px; }
.onboard-icon { font-size: 40px; line-height: 1; margin-bottom: 12px; }
.onboard-title { font-size: 17px; font-weight: 600; margin: 0 0 10px; }
.onboard-desc { font-size: 13px; color: var(--text-dim); line-height: 1.7; margin: 0 auto 16px; max-width: 420px; }
.onboard-dots { display: flex; gap: 6px; justify-content: center; margin-bottom: 16px; }
.dot { width: 7px; height: 7px; border-radius: 999px; background: var(--border); transition: background 0.2s; }
.dot.active { background: var(--accent); width: 18px; }
.onboard-actions { display: flex; align-items: center; gap: 10px; }
</style>
