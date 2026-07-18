import { createApp, type Component } from "vue";
import { ElButton, ElDrawer, ElInput, ElOption, ElSelect, ElSwitch, ElTabPane, ElTabs } from "element-plus";
import App from "./App.vue";
import AdminLogin from "./AdminLogin.vue";
import LandingPage from "./LandingPage.vue";
import { getAdminAuthStatus, rememberedAdminLoginPath } from "./api";
import "element-plus/es/components/button/style/css";
import "element-plus/es/components/drawer/style/css";
import "element-plus/es/components/input/style/css";
import "element-plus/es/components/select/style/css";
import "element-plus/es/components/switch/style/css";
import "element-plus/es/components/tabs/style/css";
import "element-plus/es/components/tab-pane/style/css";
import "element-plus/theme-chalk/dark/css-vars.css";
import "./styles.css";

const consolePaths = new Set(["/console", "/admin", "/webui", "/llm", "/test", "/qqbot", "/groups", "/plugins", "/web-search", "/logs", "/security", "/theme"]);
const currentPath = window.location.pathname.replace(/\/+$/, "") || "/";

function mount(component: Component): void {
  const app = createApp(component);
  for (const plugin of [ElButton, ElDrawer, ElInput, ElOption, ElSelect, ElSwitch, ElTabPane, ElTabs]) {
    app.use(plugin);
  }
  app.mount("#app");
}

async function bootstrap(): Promise<void> {
  try {
    const auth = await getAdminAuthStatus(currentPath);
    if (auth.login_page && auth.configured && !auth.authenticated) {
      mount(AdminLogin);
      return;
    }
    if (currentPath === "/") {
      if (auth.configured && !auth.authenticated) {
        mount(LandingPage);
        return;
      }
      mount(App);
      return;
    }
    if (consolePaths.has(currentPath)) {
      if (auth.configured && !auth.authenticated) {
        window.location.replace(rememberedAdminLoginPath() || "/");
        return;
      }
      mount(App);
      return;
    }
    if (auth.login_page && auth.authenticated) {
      window.location.replace("/console");
      return;
    }
  } catch {
    if (consolePaths.has(currentPath)) {
      mount(App);
      return;
    }
  }

  mount(LandingPage);
}

void bootstrap();
