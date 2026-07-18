<template>
  <main class="admin-login-page">
    <section class="admin-login-brand" aria-label="Diana 管理后台">
      <div class="admin-login-mark">
        <BotMessageSquare :size="28" aria-hidden="true" />
      </div>
      <div>
        <p class="admin-login-kicker">DIANA QQ BOT</p>
        <h1>管理后台</h1>
      </div>
      <div class="admin-login-status">
        <ShieldCheck :size="17" aria-hidden="true" />
        <span>受保护的管理入口</span>
      </div>
    </section>

    <section class="admin-login-panel" aria-labelledby="admin-login-title">
      <form class="admin-login-form" @submit.prevent="submit">
        <div class="admin-login-heading">
          <div class="admin-login-key"><KeyRound :size="19" aria-hidden="true" /></div>
          <div>
            <h2 id="admin-login-title">管理员登录</h2>
            <p>输入管理 Token</p>
          </div>
        </div>

        <label class="admin-token-field">
          <span>Token</span>
          <div class="admin-token-input">
            <input
              v-model="token"
              :type="showToken ? 'text' : 'password'"
              autocomplete="current-password"
              autofocus
              required
              aria-describedby="admin-login-error"
            />
            <button type="button" :aria-label="showToken ? '隐藏 Token' : '显示 Token'" :title="showToken ? '隐藏 Token' : '显示 Token'" @click="showToken = !showToken">
              <EyeOff v-if="showToken" :size="17" aria-hidden="true" />
              <Eye v-else :size="17" aria-hidden="true" />
            </button>
          </div>
        </label>

        <p v-if="error" id="admin-login-error" class="admin-login-error" role="alert">{{ error }}</p>

        <button class="admin-login-submit" type="submit" :disabled="submitting || !token.trim()">
          <LoaderCircle v-if="submitting" class="spin" :size="18" aria-hidden="true" />
          <LogIn v-else :size="18" aria-hidden="true" />
          <span>{{ submitting ? "验证中" : "进入后台" }}</span>
        </button>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import { BotMessageSquare, Eye, EyeOff, KeyRound, LoaderCircle, LogIn, ShieldCheck } from "@lucide/vue";
import { getAdminAuthStatus, loginAdmin, rememberAdminLoginPath } from "./api";

const token = ref("");
const showToken = ref(false);
const submitting = ref(false);
const error = ref("");
const loginPath = window.location.pathname.replace(/\/+$/, "") || "/";

onMounted(async () => {
  try {
    const status = await getAdminAuthStatus(loginPath);
    if (!status.login_page) {
      window.location.replace("/");
      return;
    }
    if (!status.configured || status.authenticated) {
      window.location.replace("/console");
    }
  } catch {
    error.value = "暂时无法连接管理服务";
  }
});

async function submit(): Promise<void> {
  if (submitting.value || !token.value.trim()) return;
  submitting.value = true;
  error.value = "";
  try {
    await loginAdmin(token.value.trim(), loginPath);
    rememberAdminLoginPath(loginPath);
    window.location.replace("/console");
  } catch (err) {
    error.value = err instanceof Error ? err.message : "登录失败";
  } finally {
    submitting.value = false;
  }
}
</script>
