<template>
  <div class="landing-page">
    <header class="landing-nav" aria-label="站点导航">
      <a class="landing-brand" href="/" aria-label="Diana QQ Bot 首页">
        <span class="landing-brand-mark">
          <BotMessageSquare :size="20" aria-hidden="true" />
        </span>
        <span>
          <strong>DIANA QQ BOT</strong>
          <small>LLM Bot Control Plane</small>
        </span>
      </a>
      <nav class="landing-nav-links" aria-label="页面导航">
        <a href="#features">能力</a>
        <a href="#workflow">流程</a>
        <a href="#deploy">部署</a>
      </nav>
      <a class="landing-nav-action" href="/console">
        <span>进入控制台</span>
        <ArrowRight :size="16" aria-hidden="true" />
      </a>
    </header>

    <main>
      <section class="landing-hero" aria-labelledby="landing-title">
        <div class="landing-hero-media" aria-hidden="true" />
        <div class="landing-hero-overlay" />
        <div class="landing-hero-content">
          <p class="landing-eyebrow">NapCat / OneBot v11 + LLM 配置中枢</p>
          <h1 id="landing-title">把 QQ 机器人、模型配置和插件运行统一管起来</h1>
          <p class="landing-hero-copy">
            Diana QQ Bot 面向自托管场景，提供 LLM Provider 管理、模型连通测试、QQ 机器人配置、插件开关、日志追踪和系统更新入口。
          </p>
          <div class="landing-hero-actions">
            <a class="landing-button primary" href="/console">
              <span>打开控制台</span>
              <ArrowRight :size="17" aria-hidden="true" />
            </a>
            <a class="landing-button" href="#deploy">
              <span>查看部署方式</span>
              <TerminalSquare :size="17" aria-hidden="true" />
            </a>
          </div>
          <dl class="landing-hero-metrics" aria-label="核心能力">
            <div>
              <dt>LLM</dt>
              <dd>多配置切换</dd>
            </div>
            <div>
              <dt>OneBot</dt>
              <dd>反向 WS 接入</dd>
            </div>
            <div>
              <dt>Logs</dt>
              <dd>操作与错误追踪</dd>
            </div>
          </dl>
        </div>
      </section>

      <section id="features" class="landing-section" aria-labelledby="features-title">
        <div class="landing-section-head">
          <p class="landing-eyebrow">Core Features</p>
          <h2 id="features-title">适合日常运维的机器人控制面</h2>
        </div>
        <div class="landing-feature-grid">
          <article v-for="feature in features" :key="feature.title" class="landing-feature-card">
            <span class="landing-feature-icon" :class="feature.tone">
              <component :is="feature.icon" :size="20" aria-hidden="true" />
            </span>
            <h3>{{ feature.title }}</h3>
            <p>{{ feature.copy }}</p>
          </article>
        </div>
      </section>

      <section id="workflow" class="landing-band" aria-labelledby="workflow-title">
        <div class="landing-section-head">
          <p class="landing-eyebrow">Workflow</p>
          <h2 id="workflow-title">从模型到 QQ，一条路径完成验证</h2>
        </div>
        <ol class="landing-steps">
          <li v-for="step in steps" :key="step.title">
            <span>{{ step.no }}</span>
            <div>
              <h3>{{ step.title }}</h3>
              <p>{{ step.copy }}</p>
            </div>
          </li>
        </ol>
      </section>

      <section id="deploy" class="landing-section landing-deploy" aria-labelledby="deploy-title">
        <div class="landing-section-head">
          <p class="landing-eyebrow">Deploy</p>
          <h2 id="deploy-title">本地、服务器和容器都能跑</h2>
        </div>
        <div class="landing-deploy-grid">
          <article class="landing-command-card">
            <div>
              <h3>源码开发</h3>
              <p>适合本机调试 WebUI、NapCat 集成和插件开发。</p>
            </div>
            <pre><code>make dev</code></pre>
          </article>
          <article class="landing-command-card">
            <div>
              <h3>生产构建</h3>
              <p>构建前端静态资源和 Go 后端二进制。</p>
            </div>
            <pre><code>make build</code></pre>
          </article>
          <article class="landing-command-card">
            <div>
              <h3>Docker</h3>
              <p>适合服务器部署，把数据和日志挂载到宿主机。</p>
            </div>
            <pre><code>docker compose up -d --build</code></pre>
          </article>
        </div>
      </section>
    </main>

    <footer class="landing-footer">
      <span>DIANA QQ BOT</span>
      <a href="/console">进入控制台</a>
    </footer>
  </div>
</template>

<script setup lang="ts">
import {
  Activity,
  ArrowRight,
  BotMessageSquare,
  Cable,
  FileClock,
  PlugZap,
  Settings2,
  Sparkles,
  TerminalSquare,
  Wrench
} from "@lucide/vue";

const features = [
  {
    title: "LLM 配置管理",
    copy: "保存多套 Provider、Base URL、API Key 和模型配置，按场景快速切换。",
    icon: Sparkles,
    tone: "rose"
  },
  {
    title: "模型连通测试",
    copy: "在真正接入 QQ 前先验证模型响应、延迟和返回内容，减少线上试错。",
    icon: Activity,
    tone: "green"
  },
  {
    title: "NapCat / OneBot",
    copy: "通过 OneBot v11 反向 WebSocket 连接 QQ 机器人，并集中管理身份和群策略。",
    icon: Cable,
    tone: "blue"
  },
  {
    title: "插件开关",
    copy: "内置插件与后续扩展可以在 WebUI 中启停，运行状态一眼可见。",
    icon: PlugZap,
    tone: "amber"
  },
  {
    title: "日志中心",
    copy: "把操作日志、错误日志和执行上下文放在同一个界面，排查更省心。",
    icon: FileClock,
    tone: "violet"
  },
  {
    title: "系统维护",
    copy: "控制台内置主题配置和版本更新面板，适合长期自托管维护。",
    icon: Settings2,
    tone: "slate"
  }
];

const steps = [
  {
    no: "01",
    title: "配置模型",
    copy: "添加 OpenAI-compatible、Gemini 或 Anthropic 配置，选择真实可用模型。"
  },
  {
    no: "02",
    title: "连接机器人",
    copy: "填写 OneBot 反向 WebSocket、机器人 QQ、主人 QQ 和群触发策略。"
  },
  {
    no: "03",
    title: "测试并启用",
    copy: "完成连通测试后启动机器人，再通过日志和插件页持续观察运行状态。"
  }
];
</script>

<style scoped>
:global(body) {
  margin: 0;
  min-width: 320px;
  background: #fff8fb;
}

.landing-page {
  min-height: 100vh;
  background: #fff8fb;
  color: #211821;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  letter-spacing: 0;
}

.landing-nav {
  position: sticky;
  top: 0;
  z-index: 20;
  min-height: 76px;
  padding: 14px clamp(18px, 4vw, 56px);
  border-bottom: 1px solid #f1dbe6;
  background: rgba(255, 248, 251, 0.92);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  backdrop-filter: blur(18px);
}

.landing-brand,
.landing-nav-action,
.landing-button,
.landing-footer a {
  color: inherit;
  text-decoration: none;
}

.landing-brand {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.landing-brand-mark {
  width: 42px;
  height: 42px;
  border: 1px solid #ffd1df;
  border-radius: 14px;
  background: #fff0f6;
  color: #ff5f94;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
}

.landing-brand strong,
.landing-brand small {
  display: block;
}

.landing-brand strong {
  font-size: 14px;
  font-weight: 860;
}

.landing-brand small {
  margin-top: 2px;
  color: #7d6f7c;
  font-size: 11px;
  font-weight: 700;
}

.landing-nav-links {
  display: flex;
  align-items: center;
  gap: 22px;
}

.landing-nav-links a {
  color: #675b66;
  text-decoration: none;
  font-size: 13px;
  font-weight: 760;
}

.landing-nav-links a:hover {
  color: #ff5f94;
}

.landing-nav-action {
  min-height: 42px;
  border: 1px solid #ffd1df;
  border-radius: 999px;
  padding: 0 16px;
  background: #ffffff;
  color: #ff5f94;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 800;
  white-space: nowrap;
}

.landing-hero {
  position: relative;
  min-height: min(760px, 78svh);
  display: grid;
  align-items: center;
  overflow: hidden;
}

.landing-hero-media,
.landing-hero-overlay {
  position: absolute;
  inset: 0;
}

.landing-hero-media {
  background-color: #1f1721;
  background-image: url("/landing-console-preview.png");
  background-size: cover;
  background-position: center top;
  transform: scale(1.02);
}

.landing-hero-overlay {
  background: rgba(29, 20, 28, 0.62);
}

.landing-hero-content {
  position: relative;
  z-index: 1;
  width: min(940px, calc(100% - 36px));
  margin-left: clamp(18px, 6vw, 88px);
  padding: 72px 0;
  color: #ffffff;
  display: grid;
  gap: 22px;
}

.landing-eyebrow {
  margin: 0;
  color: #ff8eb4;
  font-size: 12px;
  font-weight: 860;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.landing-hero h1 {
  margin: 0;
  max-width: 900px;
  font-size: clamp(38px, 5.2vw, 64px);
  line-height: 1.04;
  font-weight: 900;
  letter-spacing: 0;
}

.landing-hero-copy {
  margin: 0;
  max-width: 680px;
  color: rgba(255, 255, 255, 0.82);
  font-size: clamp(16px, 1.6vw, 20px);
  line-height: 1.8;
}

.landing-hero-actions,
.landing-hero-metrics {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.landing-button {
  min-height: 48px;
  border: 1px solid rgba(255, 255, 255, 0.32);
  border-radius: 999px;
  padding: 0 18px;
  background: rgba(255, 255, 255, 0.12);
  color: #ffffff;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  font-weight: 820;
}

.landing-button.primary {
  border-color: #ff6f9d;
  background: #ff6f9d;
}

.landing-hero-metrics {
  margin: 6px 0 0;
  padding: 0;
}

.landing-hero-metrics div {
  min-width: 142px;
  border-left: 2px solid #ff8eb4;
  padding-left: 14px;
}

.landing-hero-metrics dt,
.landing-hero-metrics dd {
  margin: 0;
}

.landing-hero-metrics dt {
  font-size: 20px;
  font-weight: 900;
}

.landing-hero-metrics dd {
  margin-top: 3px;
  color: rgba(255, 255, 255, 0.76);
  font-size: 13px;
  font-weight: 700;
}

.landing-section,
.landing-band {
  padding: 78px clamp(18px, 5vw, 72px);
}

.landing-section-head {
  width: min(760px, 100%);
  display: grid;
  gap: 10px;
}

.landing-section-head h2 {
  margin: 0;
  color: #201720;
  font-size: clamp(28px, 3vw, 44px);
  line-height: 1.16;
  font-weight: 900;
}

.landing-feature-grid {
  margin-top: 30px;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.landing-feature-card,
.landing-command-card {
  border: 1px solid #f1dbe6;
  border-radius: 8px;
  background: #ffffff;
  box-shadow: 0 16px 36px rgba(255, 111, 157, 0.1);
}

.landing-feature-card {
  min-height: 220px;
  padding: 22px;
  display: grid;
  align-content: start;
  gap: 14px;
}

.landing-feature-icon {
  width: 44px;
  height: 44px;
  border-radius: 14px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.landing-feature-icon.rose {
  background: #fff0f6;
  color: #ff5f94;
}

.landing-feature-icon.green {
  background: #eefaf3;
  color: #3f9c69;
}

.landing-feature-icon.blue {
  background: #eef5ff;
  color: #4f8ef7;
}

.landing-feature-icon.amber {
  background: #fff6df;
  color: #c78a18;
}

.landing-feature-icon.violet {
  background: #f4efff;
  color: #8a67ef;
}

.landing-feature-icon.slate {
  background: #f1f3f8;
  color: #798397;
}

.landing-feature-card h3,
.landing-command-card h3,
.landing-steps h3 {
  margin: 0;
  color: #211821;
  font-size: 18px;
  font-weight: 860;
}

.landing-feature-card p,
.landing-command-card p,
.landing-steps p {
  margin: 0;
  color: #756977;
  line-height: 1.7;
}

.landing-band {
  border-top: 1px solid #f1dbe6;
  border-bottom: 1px solid #f1dbe6;
  background: #fff1f6;
}

.landing-steps {
  margin: 30px 0 0;
  padding: 0;
  list-style: none;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.landing-steps li {
  border: 1px solid #f1dbe6;
  border-radius: 8px;
  background: #ffffff;
  padding: 22px;
  display: grid;
  gap: 18px;
}

.landing-steps li > span {
  width: 48px;
  height: 48px;
  border-radius: 16px;
  background: #ff6f9d;
  color: #ffffff;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-weight: 900;
}

.landing-steps li > div {
  display: grid;
  gap: 10px;
}

.landing-deploy-grid {
  margin-top: 30px;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.landing-command-card {
  padding: 22px;
  display: grid;
  gap: 18px;
}

.landing-command-card > div {
  display: grid;
  gap: 8px;
}

.landing-command-card pre {
  margin: 0;
  border: 1px solid #ead6e1;
  border-radius: 8px;
  background: #211821;
  color: #f8edf3;
  padding: 14px;
  overflow-x: auto;
}

.landing-command-card code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 13px;
}

.landing-footer {
  min-height: 72px;
  padding: 0 clamp(18px, 5vw, 72px);
  border-top: 1px solid #f1dbe6;
  color: #756977;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.landing-footer span,
.landing-footer a {
  font-size: 13px;
  font-weight: 800;
}

.landing-footer a {
  color: #ff5f94;
}

@media (prefers-color-scheme: dark) {
  :global(body) {
    background: #171017;
  }

  .landing-page {
    background: #171017;
    color: #f7edf3;
  }

  .landing-nav {
    border-bottom-color: #3c2e3b;
    background: rgba(23, 16, 23, 0.92);
  }

  .landing-brand small,
  .landing-nav-links a,
  .landing-feature-card p,
  .landing-command-card p,
  .landing-steps p,
  .landing-footer {
    color: #b9aab5;
  }

  .landing-nav-action,
  .landing-feature-card,
  .landing-steps li,
  .landing-command-card {
    border-color: #3c2e3b;
    background: #211821;
    box-shadow: 0 16px 36px rgba(0, 0, 0, 0.22);
  }

  .landing-section-head h2,
  .landing-feature-card h3,
  .landing-command-card h3,
  .landing-steps h3 {
    color: #f7edf3;
  }

  .landing-band {
    border-color: #3c2e3b;
    background: #1d141d;
  }

  .landing-footer {
    border-top-color: #3c2e3b;
  }
}

@media (max-width: 980px) {
  .landing-nav-links {
    display: none;
  }

  .landing-feature-grid,
  .landing-steps,
  .landing-deploy-grid {
    grid-template-columns: 1fr;
  }

  .landing-hero {
    min-height: 720px;
  }

  .landing-hero-content {
    margin: 0 auto;
  }
}

@media (max-width: 620px) {
  .landing-nav {
    align-items: flex-start;
    flex-direction: column;
  }

  .landing-nav-action {
    width: 100%;
    justify-content: center;
  }

  .landing-hero {
    min-height: 680px;
  }

  .landing-hero-content {
    padding: 48px 0;
  }

  .landing-hero-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .landing-button {
    width: 100%;
  }

  .landing-hero-metrics div {
    min-width: min(100%, 220px);
  }

  .landing-section,
  .landing-band {
    padding-top: 58px;
    padding-bottom: 58px;
  }
}
</style>
