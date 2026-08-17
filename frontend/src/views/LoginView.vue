<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import UiButton from '../components/ui/UiButton.vue'
import Icon from '../components/icons/Icon.vue'
import BrandMark from '../components/icons/BrandMark.vue'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const error = ref<string | null>(null)
const loading = ref(false)

const brandText = 'Dhunter'
const brandDelays = brandText.split('').map((_, i) => i * 55)

const features = [
  { label: '智能爬取', icon: 'compass' },
  { label: '深度分析', icon: 'sparkle' },
  { label: '报告输出', icon: 'trending' },
] as const

async function submit() {
  if (!username.value || !password.value) {
    error.value = '请输入账号和密码'
    return
  }
  error.value = null
  loading.value = true
  try {
    await auth.login(username.value, password.value)
    router.push((route.query.redirect as string) || '/dashboard')
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-shell">
    <!-- 5 shooting stars, staggered -->
    <span class="shooting-star s1" aria-hidden="true" />
    <span class="shooting-star s2" aria-hidden="true" />
    <span class="shooting-star s3" aria-hidden="true" />
    <span class="shooting-star s4" aria-hidden="true" />
    <span class="shooting-star s5" aria-hidden="true" />

    <!-- 6 twinkling stars, always pulsing -->
    <span class="twinkle t1" aria-hidden="true" />
    <span class="twinkle t2" aria-hidden="true" />
    <span class="twinkle t3" aria-hidden="true" />
    <span class="twinkle t4" aria-hidden="true" />
    <span class="twinkle t5" aria-hidden="true" />
    <span class="twinkle t6" aria-hidden="true" />

    <!-- breathing ambient halo behind the card -->
    <div class="card-halo" aria-hidden="true" />

    <div class="login-card card">
      <div class="login-brand">
        <div class="brand-mark">
          <BrandMark :size="56" :glow="true" :animate="true" />
        </div>
        <h1 class="brand-chars" aria-label="Dhunter">
          <span
            v-for="(c, i) in brandText.split('')"
            :key="i"
            class="ch"
            :style="{ '--d': brandDelays[i] + 'ms' }"
            >{{ c }}</span
          >
        </h1>
      </div>
      <p class="login-tagline">AI 驱动的渗透测试平台</p>

      <form @submit.prevent="submit" class="col login-form" novalidate>
        <div class="login-field" style="--d: 800ms">
          <label class="field-label">账号</label>
          <input v-model="username" autocomplete="username" style="width: 100%" />
        </div>
        <div class="login-field" style="--d: 880ms">
          <label class="field-label">密码</label>
          <input v-model="password" type="password" autocomplete="current-password" style="width: 100%" />
        </div>
        <div v-if="error" class="login-error" style="--d: 960ms">
          <span class="err-dot" />{{ error }}
        </div>
        <div class="login-submit" style="--d: 1040ms">
          <UiButton type="submit" variant="primary" size="lg" :disabled="loading">
            {{ loading ? '登录中…' : '登录' }}
          </UiButton>
        </div>
      </form>

      <!-- 3 capability pills — gives the card real content density -->
      <div class="login-features" style="--d: 1120ms">
        <span v-for="(f, i) in features" :key="i" class="lf-pill">
          <span class="lf-ico"><Icon :name="f.icon" :size="11" /></span>
          {{ f.label }}
        </span>
      </div>

      <p class="first-run muted">首次运行请查看启动横幅获取默认账号（默认 admin / 随机密码）；已修改过密码请用当前凭据</p>

      <details class="forgot">
        <summary>忘记密码？</summary>
        <ol class="muted">
          <li>查看启动横幅或日志（首次运行会打印一次 <code>password</code>）</li>
          <li>在 <code>configs/dhunter.yaml</code> 设置 <code>admin.bootstrap_password</code>，重启后用它登录</li>
          <li>删除数据库 settings 表中的 <code>admin_password_hash</code>，重启会重新随机生成并打印</li>
        </ol>
      </details>

      <p class="disclaimer">
        <span class="warn-icon" aria-hidden="true">⚠</span>
        仅供学术交流与安全研究使用 · 禁止用于任何非法或盈利行为
      </p>
    </div>
  </div>
</template>

<style scoped>
.login-brand {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  margin-bottom: 6px;
}
.brand-mark {
  background: linear-gradient(135deg, #a78bfa 0%, #6d56c0 100%);
  display: flex; align-items: center; justify-content: center;
  border-radius: 14px;
  position: relative;
  /* overflow:visible — let orbital particles escape the mark */
}
.brand-mark::after {
  content: ''; position: absolute; inset: 0;
  background: linear-gradient(135deg, rgba(255, 255, 255, 0.18), transparent 55%);
  pointer-events: none;
}
.field-label {
  font-size: 11.5px;
  color: var(--text-faint);
  margin-bottom: 6px;
  display: block;
  font-family: var(--font-display);
  letter-spacing: 0.04em;
}
.disclaimer, .first-run {
  font-size: 11.5px;
  text-align: center;
  margin: 8px 0 0;
  color: var(--text-faint);
  line-height: 1.55;
}
.first-run { color: var(--text-dim); }
.warn-icon { color: var(--sev-high); margin-right: 4px; }

/* per-field stagger via inline --d */
.login-field, .login-error, .login-submit {
  opacity: 0;
  transform: translate3d(0, 8px, 0);
  animation: lf-rise 0.6s cubic-bezier(0.16, 1, 0.3, 1) forwards;
  animation-delay: var(--d, 0ms);
}
.login-error {
  display: flex; align-items: center; gap: 8px;
  padding: 9px 12px;
  background: rgba(226, 100, 114, 0.1);
  border: 1px solid rgba(226, 100, 114, 0.32);
  border-radius: 6px;
  font-size: 12.5px;
  color: var(--sev-critical);
}
.login-error .err-dot {
  width: 6px; height: 6px; border-radius: 50%;
  background: var(--sev-critical);
  box-shadow: 0 0 6px var(--sev-critical);
  flex-shrink: 0;
}
@keyframes lf-rise {
  to { opacity: 1; transform: translate3d(0, 0, 0); }
}
</style>

<style scoped>
.forgot { margin-top: 10px; font-size: 12px; color: var(--text-dim); }
.forgot summary { cursor: pointer; color: var(--accent); font-size: 12px; }
.forgot ol { margin: 6px 0 0 18px; padding: 0; line-height: 1.7; }
.forgot code { background: var(--bg-elev-2); padding: 0 4px; border-radius: 4px; }
</style>
