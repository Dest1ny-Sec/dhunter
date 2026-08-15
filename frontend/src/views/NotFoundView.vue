<script setup lang="ts">
import { useRouter } from 'vue-router'
import Icon from '../components/icons/Icon.vue'

const router = useRouter()
</script>

<template>
  <div class="nf-shell">
    <div class="nf-glow" />
    <div class="nf-card">
      <div class="nf-code">404</div>
      <div class="nf-orbit">
        <span class="o o1" />
        <span class="o o2" />
        <span class="o o3" />
      </div>
      <h1 class="nf-title">这片星域还没有坐标</h1>
      <p class="nf-desc">你访问的页面不在星图上。或许它从未存在，或许它迁移到了新的轨道。无论哪种情况，掉头回去都是好主意。</p>
      <div class="nf-actions">
        <button class="nf-primary" @click="router.push('/dashboard')">
          <Icon name="dashboard" :size="15" />
          <span>回到仪表盘</span>
        </button>
        <button class="nf-secondary" @click="router.back()">
          <Icon name="arrow-left" :size="15" />
          <span>返回上一页</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.nf-shell {
  position: relative;
  min-height: 100dvh;
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
  overflow: hidden;
}
.nf-glow {
  position: absolute; inset: 0;
  background:
    radial-gradient(ellipse 50% 60% at 30% 30%, rgba(125, 146, 232, 0.16), transparent 60%),
    radial-gradient(ellipse 60% 50% at 70% 70%, rgba(155, 140, 232, 0.12), transparent 60%);
  filter: blur(40px);
  pointer-events: none;
}
.nf-card {
  position: relative;
  max-width: 480px;
  text-align: center;
  background: linear-gradient(180deg, rgba(20, 32, 70, 0.6) 0%, rgba(8, 14, 36, 0.45) 100%);
  border: 1px solid rgba(125, 146, 232, 0.22);
  border-radius: 18px;
  padding: 56px 40px 44px;
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  box-shadow: 0 1px 0 rgba(255, 255, 255, 0.04) inset, 0 8px 32px rgba(3, 6, 26, 0.5);
}
.nf-code {
  font-size: 88px;
  font-weight: 600;
  font-family: var(--font-display);
  letter-spacing: -0.04em;
  line-height: 1;
  background: linear-gradient(135deg, var(--stellar-bright) 0%, var(--nebula-bright) 100%);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  color: transparent;
  margin-bottom: 4px;
  position: relative;
  display: inline-block;
}
.nf-orbit {
  position: relative;
  width: 200px;
  height: 40px;
  margin: 0 auto 18px;
}
.nf-orbit .o {
  position: absolute;
  width: 6px; height: 6px;
  border-radius: 50%;
  background: var(--stellar-bright);
  box-shadow: 0 0 10px currentColor;
  top: 50%;
}
.nf-orbit .o1 { left: 30%; background: var(--nebula-bright); animation: orbit1 6s ease-in-out infinite; }
.nf-orbit .o2 { left: 50%; transform: translate(-50%, -50%); background: var(--aurora-bright); animation: orbit2 5s ease-in-out infinite; }
.nf-orbit .o3 { left: 70%; background: var(--star-amber-bright); animation: orbit3 7s ease-in-out infinite; }
@keyframes orbit1 { 0%,100% { transform: translateY(-12px); } 50% { transform: translateY(12px); } }
@keyframes orbit2 { 0%,100% { transform: translate(-50%, -16px); } 50% { transform: translate(-50%, 8px); } }
@keyframes orbit3 { 0%,100% { transform: translateY(10px); } 50% { transform: translateY(-14px); } }
.nf-title {
  font-size: 22px;
  font-weight: 600;
  color: var(--text);
  font-family: var(--font-display);
  letter-spacing: -0.015em;
  margin: 0 0 10px;
}
.nf-desc {
  font-size: 13.5px;
  color: var(--text-dim);
  line-height: 1.7;
  margin: 0 0 24px;
  max-width: 380px;
  margin-left: auto;
  margin-right: auto;
}
.nf-actions {
  display: flex; justify-content: center; gap: 10px;
}
.nf-primary, .nf-secondary {
  display: inline-flex; align-items: center; gap: 8px;
  border-radius: 8px;
  padding: 9px 18px;
  min-height: 36px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}
.nf-primary {
  background: linear-gradient(135deg, rgba(125, 146, 232, 0.95) 0%, rgba(95, 110, 200, 0.95) 100%);
  color: #fff;
  border: 1px solid rgba(163, 180, 255, 0.4);
  box-shadow: 0 4px 20px rgba(125, 146, 232, 0.32);
}
.nf-primary:hover { filter: brightness(1.1); transform: translateY(-1px); box-shadow: 0 6px 26px rgba(125, 146, 232, 0.42); }
.nf-secondary {
  background: transparent;
  color: var(--text-dim);
  border: 1px solid var(--border);
}
.nf-secondary:hover { color: var(--text); border-color: var(--border-bright); background: rgba(125, 146, 232, 0.05); }
</style>
