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
            <h2 id="admin-login-title">{{ setupRequired ? "设置管理员" : "管理员登录" }}</h2>
            <p>{{ setupRequired ? "首次使用，请设置邮箱和密码" : "输入邮箱和密码" }}</p>
          </div>
        </div>

        <label class="admin-token-field">
          <span>邮箱</span>
          <div class="admin-token-input">
            <input
              v-model.trim="email"
              type="email"
              autocomplete="username"
              autofocus
              required
              aria-describedby="admin-login-error"
            />
          </div>
        </label>

        <label class="admin-token-field">
          <span>密码</span>
          <div class="admin-token-input">
            <input
              v-model="password"
              :type="showPassword ? 'text' : 'password'"
              :autocomplete="setupRequired ? 'new-password' : 'current-password'"
              :minlength="setupRequired ? 12 : undefined"
              required
              aria-describedby="admin-login-error"
            />
          </div>
        </label>

        <label v-if="setupRequired" class="admin-token-field">
          <span>确认密码</span>
          <div class="admin-token-input">
            <input
              v-model="passwordConfirm"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="new-password"
              required
              aria-describedby="admin-login-error"
            />
            <button type="button" :aria-label="showPassword ? '隐藏密码' : '显示密码'" :title="showPassword ? '隐藏密码' : '显示密码'" @click="showPassword = !showPassword">
              <EyeOff v-if="showPassword" :size="17" aria-hidden="true" />
              <Eye v-else :size="17" aria-hidden="true" />
            </button>
          </div>
        </label>

        <p v-if="error" id="admin-login-error" class="admin-login-error" role="alert">{{ error }}</p>

        <button class="admin-login-submit" type="submit" :disabled="submitDisabled">
          <LoaderCircle v-if="submitting" class="spin" :size="18" aria-hidden="true" />
          <LogIn v-else :size="18" aria-hidden="true" />
          <span>{{ submitting ? (setupRequired ? "创建中" : "验证中") : (setupRequired ? "创建并进入后台" : "进入后台") }}</span>
        </button>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { BotMessageSquare, Eye, EyeOff, KeyRound, LoaderCircle, LogIn, ShieldCheck } from "@lucide/vue";
import { getAdminAuthStatus, loginAdmin, rememberAdminLoginPath, setupAdmin } from "./api";

const email = ref("");
const password = ref("");
const passwordConfirm = ref("");
const showPassword = ref(false);
const submitting = ref(false);
const setupRequired = ref(false);
const error = ref("");
const loginPath = window.location.pathname.replace(/\/+$/, "") || "/";
const submitDisabled = computed(() => {
  if (submitting.value || !email.value.trim() || !password.value) return true;
  return setupRequired.value && (!passwordConfirm.value || password.value !== passwordConfirm.value);
});

onMounted(async () => {
  try {
    const status = await getAdminAuthStatus(loginPath);
    if (!status.login_page) {
      window.location.replace("/");
      return;
    }
    setupRequired.value = status.setup_required;
    if (!status.configured || status.authenticated) {
      window.location.replace("/console");
    }
  } catch {
    error.value = "暂时无法连接管理服务";
  }
});

async function submit(): Promise<void> {
  if (submitDisabled.value) return;
  submitting.value = true;
  error.value = "";
  try {
    if (setupRequired.value) {
      await setupAdmin(email.value.trim(), password.value, passwordConfirm.value, loginPath);
    } else {
      await loginAdmin(email.value.trim(), password.value, loginPath);
    }
    rememberAdminLoginPath(loginPath);
    window.location.replace("/console");
  } catch (err) {
    error.value = err instanceof Error ? err.message : "登录失败";
  } finally {
    submitting.value = false;
  }
}
</script>
