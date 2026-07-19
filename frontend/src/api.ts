export type Provider = "openai_compatible" | "gemini" | "anthropic";
export type APIFormat = "responses" | "chat_completions";

export interface LLMConfig {
  id?: string;
  name?: string;
  group?: string;
  description?: string;
  updated_at?: string;
  active_profile_id?: string;
  profiles?: LLMConfig[];
  provider: Provider;
  api_key?: string;
  api_key_configured?: boolean;
  base_url?: string;
  api_format?: APIFormat;
  model: string;
  image_model?: string;
  image_base_url?: string;
  image_origin?: string;
  image_timeout_ms?: number;
  user_agent?: string;
  headers?: Record<string, string>;
  temperature?: number | null;
  reasoning_effort?: string;
  context_window_tokens?: number;
  max_context_tokens?: number;
  max_output_tokens?: number;
  timeout_ms?: number;
}

export interface GenerateResponse {
  provider: Provider;
  model?: string;
  text: string;
  usage?: {
    input_tokens?: number;
    output_tokens?: number;
    total_tokens?: number;
  };
}

export interface LLMModelInfo {
  id: string;
  name?: string;
  object?: string;
  owned_by?: string;
  created?: number;
  context_window_tokens?: number;
  max_input_tokens?: number;
  max_output_tokens?: number;
}

export interface LLMModelsResponse {
  models: LLMModelInfo[];
}

export type WebSearchProviderType = "exa_mcp" | "tavily";

export interface WebSearchProviderConfig {
  name: string;
  type: WebSearchProviderType;
  url: string;
  tool?: string;
  api_key_env?: string;
  api_key_configured?: boolean;
  timeout_ms?: number;
  max_results?: number;
  disabled?: boolean;
}

export interface WebSearchConfig {
  providers: WebSearchProviderConfig[];
  config_path?: string;
  overridden_by_env?: boolean;
}

export interface WebSearchTestResponse {
  provider: string;
  duration_ms: number;
  content: string;
}

export interface QQBotConfig {
  id?: string;
  name?: string;
  platform?: string;
  avatar_url?: string;
  active_profile_id?: string;
  profiles?: QQBotConfig[];
  enabled: boolean;
  onebot_reverse_ws_endpoint: string;
  onebot_access_token?: string;
  onebot_access_token_configured?: boolean;
  nonebot_bridge_enabled?: boolean;
  nonebot_bridge_endpoint?: string;
  nonebot_bridge_token?: string;
  nonebot_bridge_token_configured?: boolean;
  bot_qq?: string;
  owner_id?: string;
  group_triggers?: string[];
  disabled_groups?: string[];
  disabled_users?: string[];
  welcome_enabled?: boolean;
  welcome_message?: string;
  system_prompt?: string;
  passive_reply_router_prompt?: string;
  passive_reply_prompt?: string;
  max_input_chars?: number;
  max_reply_chars?: number;
  direct_reply_chunk_size?: number;
  forward_reply_threshold?: number;
  recall_reply_mode?: "llm_summary" | "original_forward";
  recall_reply_auto_delete_enabled?: boolean;
  llm_qq_id_masking_enabled?: boolean;
  recent_context_limit?: number;
  context_summary_threshold?: number;
  passive_reply_chance?: number;
  passive_reply_threshold?: number;
  reply_rules?: ReplyRule[];
  max_bot_concurrency?: number;
  request_timeout_ms?: number;
  agent_enabled?: boolean;
  agent_work_dir?: string;
  agent_max_steps?: number;
  agent_skill_roots?: string[];
  agent_mcp_config_path?: string;
  agent_command_allowlist?: string[];
  agent_command_timeout_ms?: number;
  agent_browser_cdp_url?: string;
  agent_browser_timeout_ms?: number;
}

export type ReplyRuleAction = "model" | "voice";

export interface ReplyRule {
  id?: string;
  name?: string;
  enabled: boolean;
  prompt?: string;
  action?: ReplyRuleAction;
  llm_profile_id?: string;
}

export interface QQBotTask {
  id: string;
  kind: string;
  owner_id?: string;
  group_id?: string;
  user_id?: string;
  message?: string;
  status?: string;
  trigger_at?: string;
  interval_seconds?: number;
  last_run_at?: string;
  cancelled_at?: string;
  last_error?: string;
  consecutive_failures?: number;
  pending_delivery?: boolean;
  pending_since?: string;
  created_at?: string;
}

export interface QQBotTasksResponse {
  items: QQBotTask[];
}

export interface QQBotAutoGroupInfo {
  group_id: string;
  group_name?: string;
  member_count?: number;
  max_member_count?: number;
}

export interface QQBotAutoInfo {
  bot_qq?: string;
  nickname?: string;
  avatar_url?: string;
  groups?: QQBotAutoGroupInfo[];
  recent_group_id?: string;
  recent_user_id?: string;
}

export interface QQBotDashboardBucket {
  hour: string;
  messages: number;
  replies: number;
  searches: number;
  images: number;
}

export interface QQBotDashboardMeasure {
  label: string;
  value: number;
}

export interface QQBotDashboardStats {
  since?: string;
  until?: string;
  received_messages: number;
  active_members: number;
  replied_messages: number;
  text_replies: number;
  image_generations: number;
  image_edits: number;
  search_calls: number;
  api_calls: number;
  llm_calls: number;
  llm_input_tokens: number;
  llm_output_tokens: number;
  llm_total_tokens: number;
  server?: QQBotDashboardServerStats;
  hourly: QQBotDashboardBucket[];
  operation_breakdown: QQBotDashboardMeasure[];
}

export interface QQBotDashboardServerStats {
  collected_at?: string;
  hostname?: string;
  os: string;
  arch: string;
  process_id: number;
  process_uptime_seconds?: number;
  cpu_model?: string;
  cpu_cores: number;
  cpu_usage_percent?: number;
  process_cpu_percent?: number;
  memory_total_bytes?: number;
  memory_used_bytes?: number;
  memory_usage_percent?: number;
  process_memory_bytes?: number;
  go_heap_alloc_bytes?: number;
  go_heap_system_bytes?: number;
  go_routines: number;
  runtime_version?: string;
  metrics_unavailable_reason?: string;
  process_metrics_unavailable?: string;
}

export interface PluginManifest {
  id: string;
  name: string;
  version: string;
  description: string;
  official: boolean;
  built_in: boolean;
  permissions?: string[];
}

export interface PluginState {
  manifest: PluginManifest;
  installed: boolean;
  enabled: boolean;
}

export interface QQBotGroupConfig {
  group_id: string;
  enabled: boolean;
  enabled_set?: boolean;
  group_triggers?: string[];
  welcome_enabled?: boolean;
  welcome_message?: string;
  recent_context_limit?: number;
  max_reply_chars?: number;
  passive_reply_chance?: number;
  passive_reply_threshold?: number;
  minimum_reply_member_level?: number;
  plugin_overrides?: Record<string, boolean>;
  updated_at?: string;
}

export interface QQBotGroupAdminChallengeResponse {
  group_id: string;
  user_id: string;
  expires_at: string;
  message: string;
}

export interface QQBotGroupAdminConfigResponse {
  group_id: string;
  user_id?: string;
  token?: string;
  expires_at?: string;
  config: QQBotGroupConfig;
  plugins: PluginState[];
}

export interface UpdateStatus {
  root: string;
  branch?: string;
  remote_name?: string;
  remote_url?: string;
  head_commit?: string;
  head_subject?: string;
  running_commit?: string;
  dirty: boolean;
  ahead?: number;
  behind?: number;
  upstream?: string;
  updating: boolean;
  apply_supported: boolean;
  update_available: boolean;
  restart_required: boolean;
  last_fetched_at?: string;
  last_update_at?: string;
  last_update_text?: string;
}

export interface UpdateResult {
  status: UpdateStatus;
  fetched: boolean;
  updated: boolean;
  source_updated: boolean;
  applied: boolean;
  restart_required: boolean;
  previous_commit?: string;
  target_commit?: string;
  output?: string;
  at: string;
}

export type AppLogKind = "operation" | "error";
export type AppLogLevel = "info" | "error";

export interface AppLogEntry {
  id: string;
  kind: AppLogKind;
  level: AppLogLevel;
  action: string;
  message: string;
  detail?: string;
  actor?: string;
  target?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface AppLogsResponse {
  logs: AppLogEntry[];
}

export interface QQBotStatus {
  running: boolean;
  config: QQBotConfig;
  channel: {
    connected: boolean;
    endpoint: string;
    self_id?: string;
    last_error?: string;
    updated_at: string;
  };
  nonebot_bridge: {
    enabled: boolean;
    connected: boolean;
    endpoint?: string;
    last_error?: string;
    updated_at: string;
  };
  plugins: PluginState[];
  recent_events?: Array<{
    at: string;
    kind: string;
    user_id?: string;
    group_id?: string;
    text?: string;
    reply?: string;
    error?: string;
    handled: boolean;
    duration_ms?: number;
  }>;
  active_workers: number;
  active_subagent_tasks: number;
  subagent_tasks?: Array<{
    id: string;
    kind: string;
    name: string;
    phase: string;
    completed?: number;
    total?: number;
    started_at: string;
    updated_at: string;
  }>;
  pending_events: number;
  last_error?: string;
  updated_at: string;
}

export interface QQGroupTestResponse {
  group_id: string;
  message?: string;
  message_id?: string;
  sent: boolean;
  send_result?: Record<string, unknown>;
  channel: QQBotStatus["channel"];
  recent_events?: NonNullable<QQBotStatus["recent_events"]>;
  status: QQBotStatus;
}

export interface QQBotFeatureFlags {
  group_test: boolean;
}

export interface NapCatLoginAccount {
  uin: string;
  nickname?: string;
  avatar_url?: string;
  online?: boolean;
}

export interface NapCatLoginStatus {
  configured: boolean;
  is_login: boolean;
  is_offline: boolean;
  qrcode_available: boolean;
  login_error?: string;
  account?: NapCatLoginAccount | null;
  quick_accounts?: NapCatLoginAccount[];
}

export interface AdminAuthStatus {
  configured: boolean;
  setup_required: boolean;
  authenticated: boolean;
  login_page: boolean;
  login_path: string;
  email?: string;
  username?: string;
}

export interface AdminAccessSettings {
  configured: boolean;
  username?: string;
  random_suffix_enabled: boolean;
  login_path: string;
  managed_by_environment: boolean;
}

export interface AdminAuthSession {
  id: string;
  device_name: string;
  user_agent?: string;
  ip_address?: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  current: boolean;
}

export interface AdminAuthSessionsResponse {
  sessions: AdminAuthSession[];
}

export interface AdminAuthResult {
  authenticated: boolean;
  access_expires_at?: string;
  refresh_expires_at?: string;
}

const adminLoginPathStorageKey = "diana_admin_login_path";
let adminRefreshPromise: Promise<boolean> | null = null;

async function requestJSON<T>(url: string, init?: RequestInit, allowRefresh = true): Promise<T> {
  const response = await fetch(url, {
    ...init,
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {})
    }
  });
  const data = (await response.json().catch(() => ({}))) as T & { error?: string };
  if (!response.ok) {
    if (response.status === 401 && allowRefresh && !url.startsWith("/api/auth/")) {
      const refreshed = await refreshAdminSession();
      if (refreshed) {
        return requestJSON<T>(url, init, false);
      }
      window.location.replace(rememberedAdminLoginPath() || "/login");
    }
    throw new Error(data.error || `HTTP ${response.status}`);
  }
  return data;
}

async function refreshAdminSession(): Promise<boolean> {
  if (!adminRefreshPromise) {
    adminRefreshPromise = fetch("/api/auth/refresh", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" }
    })
      .then((response) => response.ok)
      .catch(() => false)
      .finally(() => {
        adminRefreshPromise = null;
      });
  }
  return adminRefreshPromise;
}

export function getAdminAuthStatus(path = window.location.pathname): Promise<AdminAuthStatus> {
  return requestJSON<AdminAuthStatus>(`/api/auth/status?path=${encodeURIComponent(path)}`);
}

export function loginAdmin(email: string, password: string, loginPath: string): Promise<AdminAuthResult> {
  return requestJSON("/api/auth/login", {
    method: "POST",
    headers: { "X-Diana-Login-Path": loginPath },
    body: JSON.stringify({ email, password })
  });
}

export function setupAdmin(email: string, password: string, passwordConfirm: string, loginPath: string): Promise<AdminAuthResult> {
  return requestJSON("/api/auth/setup", {
    method: "POST",
    headers: { "X-Diana-Login-Path": loginPath },
    body: JSON.stringify({ email, password, password_confirm: passwordConfirm })
  });
}

export function refreshAdmin(): Promise<AdminAuthResult> {
  return requestJSON("/api/auth/refresh", { method: "POST" }, false);
}

export function logoutAdmin(): Promise<{ authenticated: boolean }> {
  return requestJSON("/api/auth/logout", { method: "POST" });
}

export function rememberAdminLoginPath(path: string): void {
  window.sessionStorage.setItem(adminLoginPathStorageKey, path);
}

export function rememberedAdminLoginPath(): string {
  return window.sessionStorage.getItem(adminLoginPathStorageKey) || "";
}

export function getAdminAccessSettings(): Promise<AdminAccessSettings> {
  return requestJSON<AdminAccessSettings>("/api/auth/settings");
}

export function saveAdminAccessSettings(randomSuffixEnabled: boolean, regenerate = false): Promise<AdminAccessSettings> {
  return requestJSON<AdminAccessSettings>("/api/auth/settings", {
    method: "PUT",
    body: JSON.stringify({
      random_suffix_enabled: randomSuffixEnabled,
      regenerate
    })
  });
}

export function listAdminSessions(): Promise<AdminAuthSessionsResponse> {
  return requestJSON<AdminAuthSessionsResponse>("/api/auth/sessions");
}

export function revokeAdminSession(id: string): Promise<{ revoked: boolean; current: boolean }> {
  return requestJSON(`/api/auth/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function revokeOtherAdminSessions(): Promise<{ revoked: number }> {
  return requestJSON("/api/auth/sessions/revoke-others", { method: "POST" });
}

export function updateAdminEmail(email: string, currentPassword: string): Promise<{ email: string }> {
  return requestJSON("/api/auth/account", {
    method: "PUT",
    body: JSON.stringify({ email, current_password: currentPassword })
  });
}

export function changeAdminPassword(currentPassword: string, newPassword: string, passwordConfirm: string): Promise<{ changed: boolean; other_sessions_revoked: boolean }> {
  return requestJSON("/api/auth/password", {
    method: "PUT",
    body: JSON.stringify({
      current_password: currentPassword,
      new_password: newPassword,
      password_confirm: passwordConfirm
    })
  });
}

export function getConfig(includeSecrets = false): Promise<LLMConfig> {
  const suffix = includeSecrets ? "?include_secrets=true" : "";
  return requestJSON<LLMConfig>(`/api/llm/config${suffix}`);
}

export function exportConfig(): Promise<LLMConfig> {
  return requestJSON<LLMConfig>("/api/llm/config/export");
}

export function saveConfig(config: LLMConfig): Promise<LLMConfig> {
  return requestJSON<LLMConfig>("/api/llm/config", {
    method: "POST",
    body: JSON.stringify(config)
  });
}

export function activateConfigProfile(id: string): Promise<LLMConfig> {
  return requestJSON<LLMConfig>("/api/llm/config/activate", {
    method: "POST",
    body: JSON.stringify({ id })
  });
}

export function cloneConfigProfile(id: string): Promise<LLMConfig> {
  return requestJSON<LLMConfig>("/api/llm/config/clone", {
    method: "POST",
    body: JSON.stringify({ id })
  });
}

export function deleteConfigProfile(id: string): Promise<LLMConfig> {
  return requestJSON<LLMConfig>("/api/llm/config/delete", {
    method: "POST",
    body: JSON.stringify({ id })
  });
}

export function importConfigProfiles(payload: Pick<LLMConfig, "active_profile_id" | "profiles">): Promise<LLMConfig> {
  return requestJSON<LLMConfig>("/api/llm/config/import", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export function testLLM(message: string, config?: LLMConfig): Promise<GenerateResponse> {
  return requestJSON<GenerateResponse>("/api/llm/test", {
    method: "POST",
    body: JSON.stringify({ ...(config || {}), message })
  });
}

export function listLLMModels(config: LLMConfig): Promise<LLMModelsResponse> {
  return requestJSON<LLMModelsResponse>("/api/llm/models", {
    method: "POST",
    body: JSON.stringify(config)
  });
}

export function getWebSearchConfig(): Promise<WebSearchConfig> {
  return requestJSON<WebSearchConfig>("/api/web-search/config");
}

export function saveWebSearchConfig(config: WebSearchConfig): Promise<WebSearchConfig> {
  return requestJSON<WebSearchConfig>("/api/web-search/config", {
    method: "POST",
    body: JSON.stringify({ providers: config.providers })
  });
}

export function testWebSearchProvider(provider: WebSearchProviderConfig, query: string): Promise<WebSearchTestResponse> {
  return requestJSON<WebSearchTestResponse>("/api/web-search/test", {
    method: "POST",
    body: JSON.stringify({ provider, query })
  });
}

export function getQQBotConfig(): Promise<QQBotConfig> {
  return requestJSON<QQBotConfig>("/api/qqbot/config");
}

export function saveQQBotConfig(config: QQBotConfig): Promise<QQBotConfig> {
  return requestJSON<QQBotConfig>("/api/qqbot/config", {
    method: "POST",
    body: JSON.stringify(config)
  });
}

export function activateQQBotProfile(id: string): Promise<QQBotConfig> {
  return requestJSON<QQBotConfig>("/api/qqbot/config/activate", {
    method: "POST",
    body: JSON.stringify({ id })
  });
}

export function cloneQQBotProfile(id: string): Promise<QQBotConfig> {
  return requestJSON<QQBotConfig>("/api/qqbot/config/clone", {
    method: "POST",
    body: JSON.stringify({ id })
  });
}

export function deleteQQBotProfile(id: string): Promise<QQBotConfig> {
  return requestJSON<QQBotConfig>("/api/qqbot/config/delete", {
    method: "POST",
    body: JSON.stringify({ id })
  });
}

export function getQQBotStatus(): Promise<QQBotStatus> {
  return requestJSON<QQBotStatus>("/api/qqbot/status");
}

export function getQQBotAutoInfo(): Promise<QQBotAutoInfo> {
  return requestJSON<QQBotAutoInfo>("/api/qqbot/auto-info");
}

export function listQQBotTasks(): Promise<QQBotTasksResponse> {
  return requestJSON<QQBotTasksResponse>("/api/qqbot/tasks");
}

export function getQQBotDashboardStats(): Promise<QQBotDashboardStats> {
  return requestJSON<QQBotDashboardStats>("/api/qqbot/dashboard-stats");
}

export function startQQBot(): Promise<QQBotStatus> {
  return requestJSON<QQBotStatus>("/api/qqbot/start", { method: "POST" });
}

export function stopQQBot(): Promise<QQBotStatus> {
  return requestJSON<QQBotStatus>("/api/qqbot/stop", { method: "POST" });
}

export function getQQBotFeatures(): Promise<QQBotFeatureFlags> {
  return requestJSON<QQBotFeatureFlags>("/api/qqbot/features");
}

export function getNapCatLoginStatus(): Promise<NapCatLoginStatus> {
  return requestJSON<NapCatLoginStatus>("/api/napcat/login/status");
}

export function refreshNapCatLoginQRCode(): Promise<NapCatLoginStatus> {
  return requestJSON<NapCatLoginStatus>("/api/napcat/login/refresh", { method: "POST" });
}

export function quickLoginNapCat(uin: string): Promise<NapCatLoginStatus> {
  return requestJSON<NapCatLoginStatus>("/api/napcat/login/quick", {
    method: "POST",
    body: JSON.stringify({ uin })
  });
}

export function napCatLoginQRCodeURL(nonce: string | number = Date.now()): string {
  return `/api/napcat/login/qrcode?v=${encodeURIComponent(String(nonce))}`;
}

export function getQQGroupTest(groupID: string): Promise<QQGroupTestResponse> {
  const params = new URLSearchParams({ group_id: groupID });
  return requestJSON<QQGroupTestResponse>(`/api/qqbot/group-test?${params.toString()}`);
}

export function sendQQGroupTest(groupID: string, message: string): Promise<QQGroupTestResponse> {
  return requestJSON<QQGroupTestResponse>("/api/qqbot/group-test", {
    method: "POST",
    body: JSON.stringify({ group_id: groupID, message })
  });
}

export function listPlugins(): Promise<PluginState[]> {
  return requestJSON<PluginState[]>("/api/qqbot/plugins");
}

export function installPlugin(id: string): Promise<PluginState> {
  return requestJSON<PluginState>(`/api/qqbot/plugins/${encodeURIComponent(id)}/install`, { method: "POST" });
}

export function uninstallPlugin(id: string): Promise<PluginState> {
  return requestJSON<PluginState>(`/api/qqbot/plugins/${encodeURIComponent(id)}/uninstall`, { method: "POST" });
}

export function setPluginEnabled(id: string, enabled: boolean): Promise<PluginState> {
  return requestJSON<PluginState>(`/api/qqbot/plugins/${encodeURIComponent(id)}/enabled`, {
    method: "POST",
    body: JSON.stringify({ enabled })
  });
}

export function requestQQBotGroupAdminChallenge(groupID: string, userID: string): Promise<QQBotGroupAdminChallengeResponse> {
  return requestJSON<QQBotGroupAdminChallengeResponse>("/api/qqbot/group-admin/challenge", {
    method: "POST",
    body: JSON.stringify({ group_id: groupID, user_id: userID })
  });
}

export function verifyQQBotGroupAdmin(groupID: string, userID: string, code: string): Promise<QQBotGroupAdminConfigResponse> {
  return requestJSON<QQBotGroupAdminConfigResponse>("/api/qqbot/group-admin/verify", {
    method: "POST",
    body: JSON.stringify({ group_id: groupID, user_id: userID, code })
  });
}

export function getQQBotGroupAdminConfig(token: string): Promise<QQBotGroupAdminConfigResponse> {
  return requestJSON<QQBotGroupAdminConfigResponse>("/api/qqbot/group-admin/config", {
    headers: { "X-Diana-Group-Token": token }
  });
}

export function saveQQBotGroupAdminConfig(token: string, config: QQBotGroupConfig): Promise<QQBotGroupAdminConfigResponse> {
  return requestJSON<QQBotGroupAdminConfigResponse>("/api/qqbot/group-admin/config", {
    method: "POST",
    headers: { "X-Diana-Group-Token": token },
    body: JSON.stringify({ config })
  });
}

export function getUpdateStatus(refresh = false): Promise<UpdateStatus> {
  return requestJSON<UpdateStatus>(refresh ? "/api/system/update?refresh=true" : "/api/system/update");
}

export function pullFromGitHub(): Promise<UpdateResult> {
  return requestJSON<UpdateResult>("/api/system/update", { method: "POST" });
}

export function listAppLogs(kind?: AppLogKind, limit = 100): Promise<AppLogsResponse> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (kind) {
    params.set("kind", kind);
  }
  return requestJSON<AppLogsResponse>(`/api/logs?${params.toString()}`);
}
