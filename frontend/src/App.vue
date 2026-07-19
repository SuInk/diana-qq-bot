<template>
  <div class="app-shell" :data-theme="resolvedTheme" :style="themeStyleVars">
    <div class="app-frame" :class="{ 'test-mode': activeTab === 'test' }">
      <button v-if="sidebarOpen" class="sidebar-overlay" type="button" aria-label="关闭导航" @click="sidebarOpen = false" />

      <aside class="sidebar" :class="{ open: sidebarOpen }">
        <div class="sidebar-brand">
          <div class="brand-mark">
            <BotMessageSquare :size="20" aria-hidden="true" />
          </div>
          <div>
            <p class="brand-title">DIANA QQ BOT</p>
            <p class="brand-subtitle">CONTROL PANEL</p>
            <button
              class="brand-version"
              :class="{ warning: systemHasUpdate, open: updateDrawerOpen }"
              type="button"
              aria-label="系统更新"
              :title="systemEntryTitle"
              @click="toggleUpdateDrawer"
            >
              <span>v{{ appVersion }}</span>
              <span v-if="systemHasUpdate" class="brand-update-count">{{ (updateStatus?.behind ?? 0) > 0 ? updateStatus?.behind : "!" }}</span>
            </button>
          </div>
        </div>

        <nav class="sidebar-nav" role="tablist" aria-label="功能页面">
          <button
            :id="`tab-${dashboardTab.id}`"
            type="button"
            role="tab"
            :aria-selected="activeTab === dashboardTab.id"
            :aria-controls="`panel-${dashboardTab.id}`"
            class="sidebar-tab"
            :class="{ active: activeTab === dashboardTab.id }"
            @click="selectTab(dashboardTab.id)"
          >
            <component :is="dashboardTab.icon" :size="18" aria-hidden="true" />
            <span>{{ dashboardTab.label }}</span>
          </button>
          <div v-for="group in sidebarGroups" :key="group.label" class="sidebar-nav-group">
            <p>{{ group.label }}</p>
            <button
              v-for="tab in group.tabs"
              :id="`tab-${tab.id}`"
              :key="tab.id"
              type="button"
              role="tab"
              :aria-selected="activeTab === tab.id"
              :aria-controls="`panel-${tab.id}`"
              class="sidebar-tab"
              :class="{ active: activeTab === tab.id }"
              @click="selectTab(tab.id)"
            >
              <component :is="tab.icon" :size="18" aria-hidden="true" />
              <span>{{ tab.label }}</span>
            </button>
          </div>
        </nav>

        <div class="sidebar-user">
          <div class="sidebar-avatar">D</div>
          <div>
            <strong>Diana</strong>
            <span>管理员</span>
          </div>
          <ChevronDown :size="16" aria-hidden="true" />
        </div>
      </aside>

      <div class="content-shell">
        <header class="workspace-head">
          <div class="workspace-head-main">
            <div class="workspace-head-leading">
              <button class="icon-button workspace-menu" type="button" aria-label="切换导航" title="切换导航" @click="sidebarOpen = !sidebarOpen">
                <PanelLeftOpen :size="18" aria-hidden="true" />
              </button>
              <div v-if="activeTab === 'dashboard'" class="workspace-page-title">
                <strong>仪表盘</strong>
                <span>欢迎回来，Diana</span>
              </div>
            </div>
          </div>
          <div class="workspace-head-actions">
            <div class="status" :class="status.kind || 'ok'">
              <CheckCircle2 v-if="status.kind !== 'bad'" :size="16" aria-hidden="true" />
              <Activity v-else :size="16" aria-hidden="true" />
              <span>{{ status.text }}</span>
            </div>
            <div class="head-mode-switch" role="group" aria-label="主题切换">
              <button type="button" :aria-pressed="themeMode === 'system'" @click="setThemeMode('system')">跟随系统</button>
              <button type="button" :aria-pressed="themeMode === 'light'" @click="setThemeMode('light')">浅色</button>
              <button type="button" :aria-pressed="themeMode === 'dark'" @click="setThemeMode('dark')">深色</button>
            </div>
            <button class="icon-button" type="button" aria-label="通知" title="通知">
              <Bell :size="17" aria-hidden="true" />
            </button>
            <button class="icon-button" type="button" aria-label="退出登录" title="退出登录" @click="onLogoutAdmin">
              <LogOut :size="17" aria-hidden="true" />
            </button>
            <button class="avatar-button" type="button" aria-label="当前用户">
              <span>D</span>
            </button>
          </div>
        </header>

        <main class="workspace tab-workspace">
          <section
            v-show="activeTab === 'dashboard'"
            id="panel-dashboard"
            class="panel tab-panel dashboard-panel"
            role="tabpanel"
            aria-labelledby="tab-dashboard"
          >
            <section class="dashboard-surface" aria-labelledby="dashboard-title">
              <div class="dashboard-head">
                <div>
                  <h2 id="dashboard-title">概览</h2>
                </div>
                <div class="dashboard-actions">
                  <button class="button" type="button" :disabled="loadingLogs" @click="refreshDashboard">
                    <RefreshCw :size="16" aria-hidden="true" />
                    <span>{{ loadingLogs ? "刷新中" : "刷新" }}</span>
                  </button>
                  <button class="button primary" type="button" @click="selectTab('qqbot')">
                    <Cable :size="16" aria-hidden="true" />
                    <span>机器人配置</span>
                  </button>
                </div>
              </div>

              <div class="dashboard-hero">
                <button
                  class="dashboard-main-status dashboard-link-card"
                  :class="dashboardBotTone"
                  type="button"
                  aria-label="打开 QQ 机器人管理"
                  @click="selectTab('qqbot')"
                >
                  <div class="dashboard-status-icon">
                    <BotMessageSquare :size="24" aria-hidden="true" />
                  </div>
                  <div>
                    <span>QQ 机器人</span>
                    <strong>{{ dashboardBotStateLabel }}</strong>
                    <small>{{ dashboardBotDetail }}</small>
                  </div>
                  <ChevronRight class="dashboard-card-link-icon" :size="17" aria-hidden="true" />
                </button>
                <div class="dashboard-quick-grid">
                  <button
                    v-for="item in dashboardMetrics"
                    :key="item.label"
                    class="dashboard-metric dashboard-link-card"
                    type="button"
                    :aria-label="`查看${item.label}详情`"
                    @click="selectTab(item.target)"
                  >
                    <component :is="item.icon" :size="17" aria-hidden="true" />
                    <span>{{ item.label }}</span>
                    <strong>{{ item.value }}</strong>
                    <small>{{ item.detail }}</small>
                    <ChevronRight class="dashboard-card-link-icon" :size="16" aria-hidden="true" />
                  </button>
                </div>
              </div>

              <div class="dashboard-chart-grid">
                <section class="dashboard-chart-card">
                  <div class="dashboard-section-head">
                    <h3>今日回复率</h3>
                    <span class="dashboard-chart-caption">{{ dashboardStats?.replied_messages ?? 0 }}/{{ dashboardStats?.received_messages ?? 0 }}</span>
                  </div>
                  <div class="dashboard-donut-wrap">
                    <div class="dashboard-donut" :style="{ '--value': `${dashboardReplyRate}%` }">
                      <span>{{ dashboardReplyRate }}%</span>
                    </div>
                    <div class="dashboard-chart-legend">
                      <span><i class="ok"></i>已回复 {{ formatStatNumber(dashboardStats?.replied_messages ?? 0) }}</span>
                      <span><i></i>收到 {{ formatStatNumber(dashboardStats?.received_messages ?? 0) }}</span>
                    </div>
                  </div>
                </section>

                <section class="dashboard-chart-card">
                  <div class="dashboard-section-head">
                    <h3>今日功能统计</h3>
                    <span class="dashboard-chart-caption">{{ formatStatNumber(dashboardStats?.api_calls ?? 0) }} API</span>
                  </div>
                  <div class="dashboard-function-chart" role="img" aria-label="今日文本回复、生图修图、联网搜索和 LLM API 调用统计图">
                    <div class="dashboard-bar-list">
                      <button
                        v-for="item in dashboardOperationBars"
                        :key="item.label"
                        class="dashboard-bar-item"
                        type="button"
                        :aria-label="`查看${item.label}详情`"
                        @click="selectTab(item.target)"
                      >
                        <div>
                          <span>{{ item.label }}</span>
                          <span class="dashboard-bar-value">
                            <strong>{{ item.value }}</strong>
                            <ChevronRight :size="14" aria-hidden="true" />
                          </span>
                        </div>
                        <div class="dashboard-bar-track">
                          <span :style="{ width: `${item.percent}%` }"></span>
                        </div>
                      </button>
                    </div>
                    <div class="dashboard-chart-axis" aria-hidden="true">
                      <span>0</span>
                      <span>50%</span>
                      <span>100%</span>
                    </div>
                  </div>
                </section>

                <section class="dashboard-chart-card dashboard-chart-card-wide">
                  <div class="dashboard-section-head">
                    <h3>24 小时消息流</h3>
                    <span class="dashboard-chart-caption">消息 / 回复 / 工具</span>
                  </div>
                  <div class="dashboard-timeline">
                    <article v-for="bucket in dashboardHourlyBars" :key="bucket.hour" class="dashboard-hour">
                      <div class="dashboard-hour-bars" :title="`${bucket.hour} 消息 ${bucket.messages}，回复 ${bucket.replies}，搜索/生图 ${bucket.searches + bucket.images}`">
                        <span class="messages" :style="{ height: `${bucket.messagePercent}%` }"></span>
                        <span class="replies" :style="{ height: `${bucket.replyPercent}%` }"></span>
                        <span class="tools" :style="{ height: `${bucket.toolPercent}%` }"></span>
                      </div>
                      <small>{{ bucket.hour.slice(0, 2) }}</small>
                    </article>
                  </div>
                </section>

                <section class="dashboard-chart-card dashboard-chart-card-wide dashboard-server-card">
                  <div class="dashboard-section-head">
                    <h3>服务器信息</h3>
                    <span class="dashboard-chart-caption">{{ dashboardServerSubtitle }}</span>
                  </div>
                  <div class="dashboard-server-layout">
                    <div class="dashboard-server-details">
                      <section>
                        <div class="dashboard-server-title">
                          <Cpu :size="18" aria-hidden="true" />
                          <h4>CPU</h4>
                        </div>
                        <dl class="dashboard-server-list">
                          <div>
                            <dt>型号</dt>
                            <dd>{{ dashboardServer?.cpu_model || "-" }}</dd>
                          </div>
                          <div>
                            <dt>核心数</dt>
                            <dd>{{ dashboardServer?.cpu_cores || "-" }}</dd>
                          </div>
                          <div>
                            <dt>系统占用</dt>
                            <dd>{{ formatPercent(dashboardServer?.cpu_usage_percent ?? 0) }}</dd>
                          </div>
                          <div>
                            <dt>Diana 进程</dt>
                            <dd>{{ formatPercent(dashboardServer?.process_cpu_percent ?? 0) }}</dd>
                          </div>
                        </dl>
                      </section>
                      <section>
                        <div class="dashboard-server-title">
                          <MemoryStick :size="18" aria-hidden="true" />
                          <h4>内存</h4>
                        </div>
                        <dl class="dashboard-server-list">
                          <div>
                            <dt>总量</dt>
                            <dd>{{ formatBytes(dashboardServer?.memory_total_bytes ?? 0) }}</dd>
                          </div>
                          <div>
                            <dt>使用量</dt>
                            <dd>{{ formatBytes(dashboardServer?.memory_used_bytes ?? 0) }}</dd>
                          </div>
                          <div>
                            <dt>Diana 进程</dt>
                            <dd>{{ formatBytes(dashboardServer?.process_memory_bytes ?? 0) }}</dd>
                          </div>
                          <div>
                            <dt>Go Heap</dt>
                            <dd>{{ formatBytes(dashboardServer?.go_heap_alloc_bytes ?? 0) }}</dd>
                          </div>
                        </dl>
                      </section>
                    </div>
                    <div class="dashboard-server-rings">
                      <div class="dashboard-server-ring" :style="{ '--value': `${dashboardServerCPUPercent}%` }">
                        <span>CPU 占用</span>
                        <strong>{{ formatPercent(dashboardServerCPUPercent) }}</strong>
                      </div>
                      <div class="dashboard-server-ring memory" :style="{ '--value': `${dashboardServerMemoryPercent}%` }">
                        <span>内存占用</span>
                        <strong>{{ formatPercent(dashboardServerMemoryPercent) }}</strong>
                      </div>
                    </div>
                  </div>
                  <div class="dashboard-server-footer">
                    <span>{{ dashboardServerRuntimeLabel }}</span>
                    <span>PID {{ dashboardServer?.process_id || "-" }}</span>
                    <span>{{ dashboardServer?.go_routines ?? 0 }} goroutines</span>
                    <span>{{ dashboardServer?.runtime_version || "-" }}</span>
                  </div>
                </section>
              </div>

              <div class="dashboard-grid">
                <section class="dashboard-section">
                  <div class="dashboard-section-head">
                    <h3>核心状态</h3>
                    <button class="table-action" type="button" aria-label="打开 LLM 配置" title="打开 LLM 配置" @click="selectTab('llm')">
                      <BrainCircuit :size="16" aria-hidden="true" />
                    </button>
                  </div>
                  <div class="dashboard-status-list">
                    <article v-for="item in dashboardHealthItems" :key="item.label" class="dashboard-status-item" :class="item.tone">
                      <component :is="item.icon" :size="18" aria-hidden="true" />
                      <div>
                        <span>{{ item.label }}</span>
                        <strong>{{ item.value }}</strong>
                        <small>{{ item.detail }}</small>
                      </div>
                    </article>
                  </div>
                </section>

                <section class="dashboard-section">
                  <div class="dashboard-section-head">
                    <h3>最近消息</h3>
                    <button class="table-action" type="button" aria-label="打开 QQ 机器人" title="打开 QQ 机器人" @click="selectTab('qqbot')">
                      <MessageCircle :size="16" aria-hidden="true" />
                    </button>
                  </div>
                  <div class="dashboard-feed">
                    <article v-for="event in dashboardRecentEvents" :key="`${event.at}-${event.group_id || event.user_id}-${event.text}`" class="dashboard-feed-item">
                      <span :class="event.handled ? 'ok' : 'idle'">{{ event.handled ? "已处理" : "未回复" }}</span>
                      <div>
                        <strong>{{ event.group_id ? `群 ${event.group_id}` : event.user_id ? `用户 ${event.user_id}` : event.kind }}</strong>
                        <p>{{ event.text || event.reply || event.error || "-" }}</p>
                        <small>{{ formatLogTime(event.at) }}</small>
                      </div>
                    </article>
                    <div v-if="dashboardRecentEvents.length === 0" class="dashboard-empty">暂无最近消息。</div>
                  </div>
                </section>

                <section class="dashboard-section">
                  <div class="dashboard-section-head">
                    <h3>订阅任务</h3>
                    <button class="table-action" type="button" aria-label="打开 QQ 机器人" title="打开 QQ 机器人" @click="selectTab('qqbot')">
                      <CalendarDays :size="16" aria-hidden="true" />
                    </button>
                  </div>
                  <div class="dashboard-task-list">
                    <article v-for="task in dashboardTasks" :key="task.id" class="dashboard-task-item">
                      <span :class="task.status === 'active' ? 'ok' : 'idle'">{{ task.kind === "schedule" ? "订阅" : "提醒" }}</span>
                      <div>
                        <strong>{{ task.message || "-" }}</strong>
                        <small>
                          {{ task.owner_id || task.group_id || task.user_id || "-" }} ·
                          {{ task.status || "-" }} ·
                          {{ task.trigger_at ? formatLogTime(task.trigger_at) : "-" }}
                        </small>
                      </div>
                    </article>
                    <div v-if="dashboardTasks.length === 0" class="dashboard-empty">暂无订阅或提醒任务。</div>
                  </div>
                </section>

                <section class="dashboard-section">
                  <div class="dashboard-section-head">
                    <h3>插件概览</h3>
                    <button class="table-action" type="button" aria-label="打开插件管理" title="打开插件管理" @click="selectTab('plugins')">
                      <PlugZap :size="16" aria-hidden="true" />
                    </button>
                  </div>
                  <div class="dashboard-plugin-list">
                    <article v-for="plugin in dashboardPlugins" :key="plugin.manifest.id" class="dashboard-plugin-item">
                      <component :is="pluginIcon(plugin)" :size="17" aria-hidden="true" />
                      <div>
                        <strong>{{ plugin.manifest.name }}</strong>
                        <span>{{ plugin.manifest.id }}</span>
                      </div>
                      <small :class="pluginStatusKind(plugin)">{{ pluginStatusLabel(plugin) }}</small>
                    </article>
                    <div v-if="dashboardPlugins.length === 0" class="dashboard-empty">暂无插件。</div>
                  </div>
                </section>

                <section class="dashboard-section">
                  <div class="dashboard-section-head">
                    <h3>日志与维护</h3>
                    <button class="table-action" type="button" aria-label="打开日志中心" title="打开日志中心" @click="selectTab('logs')">
                      <FileClock :size="16" aria-hidden="true" />
                    </button>
                  </div>
                  <div class="dashboard-log-list">
                    <article v-for="entry in dashboardLogs" :key="entry.id" class="dashboard-log-item" :class="entry.level">
                      <span>{{ entry.kind === "error" ? "错误" : "操作" }}</span>
                      <div>
                        <strong>{{ entry.message || entry.action }}</strong>
                        <small>{{ formatLogTime(entry.created_at) }} · {{ entry.target || entry.actor || "-" }}</small>
                      </div>
                    </article>
                    <div v-if="dashboardLogs.length === 0" class="dashboard-empty">暂无日志。</div>
                  </div>
                </section>
              </div>
            </section>
          </section>

          <section
            v-show="activeTab === 'llm'"
            id="panel-llm"
            class="panel tab-panel llm-panel"
            role="tabpanel"
            aria-labelledby="tab-llm"
          >
            <section class="llm-management-panel" aria-labelledby="llm-title">
              <div class="llm-management-head">
                <div class="llm-management-title">
                  <div class="section-icon">
                    <LayoutGrid :size="18" aria-hidden="true" />
                  </div>
                  <h2 id="llm-title">LLM 配置管理</h2>
                </div>
                <div class="llm-management-actions">
                  <button class="button primary" type="button" aria-label="新建配置" title="新建配置" @click="createProfile">
                    <Plus :size="16" aria-hidden="true" />
                    <span>新建配置</span>
                  </button>
                  <button class="icon-button" type="button" :disabled="savingLLM" aria-label="同步配置" title="同步配置" @click="refreshLLMConfig">
                    <RefreshCw :size="16" aria-hidden="true" />
                  </button>
                  <button class="icon-button" type="button" aria-label="导入配置" title="导入配置" @click="openImportProfiles">
                    <Upload :size="16" aria-hidden="true" />
                  </button>
                  <label class="toolbar-search llm-table-search">
                    <Search :size="16" aria-hidden="true" />
                    <input v-model.trim="llmProfileQuery" autocomplete="off" placeholder="搜索配置名称、说明、Provider 或 Model" />
                  </label>
                </div>
              </div>

              <input ref="llmImportFileRef" class="hidden-file-input" type="file" accept="application/json,.json" @change="onImportProfilesFileChange" />

              <div class="llm-config-table-wrap">
                <table class="llm-config-table">
                  <thead>
                    <tr>
                      <th scope="col">配置名称</th>
                      <th scope="col">分组</th>
                      <th scope="col">配置说明</th>
                      <th scope="col">Provider / Model</th>
                      <th scope="col">Endpoint</th>
                      <th scope="col">更新时间</th>
                      <th scope="col">操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr
                      v-for="profile in filteredLLMProfiles"
                      :key="profile.id || profile.name"
                      :class="{ active: isActiveLLMProfile(profile) }"
                    >
                      <td>
                        <div class="profile-name-cell">
                          <button
                            class="profile-star-button"
                            type="button"
                            :class="{ active: isActiveLLMProfile(profile) }"
                            :disabled="isActiveLLMProfile(profile) || !profile.id"
                            :aria-label="isActiveLLMProfile(profile) ? '当前生效配置' : '设为当前生效'"
                            :title="isActiveLLMProfile(profile) ? '当前生效配置' : '设为当前生效'"
                            @click="onSelectProfile(profile.id || '')"
                          >
                            <Star :size="17" aria-hidden="true" />
                          </button>
                          <div class="profile-name-copy">
                            <div class="profile-title-line">
                              <strong>{{ profile.name || "未命名配置" }}</strong>
                              <span v-if="isActiveLLMProfile(profile)" class="pill primary-pill">当前生效</span>
                              <span class="pill">{{ profileKeyConfigured(profile) ? "Key 已保存" : "未保存 Key" }}</span>
                            </div>
                            <span>{{ profile.id ? profile.id.slice(0, 8) : "新配置" }}</span>
                          </div>
                        </div>
                      </td>
                      <td class="profile-group-cell">{{ profileGroupLabel(profile) }}</td>
                      <td class="profile-description-cell">{{ profile.description || "-" }}</td>
                      <td>
                        <div class="provider-model-cell">
                          <strong>{{ providerDisplayLabel(profile.provider) }}</strong>
                          <span>{{ profile.model || "-" }}</span>
                        </div>
                      </td>
                      <td class="endpoint-cell">{{ profileEndpointLabel(profile) }}</td>
                      <td class="time-cell">{{ profileUpdatedAtLabel(profile) }}</td>
                      <td>
                        <div class="llm-row-actions">
                          <button
                            class="table-action"
                            type="button"
                            :disabled="testingProfileID === profile.id"
                            :aria-label="testingProfileID === profile.id ? '测试中' : '测试连接'"
                            :title="testingProfileID === profile.id ? '测试中' : '测试连接'"
                            @click="onTestProfile(profile)"
                          >
                            <Activity :size="16" aria-hidden="true" />
                          </button>
                          <button class="table-action" type="button" aria-label="编辑配置" title="编辑配置" @click="editProfile(profile)">
                            <Pencil :size="16" aria-hidden="true" />
                          </button>
                          <button
                            class="table-action"
                            type="button"
                            :disabled="!profile.id"
                            aria-label="复制配置"
                            title="复制配置"
                            @click="onCloneProfile(profile)"
                          >
                            <Copy :size="16" aria-hidden="true" />
                          </button>
                          <div class="llm-more-wrap">
                            <button
                              class="table-action"
                              type="button"
                              :aria-expanded="llmMoreMenuProfileID === profile.id"
                              aria-label="更多操作"
                              title="更多操作"
                              @click.stop="toggleLLMMoreMenu(profile)"
                            >
                              <MoreHorizontal :size="18" aria-hidden="true" />
                            </button>
                            <div v-if="llmMoreMenuProfileID === profile.id" class="llm-more-menu">
                              <button type="button" :disabled="isActiveLLMProfile(profile) || !profile.id" @click="onSelectProfile(profile.id || '')">
                                <Star :size="16" aria-hidden="true" />
                                <span>设为当前生效</span>
                              </button>
                              <button type="button" :disabled="!profile.id" @click="onExportProfile(profile)">
                                <Download :size="16" aria-hidden="true" />
                                <span>导出配置</span>
                              </button>
                              <button class="danger" type="button" :disabled="!profile.id || llmProfiles.length <= 1" @click="onDeleteProfile(profile)">
                                <Trash2 :size="16" aria-hidden="true" />
                                <span>删除配置</span>
                              </button>
                            </div>
                          </div>
                        </div>
                      </td>
                    </tr>
                    <tr v-if="filteredLLMProfiles.length === 0">
                      <td colspan="7">
                        <div class="empty-state plugin-empty">没有匹配的配置。</div>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>

              <div class="llm-table-footer">
                <span>共 {{ filteredLLMProfiles.length }} 条配置</span>
                <div class="llm-table-footer-actions">
                  <button class="icon-button" type="button" disabled aria-label="上一页">
                    <ChevronLeft :size="16" aria-hidden="true" />
                  </button>
                  <span class="page-number">1</span>
                  <button class="icon-button" type="button" disabled aria-label="下一页">
                    <ChevronDown :size="16" aria-hidden="true" class="rotate-left" />
                  </button>
                  <span class="page-size">10 条/页</span>
                </div>
              </div>
            </section>

            <div v-if="llmEditorMode === 'edit'" class="modal-backdrop" role="presentation" @click.self="closeLLMEditor">
              <form class="llm-config-modal" role="dialog" aria-modal="true" aria-labelledby="llm-editor-title" @submit.prevent="onSaveLLM">
                <div class="modal-head">
                  <h2 id="llm-editor-title">{{ llmForm.id ? "编辑配置" : "新建配置" }}</h2>
                  <button class="icon-button" type="button" aria-label="关闭" title="关闭" @click="closeLLMEditor">
                    <X :size="16" aria-hidden="true" />
                  </button>
                </div>

                <div class="modal-body llm-modal-grid">
                  <label class="field">
                    <span>配置名称</span>
                    <input v-model.trim="llmForm.name" autocomplete="off" placeholder="请输入配置名称" />
                  </label>

                  <label class="field">
                    <span>配置说明</span>
                    <input v-model.trim="llmForm.description" autocomplete="off" placeholder="请输入配置说明（可选）" />
                  </label>

                  <label class="field span-all">
                    <span>分组</span>
                    <input v-model.trim="llmForm.group" autocomplete="off" placeholder="default" />
                  </label>

                  <div class="field span-all">
                    <span>Provider</span>
                    <div class="segmented provider-segment" role="group" aria-label="Provider">
                      <button
                        v-for="option in providerOptions"
                        :key="option.value"
                        type="button"
                        :aria-pressed="llmForm.provider === option.value"
                        @click="setProvider(option.value)"
                      >
                        <component :is="option.icon" :size="16" aria-hidden="true" />
                        <span>{{ option.label }}</span>
                      </button>
                    </div>
                  </div>

                  <label class="field span-all">
                    <span>Model</span>
                    <div ref="modelSelectRef" class="model-select">
                      <div class="model-control">
                        <div class="model-input-wrap">
                          <input
                            v-model.trim="llmForm.model"
                            autocomplete="off"
                            required
                            placeholder="选择或输入 Model"
                            @focus="modelMenuOpen = true"
                            @input="modelMenuOpen = true"
                            @keydown.escape="modelMenuOpen = false"
                          />
                          <button
                            class="icon-button"
                            type="button"
                            aria-label="展开模型列表"
                            title="展开模型列表"
                            @click="toggleModelMenu"
                          >
                            <ChevronDown :size="16" aria-hidden="true" />
                          </button>
                        </div>
                        <button
                          class="icon-button"
                          type="button"
                          aria-label="刷新模型列表"
                          title="刷新模型列表"
                          :disabled="loadingModels"
                          @click="onRefreshModels"
                        >
                          <RefreshCw :size="16" aria-hidden="true" />
                        </button>
                      </div>
                      <div v-if="modelMenuOpen" class="model-menu">
                        <button
                          v-for="option in filteredModelOptions"
                          :key="option.id"
                          class="model-option"
                          type="button"
                          :class="{ active: llmForm.model === option.id }"
                          @mousedown.prevent="selectModel(option.id)"
                        >
                          <span class="model-option-id">{{ option.id }}</span>
                          <span class="model-option-meta">
                            {{ option.name || option.owned_by || "模型" }}
                            <template v-if="option.context_window_tokens"> · {{ option.context_window_tokens.toLocaleString() }} ctx</template>
                          </span>
                        </button>
                        <div v-if="filteredModelOptions.length === 0" class="model-empty">没有匹配的模型</div>
                      </div>
                    </div>
                    <span class="field-hint">{{ filteredModelOptions.length || modelOptions.length }} 个候选</span>
                  </label>

                  <label class="field span-all">
                    <span>Endpoint</span>
                    <input v-model.trim="llmForm.baseURL" autocomplete="off" placeholder="例如：https://api.openai.com/v1" />
                  </label>

                  <div v-if="llmForm.provider === 'openai_compatible'" class="field span-all">
                    <span>文本 API</span>
                    <div class="segmented provider-segment" role="group" aria-label="文本 API">
                      <button type="button" :aria-pressed="llmForm.apiFormat === 'responses'" @click="llmForm.apiFormat = 'responses'">
                        <span>Responses</span>
                      </button>
                      <button type="button" :aria-pressed="llmForm.apiFormat === 'chat_completions'" @click="llmForm.apiFormat = 'chat_completions'">
                        <span>Chat Completions</span>
                      </button>
                    </div>
                  </div>

                  <label class="field span-all">
                    <span>API Key</span>
                    <div class="api-key-control">
                      <input
                        v-model.trim="llmForm.apiKey"
                        :type="showLLMAPIKey ? 'text' : 'password'"
                        autocomplete="off"
                        minlength="8"
                        placeholder="留空沿用已保存 Key，至少 8 字符"
                      />
                      <button
                        class="icon-button"
                        type="button"
                        :aria-label="showLLMAPIKey ? '隐藏 API Key' : '显示 API Key'"
                        :title="showLLMAPIKey ? '隐藏 API Key' : '显示 API Key'"
                        @click="showLLMAPIKey = !showLLMAPIKey"
                      >
                        <EyeOff v-if="showLLMAPIKey" :size="16" aria-hidden="true" />
                        <Eye v-else :size="16" aria-hidden="true" />
                      </button>
                    </div>
                  </label>

                  <div class="field span-all">
                    <div class="headers-field-head">
                      <span>Headers</span>
                      <button class="icon-button" type="button" aria-label="添加请求头" title="添加请求头" @click="addLLMHeaderRow">
                        <Plus :size="16" aria-hidden="true" />
                      </button>
                    </div>
                    <div class="headers-editor">
                      <div v-for="row in llmHeaderRows" :key="row.id" class="header-row">
                        <input v-model.trim="row.name" autocomplete="off" placeholder="Header 名称" />
                        <input v-model.trim="row.value" autocomplete="off" placeholder="Header 值" />
                        <button class="icon-button" type="button" aria-label="删除请求头" title="删除请求头" @click="removeLLMHeaderRow(row.id)">
                          <Trash2 :size="16" aria-hidden="true" />
                        </button>
                      </div>
                      <button v-if="llmHeaderRows.length === 0" class="add-header-empty" type="button" @click="addLLMHeaderRow">
                        <Plus :size="16" aria-hidden="true" />
                        <span>添加请求头（可选）</span>
                      </button>
                    </div>
                  </div>

                  <div class="subpanel span-all">
                    <button class="subpanel-head" type="button" @click="llmAdvancedOpen = !llmAdvancedOpen">
                      <span>高级设置</span>
                      <ChevronDown :size="16" aria-hidden="true" :class="{ expanded: llmAdvancedOpen }" />
                    </button>
                    <div v-if="llmAdvancedOpen" class="subpanel-body">
                      <label class="field span-all">
                        <span>生图模型</span>
                        <input
                          v-model.trim="llmForm.imageModel"
                          autocomplete="off"
                          list="image-model-options"
                          placeholder="可选；留空使用当前 provider 默认生图模型"
                        />
                        <datalist id="image-model-options">
                          <option v-for="option in imageModelOptions" :key="option" :value="option" />
                        </datalist>
                      </label>

                      <label v-if="llmForm.provider === 'openai_compatible'" class="field span-all">
                        <span>生图 Endpoint</span>
                        <input v-model.trim="llmForm.imageBaseURL" autocomplete="off" placeholder="留空沿用主 Endpoint" />
                      </label>

                      <label v-if="llmForm.provider === 'openai_compatible'" class="field span-all">
                        <span>生图直连地址</span>
                        <input v-model.trim="llmForm.imageOrigin" autocomplete="off" placeholder="例如：129.153.75.15:443" />
                      </label>

                      <label v-if="llmForm.provider === 'openai_compatible'" class="field span-all">
                        <span>生图 Timeout MS</span>
                        <input v-model.number="llmForm.imageTimeoutMS" type="number" min="0" step="1000" />
                      </label>

                      <label v-if="llmForm.provider === 'openai_compatible'" class="field span-all">
                        <span>User-Agent</span>
                        <input v-model.trim="llmForm.userAgent" autocomplete="off" placeholder="diana-qq-bot" />
                      </label>

                      <label class="field">
                        <span>Temperature</span>
                        <input v-model.number="llmForm.temperature" type="number" min="0" max="2" step="0.1" />
                      </label>

                      <label v-if="llmForm.provider === 'openai_compatible'" class="field">
                        <span>Reasoning Effort</span>
                        <select v-model="llmForm.reasoningEffort">
                          <option value="">模型默认</option>
                          <option value="low">Low</option>
                          <option value="medium">Medium</option>
                          <option value="high">High</option>
                          <option value="xhigh">XHigh</option>
                        </select>
                      </label>

                      <label class="field">
                        <span>模型上下文窗口</span>
                        <input v-model.number="llmForm.contextWindowTokens" type="number" min="1024" step="1024" readonly />
                      </label>

                      <label class="field">
                        <span>请求上下文 Token 预算</span>
                        <input
                          v-model.number="llmForm.maxContextTokens"
                          type="number"
                          min="1024"
                          :max="llmForm.contextWindowTokens"
                          step="1024"
                        />
                      </label>

                      <label class="field">
                        <span>Max Output Tokens</span>
                        <input v-model.number="llmForm.maxOutputTokens" type="number" min="0" :max="Math.max(0, llmForm.maxContextTokens - 1)" step="1" />
                      </label>

                      <label class="field span-all">
                        <span>Timeout MS</span>
                        <input v-model.number="llmForm.timeoutMS" type="number" min="0" step="1000" />
                      </label>
                    </div>
                  </div>
                </div>

                <div class="modal-actions">
                  <button class="button" type="button" :disabled="savingLLM" @click="closeLLMEditor">取消</button>
                  <button class="button primary" type="submit" :disabled="savingLLM || !llmDirty">
                    <Save :size="16" aria-hidden="true" />
                    <span>{{ savingLLM ? "保存中" : "保存" }}</span>
                  </button>
                </div>
              </form>
            </div>

            <div v-if="llmTestResult.open" class="modal-backdrop test-result-backdrop" role="presentation" @click.self="closeLLMTestResult">
              <section class="test-result-modal" role="dialog" aria-modal="true" aria-labelledby="llm-test-result-title">
                <div class="modal-head">
                  <h2 id="llm-test-result-title">测试连接结果</h2>
                  <button class="icon-button" type="button" aria-label="关闭" title="关闭" @click="closeLLMTestResult">
                    <X :size="16" aria-hidden="true" />
                  </button>
                </div>
                <div class="test-result-body">
                  <div class="test-result-banner" :class="{ ok: llmTestResult.ok, bad: !llmTestResult.ok }">
                    <CheckCircle2 v-if="llmTestResult.ok" :size="22" aria-hidden="true" />
                    <TriangleAlert v-else :size="22" aria-hidden="true" />
                    <div>
                      <strong>{{ llmTestResult.title }}</strong>
                      <p>{{ llmTestResult.message }}</p>
                    </div>
                  </div>
                  <dl class="test-result-list">
                    <div>
                      <dt>响应时间</dt>
                      <dd>{{ llmTestResult.latencyMS ? `${llmTestResult.latencyMS}ms` : "-" }}</dd>
                    </div>
                    <div>
                      <dt>状态码</dt>
                      <dd>{{ llmTestResult.statusCode || "-" }}</dd>
                    </div>
                    <div>
                      <dt>模型</dt>
                      <dd>{{ llmTestResult.model || "-" }}</dd>
                    </div>
                    <div>
                      <dt>测试时间</dt>
                      <dd>{{ llmTestResult.testedAt || "-" }}</dd>
                    </div>
                  </dl>
                </div>
                <div class="modal-actions">
                  <button class="button" type="button" @click="closeLLMTestResult">关闭</button>
                </div>
              </section>
            </div>
          </section>

      <section
        v-show="activeTab === 'test'"
        id="panel-test"
        class="panel tab-panel"
        role="tabpanel"
        aria-labelledby="tab-test"
      >
        <div class="test-console">
          <aside class="test-session-sidebar" aria-label="会话列表">
            <div class="test-session-head">
              <h2 id="test-title">会话列表</h2>
              <div class="test-session-tools">
                <button class="icon-button" type="button" aria-label="搜索会话" title="搜索会话">
                  <Search :size="17" aria-hidden="true" />
                </button>
                <button class="icon-button" type="button" aria-label="新建会话" title="新建会话" @click="startNewTestConversation">
                  <Plus :size="17" aria-hidden="true" />
                </button>
              </div>
            </div>

            <div class="test-mode-tabs" role="group" aria-label="测试类型">
              <button type="button" :aria-pressed="testConversationMode === 'group'" @click="testConversationMode = 'group'">群聊测试</button>
              <button type="button" :aria-pressed="testConversationMode === 'private'" @click="testConversationMode = 'private'">私聊测试</button>
            </div>

            <div class="test-session-list">
              <button
                v-for="session in testSessions"
                :key="session.id"
                class="test-session-card"
                :class="{ active: selectedTestSessionID === session.id }"
                type="button"
                @click="selectTestSession(session.id)"
              >
                <div class="test-session-avatar" :class="session.kind">
                  <component :is="session.icon" :size="22" aria-hidden="true" />
                </div>
                <div class="test-session-copy">
                  <div>
                    <strong>{{ session.title }}</strong>
                    <span>{{ session.time }}</span>
                  </div>
                  <p>{{ session.preview }}</p>
                </div>
                <span class="test-session-result" :class="session.ok ? 'ok' : 'bad'">{{ session.ok ? "通过" : "失败" }}</span>
              </button>
            </div>

            <div class="test-session-count">共 {{ testSessions.length }} 个会话</div>
          </aside>

          <section class="test-chat-panel" aria-label="连通测试台">
            <div class="test-chat-head">
              <div class="test-chat-title">
                <div class="test-room-avatar">
                  <Users :size="23" aria-hidden="true" />
                </div>
                <div>
                  <h2>{{ activeTestSession.title }}</h2>
                  <p>{{ testConversationMode === 'group' ? `群组ID: ${testGroupID}` : `用户ID: ${testPrivateUserID}` }} <span>成员: {{ testConversationMode === 'group' ? testMemberCount : 1 }}</span></p>
                </div>
              </div>
              <div class="test-chat-actions">
                <button class="test-select-button" type="button">
                  <span>{{ llmForm.model || "未设置模型" }}</span>
                  <ChevronDown :size="16" aria-hidden="true" />
                </button>
                <button class="test-select-button" type="button">
                  <span>{{ activeTestScenarioLabel }}</span>
                  <ChevronDown :size="16" aria-hidden="true" />
                </button>
                <div class="status ok">
                  <CheckCircle2 :size="16" aria-hidden="true" />
                  <span>配置已加载</span>
                </div>
                <button class="test-tool-button" type="button" :disabled="activeTestHistory.length === 0" @click="clearTestHistory">
                  <Trash2 :size="17" aria-hidden="true" />
                  <span>清空</span>
                </button>
                <button class="test-tool-button" type="button" :disabled="!latestTestResult" @click="copyLatestTestResult">
                  <Copy :size="17" aria-hidden="true" />
                  <span>复制</span>
                </button>
                <button class="test-tool-button" type="button">
                  <ClipboardCheck :size="17" aria-hidden="true" />
                  <span>保存记录</span>
                </button>
                <button class="icon-button" type="button" aria-label="更多操作" title="更多操作">
                  <MoreVertical :size="18" aria-hidden="true" />
                </button>
              </div>
            </div>

            <div class="test-toolbar">
              <button class="button" type="button" @click="message = '你好，用一句话回复当前模型已连通。'">
                <Activity :size="16" aria-hidden="true" />
                <span>一句话测试</span>
              </button>
              <button class="button" type="button" @click="message = '请用 JSON 返回 provider、model、status。'">
                <Braces :size="16" aria-hidden="true" />
                <span>JSON 返回</span>
              </button>
              <button class="button" type="button" @click="message = '请模拟一次工具调用结果，总结你会调用的工具、参数和期望输出。'">
                <Wrench :size="16" aria-hidden="true" />
                <span>模拟工具结果</span>
              </button>
              <label class="test-context-toggle">
                <input v-model="recordTestContext" type="checkbox" />
                <span>记录上下文</span>
              </label>
              <div class="test-chat-mode-switch" role="group" aria-label="会话类型">
                <button type="button" :aria-pressed="testConversationMode === 'group'" @click="testConversationMode = 'group'">群聊</button>
                <button type="button" :aria-pressed="testConversationMode === 'private'" @click="testConversationMode = 'private'">私聊</button>
              </div>
            </div>

            <div ref="testMessageStreamRef" class="test-message-stream">
              <template v-if="activeTestHistory.length === 0">
                <div class="test-message-empty">当前会话暂无测试消息</div>
              </template>

              <div v-for="item in activeTestHistory" :key="item.id" class="test-message-row" :class="item.role">
                <div class="test-user-avatar" :class="{ bot: item.role !== 'user' }">{{ item.role === 'user' ? '你' : item.ok ? 'AI' : '!' }}</div>
                <div class="test-message-main">
                  <div class="test-message-meta">
                    <strong>{{ item.role === 'user' ? '你' : item.ok ? 'Agent' : '错误' }}</strong>
                    <span v-if="item.role !== 'user'" class="bot-tag">BOT</span>
                    <span>{{ item.at }}</span>
                  </div>
                  <div class="test-message-bubble" :class="{ agent: item.role !== 'user', error: item.role === 'error' }">
                    <pre>{{ item.text }}</pre>
                    <div v-if="item.role !== 'user'" class="test-message-tags">
                      <span>调用工具：{{ testToolCallCount > 0 ? "模型连通测试" : "无" }}</span>
                      <span>记忆命中：{{ recordTestContext ? "已记录上下文" : "未记录" }}</span>
                      <span>群聊识别：{{ testConversationMode === 'group' ? "是" : "否" }}</span>
                    </div>
                  </div>
                </div>
              </div>
            </div>

            <div class="test-result-strip">
              <div :class="{ ok: latestTestResult?.ok, bad: latestTestResult && !latestTestResult.ok }">
                <CheckCircle2 :size="18" aria-hidden="true" />
                <span>会话类型识别：</span>
                <strong>{{ testConversationMode === 'group' ? "正确" : "私聊" }}</strong>
              </div>
              <div>
                <Wrench :size="18" aria-hidden="true" />
                <span>工具调用：</span>
                <strong>{{ testToolCallCount }}次</strong>
              </div>
              <div>
                <Clock3 :size="18" aria-hidden="true" />
                <span>响应时间：</span>
                <strong>{{ testLatencyLabel }}</strong>
              </div>
              <div>
                <ShieldCheck :size="18" aria-hidden="true" />
                <span>风险拦截：</span>
                <strong>{{ latestTestResult?.ok === false ? 1 : 0 }}</strong>
              </div>
              <div class="test-result-time">
                <span>{{ latestTestResult?.at || "-" }} 完成</span>
                <CheckCircle2 :size="18" aria-hidden="true" />
              </div>
            </div>

            <form class="test-composer" @submit.prevent="onTest">
              <textarea
                v-model="message"
                placeholder="请输入测试消息（Enter 发送，Shift+Enter 换行）"
                @keydown.enter.exact.prevent="onTest"
                @keydown.meta.enter.prevent="onTest"
                @keydown.ctrl.enter.prevent="onTest"
              />
              <div class="test-composer-bottom">
                <div class="test-composer-icons">
                  <button type="button" aria-label="表情" title="表情">
                    <Smile :size="18" aria-hidden="true" />
                  </button>
                  <button type="button" aria-label="图片" title="图片">
                    <Image :size="18" aria-hidden="true" />
                  </button>
                  <button type="button" aria-label="附件" title="附件">
                    <Paperclip :size="18" aria-hidden="true" />
                  </button>
                  <button type="button" aria-label="结构化 JSON" title="结构化 JSON" @click="message = '请用 JSON 返回 provider、model、status。'">
                    <Braces :size="18" aria-hidden="true" />
                  </button>
                </div>
                <div class="test-send-actions">
                  <button class="button" type="button" :disabled="!lastUserTestMessage" @click="message = lastUserTestMessage">载入上一条</button>
                  <button class="button primary test-send-button" type="submit" :disabled="testing || !message.trim()">
                    <Send :size="18" aria-hidden="true" />
                    <span>{{ testing ? "发送中" : "发送测试" }}</span>
                  </button>
                </div>
              </div>
            </form>
          </section>
        </div>
      </section>

      <section
        v-show="activeTab === 'qqbot'"
        id="panel-qqbot"
        class="tab-panel bot-console-panel"
        role="tabpanel"
        aria-labelledby="tab-qqbot"
      >
        <section class="bot-console-main" aria-labelledby="qqbot-title">
          <header class="bot-console-head">
            <div>
              <h2 id="qqbot-title">机器人配置</h2>
              <p>统一管理多平台机器人连接、凭证、回调与运行状态</p>
            </div>
            <div class="bot-console-actions">
              <button class="button" type="button" :disabled="startingBot" @click="onStartBot">
                <Power :size="16" aria-hidden="true" />
                <span>{{ startingBot ? "启动中" : "启动" }}</span>
              </button>
              <button class="button" type="button" :disabled="stoppingBot" @click="onStopBot">
                <PowerOff :size="16" aria-hidden="true" />
                <span>{{ stoppingBot ? "停止中" : "停止" }}</span>
              </button>
              <button class="button primary" type="button" @click="createBotProfile">
                <Plus :size="16" aria-hidden="true" />
                <span>新建机器人</span>
              </button>
            </div>
          </header>

          <div class="bot-stat-grid">
            <article class="bot-stat-card">
              <div class="bot-stat-icon running"><Activity :size="22" aria-hidden="true" /></div>
              <div>
                <span>运行中</span>
                <strong>{{ runningBotCount }}</strong>
              </div>
            </article>
            <article class="bot-stat-card">
              <div class="bot-stat-icon connected"><Link2 :size="22" aria-hidden="true" /></div>
              <div>
                <span>已连接</span>
                <strong>{{ connectedBotCount }}</strong>
              </div>
            </article>
            <article class="bot-stat-card">
              <div class="bot-stat-icon disconnected"><Cable :size="22" aria-hidden="true" /></div>
              <div>
                <span>未连接</span>
                <strong>{{ disconnectedBotCount }}</strong>
              </div>
            </article>
            <article class="bot-stat-card">
              <div class="bot-stat-icon synced"><RefreshCw :size="22" aria-hidden="true" /></div>
              <div>
                <span>已同步</span>
                <strong>{{ botSyncLabel }}</strong>
              </div>
            </article>
          </div>

          <div class="bot-list-toolbar">
            <label class="bot-search">
              <Search :size="16" aria-hidden="true" />
              <input v-model.trim="botProfileQuery" autocomplete="off" placeholder="搜索机器人名称或用户名..." />
            </label>
            <div class="bot-platform-filter" role="group" aria-label="平台筛选">
              <span>平台：</span>
              <button
                v-for="option in botPlatformOptions"
                :key="option.value"
                type="button"
                :aria-pressed="botPlatformFilter === option.value"
                @click="setBotPlatformFilter(option.value)"
              >
                {{ option.label }}
              </button>
            </div>
            <button class="button" type="button" @click="refreshBotConfig">
              <RefreshCw :size="16" aria-hidden="true" />
              <span>同步</span>
            </button>
          </div>

          <div class="bot-table-wrap">
            <table class="bot-table">
              <thead>
                <tr>
                  <th scope="col">机器人名称</th>
                  <th scope="col">平台</th>
                  <th scope="col">Endpoint / Webhook</th>
                  <th scope="col">Bot ID / 用户名</th>
                  <th scope="col">凭证状态</th>
                  <th scope="col">回调状态</th>
                  <th scope="col">运行状态</th>
                  <th scope="col">操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="filteredBotProfiles.length === 0">
                  <td class="bot-empty-cell" colspan="8">没有匹配的机器人配置</td>
                </tr>
                <template v-else>
                  <tr
                    v-for="profile in pagedBotProfiles"
                    :key="botProfileKey(profile)"
                    :class="{ active: isSelectedBotProfile(profile) }"
                    @click="editBotProfile(profile)"
                  >
                    <td>
                      <div class="bot-name-cell">
                        <button class="bot-row-selector" type="button" :aria-label="`设为当前 ${profile.name || '机器人'}`" @click.stop="onSelectBotProfile(profile.id || '')">
                          <span :class="{ active: isActiveBotProfile(profile) }"></span>
                        </button>
                        <div class="bot-table-avatar" :class="botPlatformKey(profile)">
                          <img v-if="profile.avatar_url" :src="profile.avatar_url" :alt="profile.name || '机器人头像'" />
                          <component v-else :is="botPlatformIcon(profile)" :size="20" aria-hidden="true" />
                        </div>
                        <div>
                          <strong>{{ profile.name || "未命名机器人" }}</strong>
                          <small>{{ botProfileSubtitle(profile) }}</small>
                        </div>
                      </div>
                    </td>
                    <td>
                      <span class="bot-platform-badge">{{ botProfilePlatformLabel(profile) }}</span>
                    </td>
                    <td class="bot-endpoint-cell">{{ botProfileEndpointDisplay(profile) }}</td>
                    <td class="bot-id-cell">
                      <span>{{ botProfileResolvedID(profile) || "-" }}</span>
                      <small>{{ botProfileUsername(profile) }}</small>
                    </td>
                    <td>
                      <span class="bot-state-pill" :class="botCredentialTone(profile)">{{ botCredentialLabel(profile) }}</span>
                    </td>
                    <td>
                      <span class="bot-state-pill" :class="botCallbackTone(profile)">{{ botCallbackLabel(profile) }}</span>
                    </td>
                    <td>
                      <span class="bot-state-pill" :class="botRuntimeTone(profile)">{{ botRuntimeLabel(profile) }}</span>
                    </td>
                    <td>
                      <div class="bot-table-actions">
                        <button type="button" title="编辑" aria-label="编辑" @click.stop="editBotProfile(profile)">
                          <Pencil :size="15" aria-hidden="true" />
                        </button>
                        <button type="button" title="测试" aria-label="测试" @click.stop="onTestBotConnection(profile)">
                          <Download :size="15" aria-hidden="true" />
                        </button>
                        <button type="button" title="复制" aria-label="复制" :disabled="!profile.id" @click.stop="onCloneBotProfile(profile)">
                          <Copy :size="15" aria-hidden="true" />
                        </button>
                        <label class="bot-table-toggle" @click.stop>
                          <input :checked="profile.enabled" type="checkbox" @change="onToggleBotProfile(profile, $event)" />
                          <span></span>
                        </label>
                      </div>
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>

          <footer class="bot-table-footer">
            <span>共 {{ filteredBotProfiles.length }} 个机器人</span>
            <div class="bot-pagination">
              <button type="button" :disabled="botPage <= 1" aria-label="上一页" @click="setBotPage(botPage - 1)">
                <ChevronLeft :size="16" aria-hidden="true" />
              </button>
              <button
                v-for="page in botPageNumbers"
                :key="page"
                type="button"
                :aria-current="page === botPage ? 'page' : undefined"
                @click="setBotPage(page)"
              >
                {{ page }}
              </button>
              <button type="button" :disabled="botPage >= botPageCount" aria-label="下一页" @click="setBotPage(botPage + 1)">
                <ChevronRight :size="16" aria-hidden="true" />
              </button>
              <label>
                <select v-model.number="botPageSize" @change="setBotPage(1)">
                  <option :value="10">10 条/页</option>
                  <option :value="20">20 条/页</option>
                  <option :value="50">50 条/页</option>
                </select>
                <ChevronDown :size="14" aria-hidden="true" />
              </label>
            </div>
          </footer>
        </section>

        <el-drawer
          v-model="botDetailOpen"
          class="diana-bot-drawer"
          size="min(680px, 96vw)"
          :with-header="false"
          :append-to-body="true"
          :close-on-click-modal="false"
          :close-on-press-escape="!botDirty"
        >
          <aside class="bot-detail-panel" aria-label="机器人详情">
          <header class="bot-detail-head">
            <h3>机器人详情</h3>
            <button class="icon-button" type="button" aria-label="关闭详情" title="关闭详情" @click="closeBotDetail">
              <X :size="16" aria-hidden="true" />
            </button>
          </header>

          <div class="bot-detail-identity">
            <div class="bot-detail-avatar" :class="botPlatformKey(editingBotProfile)">
              <img v-if="botForm.avatarURL" :src="botForm.avatarURL" :alt="botForm.name || '机器人头像'" />
              <component v-else :is="botPlatformIcon(editingBotProfile)" :size="30" aria-hidden="true" />
            </div>
            <div>
              <div class="bot-detail-title-row">
                <h3>{{ botForm.name || "未命名机器人" }}</h3>
                <span class="bot-state-pill" :class="botRuntimeTone(editingBotProfile)">{{ botRuntimeLabel(editingBotProfile) }}</span>
              </div>
              <p>{{ botProfileUsername(editingBotProfile) }}</p>
              <span>同步于 {{ botRecentSyncLabel }}</span>
            </div>
          </div>

          <el-tabs v-model="botDetailTab" class="bot-detail-tabs" aria-label="机器人详情标签">
            <el-tab-pane v-for="tab in botDetailTabs" :key="tab.value" :name="tab.value" :label="tab.label" />
          </el-tabs>

          <form v-if="botDetailTab === 'config'" class="bot-detail-body" @submit.prevent="onSaveBot">
            <label class="bot-detail-row">
              <span>配置名称</span>
              <input v-model.trim="botForm.name" autocomplete="off" placeholder="例如：TG 通知机器人" />
            </label>
            <label class="bot-detail-row">
              <span>平台</span>
              <input v-model.trim="botForm.platform" autocomplete="off" placeholder="Telegram / QQ / Discord" />
            </label>
            <label class="bot-detail-row">
              <span>头像地址</span>
              <input v-model.trim="botForm.avatarURL" autocomplete="off" placeholder="https://..." />
            </label>
            <label class="bot-detail-row">
              <span>Bot Token</span>
              <input
                v-model.trim="botForm.oneBotToken"
                type="password"
                autocomplete="off"
                minlength="16"
                :placeholder="botForm.oneBotTokenConfigured ? '留空沿用已保存 token' : '至少 16 字符'"
              />
            </label>
            <label class="bot-detail-row">
              <span>Webhook URL</span>
              <input v-model.trim="botForm.oneBotEndpoint" autocomplete="off" />
            </label>
            <label class="bot-detail-row">
              <span>Bot ID / 用户名</span>
              <input v-model.trim="botForm.botQQ" autocomplete="off" placeholder="Bot QQ / 用户名" />
            </label>
            <label class="bot-detail-row">
              <span>Owner</span>
              <input v-model.trim="botForm.ownerID" autocomplete="off" />
            </label>
            <label class="bot-detail-row">
              <span>事件订阅</span>
              <input v-model.trim="botForm.groupTriggers" autocomplete="off" />
            </label>
            <label class="bot-detail-row">
              <span>会话范围</span>
              <input v-model.trim="botForm.disabledGroups" autocomplete="off" placeholder="禁用群号，逗号分隔" />
            </label>
            <label class="bot-detail-row">
              <span>屏蔽用户</span>
              <input v-model.trim="botForm.disabledUsers" autocomplete="off" placeholder="QQ 号，逗号分隔" />
            </label>
            <label class="bot-detail-row">
              <span>上下文条数</span>
              <input v-model.number="botForm.recentContextLimit" type="number" min="1" max="80" />
            </label>
            <label class="bot-detail-row">
              <span>压缩阈值</span>
              <input v-model.number="botForm.contextSummaryThreshold" type="number" min="1" max="200" />
            </label>
            <label class="bot-detail-row">
              <span>插话概率</span>
              <input v-model.number="botForm.passiveReplyChance" type="number" min="0.05" max="1" step="0.05" />
            </label>
            <label class="bot-detail-row">
              <span>插话阈值</span>
              <input v-model.number="botForm.passiveReplyThreshold" type="number" min="0.5" max="1" step="0.01" />
            </label>
            <label class="bot-detail-row">
              <span>分段长度</span>
              <el-select v-model="botForm.directReplyChunkSize" aria-label="分段长度">
                <el-option :value="500" label="短纯文本" />
                <el-option :value="900" label="纯文本" />
                <el-option :value="1200" label="长纯文本" />
              </el-select>
            </label>
            <label class="bot-detail-row">
              <span>撤回回复</span>
              <el-select v-model="botForm.recallReplyMode" aria-label="撤回回复方式">
                <el-option value="llm_summary" label="LLM 总结" />
                <el-option value="original_forward" label="原消息合并转发" />
              </el-select>
            </label>
            <label class="bot-detail-row">
              <span>代理 / 网络</span>
              <input v-model.trim="botForm.noneBotBridgeEndpoint" autocomplete="off" />
            </label>

            <div class="bot-detail-switches">
              <label>
                <span>
                  <strong>启用机器人</strong>
                  <small>允许接收并处理消息</small>
                </span>
                <el-switch v-model="botForm.enabled" aria-label="启用机器人" />
              </label>
              <label>
                <span>
                  <strong>启用 Agent</strong>
                  <small>允许模型调用本地工具和浏览器工具</small>
                </span>
                <el-switch v-model="botForm.agentEnabled" aria-label="启用 Agent" />
              </label>
              <label>
                <span>
                  <strong>启用欢迎语</strong>
                  <small>新成员入群时发送欢迎语</small>
                </span>
                <el-switch v-model="botForm.welcomeEnabled" aria-label="启用欢迎语" />
              </label>
              <label>
                <span>
                  <strong>自动撤回查看结果</strong>
                  <small>查看撤回消息的回复发送 1 分钟后自动撤回</small>
                </span>
                <el-switch v-model="botForm.recallReplyAutoDeleteEnabled" aria-label="自动撤回查看结果" />
              </label>
              <label>
                <span>
                  <strong>LLM QQ号脱敏</strong>
                  <small>发送模型前替换 QQ 号，回复和工具执行前在本地还原</small>
                </span>
                <el-switch v-model="botForm.llmQQIDMaskingEnabled" aria-label="LLM QQ号脱敏" />
              </label>
              <label>
                <span>
                  <strong>自动重连</strong>
                  <small>断开后自动尝试重连</small>
                </span>
                <el-switch v-model="botForm.noneBotBridgeEnabled" aria-label="自动重连" />
              </label>
            </div>

            <label class="bot-detail-row">
              <span>Agent 工作目录</span>
              <input v-model.trim="botForm.agentWorkDir" autocomplete="off" placeholder="." />
            </label>
            <label class="bot-detail-row">
              <span>Agent 步数</span>
              <input v-model.number="botForm.agentMaxSteps" type="number" min="1" max="8" step="1" />
            </label>
            <label class="bot-detail-row">
              <span>Skills 目录</span>
              <input v-model.trim="botForm.agentSkillRoots" autocomplete="off" placeholder="skills" />
            </label>
            <label class="bot-detail-row">
              <span>MCP 配置</span>
              <input v-model.trim="botForm.agentMCPConfigPath" autocomplete="off" placeholder=".mcp.json" />
            </label>
            <label class="bot-detail-row">
              <span>命令白名单</span>
              <input v-model.trim="botForm.agentCommandAllowlist" autocomplete="off" placeholder="留空使用默认；逗号分隔" />
            </label>
            <label class="bot-detail-row">
              <span>命令超时 MS</span>
              <input v-model.number="botForm.agentCommandTimeoutMS" type="number" min="1000" max="60000" step="1000" />
            </label>
            <label class="bot-detail-row">
              <span>浏览器 CDP</span>
              <input v-model.trim="botForm.agentBrowserCDPURL" autocomplete="off" placeholder="http://127.0.0.1:9222" />
            </label>
            <label class="bot-detail-row">
              <span>浏览器超时 MS</span>
              <input v-model.number="botForm.agentBrowserTimeoutMS" type="number" min="1000" max="60000" step="1000" />
            </label>

            <label class="bot-detail-textarea">
              <span>欢迎语</span>
              <el-input v-model="botForm.welcomeMessage" type="textarea" :rows="3" resize="vertical" />
            </label>
          </form>

          <form v-else-if="botDetailTab === 'prompts'" class="bot-detail-body bot-prompt-form" @submit.prevent="onSaveBot">
            <label class="bot-prompt-field">
              <span>主系统提示词</span>
              <el-input v-model="botForm.systemPrompt" type="textarea" :rows="12" resize="vertical" aria-label="主系统提示词" />
            </label>
            <label class="bot-prompt-field">
              <span>被动回复路由提示词</span>
              <el-input v-model="botForm.passiveReplyRouterPrompt" type="textarea" :rows="18" resize="vertical" aria-label="被动回复路由提示词" />
            </label>
            <label class="bot-prompt-field">
              <span>被动回复生成提示词</span>
              <el-input v-model="botForm.passiveReplyPrompt" type="textarea" :rows="8" resize="vertical" aria-label="被动回复生成提示词" />
            </label>
          </form>

          <div v-else-if="botDetailTab === 'rules'" class="bot-detail-body reply-rule-list">
            <div class="reply-rule-head">
              <div>
                <strong>回复规则</strong>
                <small>先由路由 prompt 判断，命中后临时切模型或转成语音回复。</small>
              </div>
              <button class="button primary" type="button" @click="addReplyRule">
                <Plus :size="16" aria-hidden="true" />
                <span>新增规则</span>
              </button>
            </div>

            <article v-for="(rule, index) in botForm.replyRules" :key="rule.id" class="reply-rule-card">
              <div class="reply-rule-card-head">
                <label>
                  <span>名称</span>
                  <input v-model.trim="rule.name" autocomplete="off" placeholder="例如：语音回复" />
                </label>
                <div class="reply-rule-actions">
                  <el-switch v-model="rule.enabled" aria-label="启用规则" />
                  <button class="table-action danger" type="button" aria-label="删除规则" title="删除规则" @click="removeReplyRule(index)">
                    <Trash2 :size="16" aria-hidden="true" />
                  </button>
                </div>
              </div>

              <div class="reply-rule-grid">
                <label>
                  <span>命中后动作</span>
                  <el-select v-model="rule.action" aria-label="命中后动作">
                    <el-option label="使用特定模型" value="model" />
                    <el-option label="使用语音回复" value="voice" />
                  </el-select>
                </label>
                <label>
                  <span>特定模型</span>
                  <el-select v-model="rule.llmProfileID" clearable filterable placeholder="沿用当前模型" aria-label="特定模型">
                    <el-option v-for="profile in llmProfiles" :key="profile.id || profile.model" :label="replyRuleProfileLabel(profile)" :value="profile.id || ''" />
                  </el-select>
                </label>
              </div>

              <label class="reply-rule-prompt">
                <span>判断 prompt</span>
                <el-input
                  v-model="rule.prompt"
                  type="textarea"
                  :rows="5"
                  resize="vertical"
                  placeholder="例如：当用户明确要求语音、朗读、念出来，或当前消息更适合语音表达时命中。只判断当前发言，历史消息只能作为参考。"
                />
              </label>
            </article>

            <div v-if="botForm.replyRules.length === 0" class="empty-state plugin-empty">暂无回复规则。</div>
          </div>

          <div v-else-if="botDetailTab === 'events'" class="bot-detail-body bot-event-list">
            <article v-for="event in botRecentEvents" :key="`${event.at}-${event.kind}-${event.user_id || event.group_id}`">
              <span>{{ event.kind || "message" }}</span>
              <strong>{{ event.text || event.reply || event.error || "事件已记录" }}</strong>
              <small>{{ formatLogTime(event.at) }} · {{ event.group_id || event.user_id || "-" }}</small>
            </article>
            <div v-if="botRecentEvents.length === 0" class="empty-state plugin-empty">暂无事件日志。</div>
          </div>

          <div v-else-if="botDetailTab === 'messages'" class="bot-detail-body bot-event-list">
            <article v-for="event in groupTestEvents" :key="`${event.at}-${event.user_id || event.group_id}-${event.text || event.reply}`">
              <span>{{ event.handled ? "已回复" : "收到消息" }}</span>
              <strong>{{ event.text || event.reply || "-" }}</strong>
              <small>{{ formatLogTime(event.at) }} · 群 {{ event.group_id || "-" }}</small>
            </article>
            <div v-if="groupTestEvents.length === 0" class="empty-state plugin-empty">暂无消息记录。</div>
          </div>

          <form v-else class="bot-detail-body bot-test-form" @submit.prevent="onSendGroupTest">
            <label class="bot-detail-row">
              <span>测试群号</span>
              <input v-model.trim="botGroupTest.groupID" autocomplete="off" placeholder="填写要测试的群号" />
            </label>
            <label class="bot-detail-textarea">
              <span>测试消息</span>
              <textarea v-model="botGroupTest.message" />
            </label>
            <div v-if="botGroupTestError" class="group-test-error">
              <TriangleAlert :size="16" aria-hidden="true" />
              <span>{{ botGroupTestError }}</span>
            </div>
            <div class="bot-test-actions">
              <el-button :loading="refreshingGroupTest" :disabled="!botGroupTest.groupID" @click="onRefreshGroupTest">
                <RefreshCw :size="16" aria-hidden="true" />
                <span>{{ refreshingGroupTest ? "刷新中" : "刷新记录" }}</span>
              </el-button>
              <el-button type="primary" native-type="submit" :loading="sendingGroupTest" :disabled="!botFeatures.group_test || !botGroupTest.groupID || !botGroupTest.message.trim()">
                <Send :size="16" aria-hidden="true" />
                <span>{{ sendingGroupTest ? "发送中" : "发送测试" }}</span>
              </el-button>
            </div>
          </form>

          <footer class="bot-detail-actions">
            <el-button @click="onTestBotConnection(editingBotProfile)">
              <Activity :size="16" aria-hidden="true" />
              <span>测试连接</span>
            </el-button>
            <el-button :disabled="!botForm.id" @click="onCloneBotProfile(editingBotProfile)">
              <Copy :size="16" aria-hidden="true" />
              <span>复制配置</span>
            </el-button>
            <el-button type="primary" :loading="savingBot" :disabled="!botDirty" @click="onSaveBot">
              <Save :size="16" aria-hidden="true" />
              <span>{{ savingBot ? "保存中" : "保存配置" }}</span>
            </el-button>
          </footer>
          </aside>
        </el-drawer>
      </section>

      <section
        v-show="activeTab === 'group-admin'"
        id="panel-group-admin"
        class="tab-panel group-admin-panel"
        role="tabpanel"
        aria-labelledby="tab-group-admin"
      >
        <section class="group-admin-surface" aria-labelledby="group-admin-title">
          <header class="group-admin-head">
            <div>
              <h2 id="group-admin-title">群管理</h2>
              <p>机器人主人、群主或管理员验证后，只配置当前群的机器人行为</p>
            </div>
            <div class="group-admin-session" :class="{ ok: groupAdminVerified }">
              <ShieldCheck :size="18" aria-hidden="true" />
              <span>{{ groupAdminVerified ? `已验证 ${groupAdmin.form.groupID}` : "等待验证" }}</span>
            </div>
          </header>

          <div class="group-admin-layout">
            <form class="group-admin-auth" @submit.prevent="onVerifyGroupAdmin">
              <div class="group-admin-section-title">
                <Users :size="20" aria-hidden="true" />
                <div>
                  <h3>管理员验证</h3>
                  <span>验证码会发送到你的 QQ 私聊</span>
                </div>
              </div>

              <label class="group-admin-field">
                <span>QQ群号</span>
                <input v-model.trim="groupAdmin.form.groupID" autocomplete="off" inputmode="numeric" placeholder="例如：123456" />
              </label>
              <label class="group-admin-field">
                <span>管理员 QQ</span>
                <input v-model.trim="groupAdmin.form.userID" autocomplete="off" inputmode="numeric" placeholder="填写你的 QQ 号" />
              </label>
              <div class="group-admin-code-row">
                <label class="group-admin-field">
                  <span>验证码</span>
                  <input v-model.trim="groupAdmin.form.code" autocomplete="one-time-code" inputmode="numeric" placeholder="6 位数字" />
                </label>
                <button class="button" type="button" :disabled="groupAdmin.sendingCode || !groupAdmin.form.groupID || !groupAdmin.form.userID" @click="onRequestGroupAdminCode">
                  <Send :size="16" aria-hidden="true" />
                  <span>{{ groupAdmin.sendingCode ? "发送中" : "发送验证码" }}</span>
                </button>
              </div>
              <button class="button primary group-admin-full-button" type="submit" :disabled="groupAdmin.verifying || !groupAdmin.form.groupID || !groupAdmin.form.userID || !groupAdmin.form.code">
                <ShieldCheck :size="16" aria-hidden="true" />
                <span>{{ groupAdmin.verifying ? "验证中" : "验证并载入配置" }}</span>
              </button>

              <div v-if="groupAdmin.notice || groupAdmin.error" class="group-admin-message" :class="{ bad: Boolean(groupAdmin.error) }">
                <component :is="groupAdmin.error ? TriangleAlert : CheckCircle2" :size="17" aria-hidden="true" />
                <span>{{ groupAdmin.error || groupAdmin.notice }}</span>
              </div>
            </form>

            <form class="group-admin-config" :class="{ disabled: !groupAdminVerified }" @submit.prevent="onSaveGroupAdminConfig">
              <div class="group-admin-section-title">
                <ListChecks :size="20" aria-hidden="true" />
                <div>
                  <h3>本群功能</h3>
                  <span>{{ groupAdminVerified ? groupAdminSessionLabel : "验证后可编辑" }}</span>
                </div>
              </div>

              <fieldset :disabled="!groupAdminVerified || groupAdmin.saving">
                <label class="group-admin-toggle-row">
                  <span>
                    <strong>启用本群机器人</strong>
                    <small>关闭后此群不会触发回复和欢迎语</small>
                  </span>
                  <input v-model="groupAdmin.config.enabled" type="checkbox" />
                </label>
                <label class="group-admin-toggle-row">
                  <span>
                    <strong>入群欢迎语</strong>
                    <small>成员加入时发送欢迎消息</small>
                  </span>
                  <input v-model="groupAdmin.config.welcome_enabled" type="checkbox" />
                </label>

                <label class="group-admin-field">
                  <span>触发词</span>
                  <input v-model.trim="groupAdmin.form.triggers" autocomplete="off" placeholder="嘉然, 然然, Diana" />
                </label>
                <label class="group-admin-field">
                  <span>欢迎语</span>
                  <textarea v-model="groupAdmin.config.welcome_message" rows="3" placeholder="欢迎 {user_id} 加入本群" />
                </label>

                <div class="group-admin-number-grid">
                  <label class="group-admin-field">
                    <span>上下文条数</span>
                    <input v-model.number="groupAdmin.config.recent_context_limit" type="number" min="1" max="80" />
                  </label>
                  <label class="group-admin-field">
                    <span>最长回复字数</span>
                    <input v-model.number="groupAdmin.config.max_reply_chars" type="number" min="100" max="8000" step="100" />
                  </label>
                  <label class="group-admin-field">
                    <span>插话概率</span>
                    <input v-model.number="groupAdmin.config.passive_reply_chance" type="number" min="0.05" max="1" step="0.05" />
                  </label>
                  <label class="group-admin-field">
                    <span>回复阈值</span>
                    <input v-model.number="groupAdmin.config.passive_reply_threshold" type="number" min="0.5" max="1" step="0.01" />
                  </label>
                  <label class="group-admin-field">
                    <span>最低回复群等级</span>
                    <input v-model.number="groupAdmin.config.minimum_reply_member_level" type="number" min="0" max="1000" step="1" />
                  </label>
                </div>

                <div class="group-admin-plugin-box">
                  <div class="group-admin-plugin-head">
                    <strong>本群插件开关</strong>
                    <span>只覆盖已安装插件</span>
                  </div>
                  <div class="group-admin-plugin-list">
                    <label v-for="plugin in groupAdmin.plugins" :key="plugin.manifest.id" class="group-admin-plugin-row">
                      <span>
                        <strong>{{ plugin.manifest.name }}</strong>
                        <small>{{ plugin.manifest.id }}</small>
                      </span>
                      <input
                        type="checkbox"
                        :checked="groupAdminPluginEnabled(plugin)"
                        :disabled="!plugin.installed"
                        @change="setGroupAdminPluginOverride(plugin.manifest.id, ($event.target as HTMLInputElement).checked)"
                      />
                    </label>
                    <div v-if="groupAdmin.plugins.length === 0" class="empty-state plugin-empty">暂无插件。</div>
                  </div>
                </div>
              </fieldset>

              <footer class="group-admin-actions">
                <button class="button" type="button" :disabled="!groupAdminVerified || groupAdmin.loading" @click="onRefreshGroupAdminConfig">
                  <RefreshCw :size="16" aria-hidden="true" />
                  <span>{{ groupAdmin.loading ? "刷新中" : "刷新" }}</span>
                </button>
                <button class="button primary" type="submit" :disabled="!groupAdminVerified || groupAdmin.saving">
                  <Save :size="16" aria-hidden="true" />
                  <span>{{ groupAdmin.saving ? "保存中" : "保存本群配置" }}</span>
                </button>
              </footer>
            </form>
          </div>
        </section>
      </section>

      <section
        v-show="activeTab === 'plugins'"
        id="panel-plugins"
        class="tab-panel plugin-center-panel"
        role="tabpanel"
        aria-labelledby="tab-plugins"
      >
        <div class="plugin-center-layout" :class="{ 'detail-open': pluginDetailOpen && activePlugin }">
          <section class="plugin-market-surface" aria-labelledby="plugins-title">
            <div class="plugin-center-head">
              <div>
                <p class="eyebrow">Plugins</p>
                <h2 id="plugins-title">插件中心</h2>
                <p>管理机器人插件、能力扩展与启用状态</p>
              </div>
              <div class="plugin-stat-grid">
                <div class="plugin-stat-card ok">
                  <strong>{{ installedPluginCount }}</strong>
                  <span>已安装</span>
                </div>
                <div class="plugin-stat-card ok">
                  <strong>{{ enabledPluginCount }}</strong>
                  <span>已启用</span>
                </div>
                <div class="plugin-stat-card info">
                  <strong>{{ updatablePluginCount }}</strong>
                  <span>可更新</span>
                </div>
                <button class="icon-button" type="button" :disabled="pluginBusy === '__refresh__'" aria-label="刷新插件" title="刷新插件" @click="onRefreshPlugins">
                  <RefreshCw :size="16" aria-hidden="true" />
                </button>
              </div>
            </div>

            <div class="plugin-center-toolbar">
              <label class="toolbar-search plugin-search">
                <Search :size="15" aria-hidden="true" />
                <input v-model.trim="pluginQuery" autocomplete="off" placeholder="搜索插件名称、ID、说明" />
              </label>
              <div class="segmented plugin-filter-tabs" role="group" aria-label="Plugin filter">
                <button type="button" :aria-pressed="pluginFilter === 'all'" @click="pluginFilter = 'all'">全部</button>
                <button type="button" :aria-pressed="pluginFilter === 'installed'" @click="pluginFilter = 'installed'">已安装</button>
                <button type="button" :aria-pressed="pluginFilter === 'enabled'" @click="pluginFilter = 'enabled'">已启用</button>
                <button type="button" :aria-pressed="pluginFilter === 'official'" @click="pluginFilter = 'official'">官方</button>
                <button type="button" :aria-pressed="pluginFilter === 'community'" @click="pluginFilter = 'community'">第三方</button>
              </div>
              <label class="plugin-category-select" aria-label="插件分类">
                <select v-model="pluginCategory">
                  <option v-for="option in pluginCategoryOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
                </select>
                <ChevronDown :size="15" aria-hidden="true" />
              </label>
              <button class="button primary plugin-install-button" type="button" @click="onInstallSelectedPlugin">
                <Plus :size="16" aria-hidden="true" />
                <span>安装插件</span>
              </button>
            </div>

            <div class="plugin-card-grid">
              <div v-if="filteredPlugins.length === 0" class="empty-state plugin-empty">没有匹配的插件。</div>
              <article
                v-for="plugin in filteredPlugins"
                :key="plugin.manifest.id"
                class="plugin-card"
                :class="{ active: activePlugin?.manifest.id === plugin.manifest.id }"
                @click="selectPlugin(plugin)"
              >
                <div class="plugin-card-icon" :class="pluginTone(plugin)">
                  <component :is="pluginIcon(plugin)" :size="28" aria-hidden="true" />
                </div>
                <div class="plugin-card-main">
                  <div class="plugin-card-title">
                    <h3>{{ plugin.manifest.name }}</h3>
                    <div class="plugin-card-tags">
                      <span v-for="tag in pluginTags(plugin)" :key="tag">{{ tag }}</span>
                    </div>
                  </div>
                  <p>{{ plugin.manifest.description }}</p>
                  <div class="plugin-card-meta">
                    <span>{{ plugin.manifest.id }}</span>
                    <span>v{{ plugin.manifest.version || "0.0.0" }}</span>
                    <span>作者：{{ pluginAuthor(plugin) }}</span>
                  </div>
                  <div class="plugin-card-actions">
                    <button class="plugin-mini-button" type="button" @click.stop="selectPlugin(plugin)">
                      <Wrench :size="14" aria-hidden="true" />
                      <span>设置</span>
                    </button>
                    <button class="plugin-mini-button" type="button" @click.stop="selectPlugin(plugin)">
                      <ListChecks :size="14" aria-hidden="true" />
                      <span>详情</span>
                    </button>
                    <button
                      v-if="pluginNeedsUpdate(plugin)"
                      class="plugin-mini-button info"
                      type="button"
                      @click.stop="onPluginUpdate(plugin)"
                    >
                      <RefreshCw :size="14" aria-hidden="true" />
                      <span>更新</span>
                    </button>
                    <button class="plugin-mini-button icon-only" type="button" aria-label="更多操作" title="更多操作" @click.stop="selectPlugin(plugin)">
                      <MoreHorizontal :size="15" aria-hidden="true" />
                    </button>
                  </div>
                </div>
                <div class="plugin-card-side">
                  <span class="plugin-status-pill" :class="pluginStatusKind(plugin)">{{ pluginStatusLabel(plugin) }}</span>
                  <label class="plugin-toggle" :aria-label="`${plugin.manifest.name} 启用状态`">
                    <input
                      type="checkbox"
                      :checked="plugin.enabled"
                      :disabled="!plugin.installed || pluginBusy === plugin.manifest.id"
                      @click.stop
                      @change="onTogglePlugin(plugin, ($event.target as HTMLInputElement).checked)"
                    />
                    <span />
                  </label>
                </div>
              </article>
            </div>

            <div class="plugin-center-footer">
              <span>共 {{ filteredPlugins.length }} 个插件</span>
              <div class="plugin-footer-controls">
                <label class="plugin-page-size">
                  <span>每页</span>
                  <select aria-label="每页插件数量" disabled>
                    <option>8</option>
                  </select>
                  <span>条</span>
                </label>
                <div class="plugin-pagination">
                  <button class="icon-button" type="button" disabled aria-label="上一页">
                    <ChevronLeft :size="16" aria-hidden="true" />
                  </button>
                  <strong>1</strong>
                  <button class="icon-button" type="button" disabled aria-label="下一页">
                    <ChevronDown :size="16" aria-hidden="true" class="rotate-left" />
                  </button>
                </div>
                <label class="plugin-page-jump">
                  <span>前往</span>
                  <input value="1" inputmode="numeric" aria-label="跳转页码" disabled />
                  <span>页</span>
                </label>
              </div>
            </div>
          </section>

          <aside v-if="pluginDetailOpen && activePlugin" class="plugin-detail-panel" aria-label="插件详情">
            <div class="plugin-detail-head">
              <h3>{{ activePlugin.manifest.name }}</h3>
              <button class="icon-button" type="button" aria-label="关闭详情" title="关闭详情" @click="closePluginDetail">
                <X :size="16" aria-hidden="true" />
              </button>
            </div>

            <div class="plugin-detail-identity">
              <div class="plugin-card-icon plugin-detail-icon" :class="pluginTone(activePlugin)">
                <component :is="pluginIcon(activePlugin)" :size="34" aria-hidden="true" />
              </div>
              <div>
                <h4>{{ activePlugin.manifest.name }}</h4>
                <p>v{{ activePlugin.manifest.version || "0.0.0" }} · {{ activePlugin.manifest.id }} · {{ pluginSourceLabel(activePlugin) }}</p>
              </div>
              <span class="plugin-status-pill" :class="pluginStatusKind(activePlugin)">{{ pluginStatusLabel(activePlugin) }}</span>
              <label class="plugin-toggle">
                <input
                  type="checkbox"
                  :checked="activePlugin.enabled"
                  :disabled="!activePlugin.installed || pluginBusy === activePlugin.manifest.id"
                  @change="onTogglePlugin(activePlugin, ($event.target as HTMLInputElement).checked)"
                />
                <span />
              </label>
            </div>

            <p class="plugin-detail-description">{{ activePlugin.manifest.description }}</p>

            <div class="plugin-detail-section">
              <h4>
                <ShieldCheck :size="15" aria-hidden="true" />
                <span>权限要求</span>
              </h4>
              <ul class="plugin-permission-list">
                <li v-for="permission in pluginPermissionLabels(activePlugin)" :key="permission">
                  <CheckCircle2 :size="14" aria-hidden="true" />
                  <span>{{ permission }}</span>
                </li>
              </ul>
            </div>

            <div class="plugin-detail-section">
              <h4>
                <TerminalSquare :size="15" aria-hidden="true" />
                <span>支持的命令</span>
              </h4>
              <div class="plugin-command-list">
                <div v-for="command in pluginCommandHints(activePlugin)" :key="command.command">
                  <code>{{ command.command }}</code>
                  <span>{{ command.description }}</span>
                </div>
              </div>
            </div>

            <div class="plugin-detail-section">
              <h4>
                <Wrench :size="15" aria-hidden="true" />
                <span>配置信息</span>
              </h4>
              <div class="plugin-setting-list">
                <div v-for="row in pluginSettingRows(activePlugin)" :key="row.label" class="plugin-setting-row">
                  <span>{{ row.label }}</span>
                  <select v-if="row.type === 'select'" :value="row.value" aria-label="插件设置">
                    <option v-for="option in row.options || []" :key="option" :value="option">{{ option }}</option>
                  </select>
                  <label v-else-if="row.type === 'switch'" class="plugin-toggle plugin-toggle-small">
                    <input type="checkbox" :checked="row.enabled" />
                    <span />
                  </label>
                  <input v-else-if="row.type === 'number'" :value="row.value" type="number" min="0" />
                  <div v-else class="plugin-setting-chips">
                    <span v-for="chip in row.chips || []" :key="chip">{{ chip }}</span>
                  </div>
                </div>
              </div>
            </div>

            <div class="plugin-detail-section">
              <h4>
                <ClipboardCheck :size="15" aria-hidden="true" />
                <span>插件信息</span>
              </h4>
              <dl class="plugin-detail-list">
                <div v-for="row in pluginInfoRows(activePlugin)" :key="row.label">
                  <dt>{{ row.label }}</dt>
                  <dd>{{ row.value }}</dd>
                </div>
              </dl>
            </div>

            <div class="plugin-detail-actions">
              <button class="button danger" type="button" :disabled="pluginBusy === activePlugin.manifest.id" @click="onDetailPrimaryPluginAction(activePlugin)">
                <PowerOff v-if="activePlugin.enabled" :size="16" aria-hidden="true" />
                <Power v-else :size="16" aria-hidden="true" />
                <span>{{ pluginDetailPrimaryLabel(activePlugin) }}</span>
              </button>
              <button class="button" type="button" @click="onResetPluginSettings(activePlugin)">
                <RotateCcw :size="16" aria-hidden="true" />
                <span>恢复默认</span>
              </button>
              <button class="button primary" type="button" @click="onSavePluginSettings(activePlugin)">
                <Save :size="16" aria-hidden="true" />
                <span>保存设置</span>
              </button>
            </div>
          </aside>
        </div>
      </section>

      <section
        v-show="activeTab === 'web-search'"
        id="panel-web-search"
        class="panel tab-panel web-search-panel"
        role="tabpanel"
        aria-labelledby="tab-web-search"
      >
        <div class="web-search-head">
          <div>
            <p class="eyebrow">Web Search</p>
            <h2 id="web-search-title">联网搜索</h2>
          </div>
          <div class="web-search-actions">
            <span class="web-search-count">{{ webSearchConfig.providers.length }} 个配置</span>
            <button class="icon-button" type="button" :disabled="loadingWebSearch || savingWebSearch" aria-label="刷新搜索配置" title="刷新搜索配置" @click="refreshWebSearchConfig">
              <RefreshCw :size="16" aria-hidden="true" />
            </button>
            <button class="button" type="button" :disabled="savingWebSearch" @click="addWebSearchProvider">
              <Plus :size="16" aria-hidden="true" />
              <span>添加配置</span>
            </button>
            <button class="button primary" type="button" :disabled="savingWebSearch || !webSearchDirty || webSearchConfig.providers.length === 0" @click="onSaveWebSearchConfig">
              <Save :size="16" aria-hidden="true" />
              <span>{{ savingWebSearch ? "保存中" : "保存配置" }}</span>
            </button>
          </div>
        </div>

        <div v-if="webSearchConfig.overridden_by_env" class="web-search-warning">
          <TriangleAlert :size="17" aria-hidden="true" />
          <span>当前运行环境设置了内联配置，文件改动暂不会生效。</span>
        </div>

        <div class="web-search-test-bar">
          <label>
            <Search :size="16" aria-hidden="true" />
            <input v-model.trim="webSearchTestQuery" autocomplete="off" placeholder="连通测试搜索词" />
          </label>
          <span>从上到下依次回退</span>
        </div>

        <div class="web-search-provider-list">
          <article v-for="(provider, index) in webSearchConfig.providers" :key="`${index}-${provider.name}`" class="web-search-provider" :class="{ disabled: provider.disabled }">
            <header class="web-search-provider-head">
              <div class="web-search-priority">
                <strong>{{ index + 1 }}</strong>
                <span>{{ index === 0 ? "首选" : `回退 ${index}` }}</span>
              </div>
              <div class="web-search-provider-identity">
                <strong>{{ provider.name || "未命名配置" }}</strong>
                <span>{{ webSearchProviderTypeLabel(provider.type) }}</span>
              </div>
              <span class="web-search-key-state" :class="{ ok: webSearchProviderReady(provider), warning: !webSearchProviderReady(provider) }">
                {{ webSearchProviderKeyLabel(provider) }}
              </span>
              <div class="web-search-provider-actions">
                <button class="table-action" type="button" :disabled="index === 0" aria-label="提高优先级" title="提高优先级" @click="moveWebSearchProvider(index, -1)">
                  <ArrowUp :size="16" aria-hidden="true" />
                </button>
                <button class="table-action" type="button" :disabled="index === webSearchConfig.providers.length - 1" aria-label="降低优先级" title="降低优先级" @click="moveWebSearchProvider(index, 1)">
                  <ArrowDown :size="16" aria-hidden="true" />
                </button>
                <button class="table-action" type="button" :disabled="testingWebSearchIndex >= 0 || !webSearchTestQuery" aria-label="测试配置" title="测试配置" @click="onTestWebSearchProvider(provider, index)">
                  <Activity :size="16" aria-hidden="true" />
                </button>
                <button class="table-action danger" type="button" :disabled="webSearchConfig.providers.length <= 1" aria-label="删除配置" title="删除配置" @click="removeWebSearchProvider(index)">
                  <Trash2 :size="16" aria-hidden="true" />
                </button>
                <label class="plugin-toggle" :aria-label="`${provider.name || '搜索配置'}启用状态`">
                  <input v-model="provider.disabled" type="checkbox" :true-value="false" :false-value="true" />
                  <span />
                </label>
              </div>
            </header>

            <div class="web-search-provider-grid">
              <label>
                <span>配置名称</span>
                <input v-model.trim="provider.name" autocomplete="off" placeholder="例如 exa-primary" />
              </label>
              <label>
                <span>类型</span>
                <select v-model="provider.type" @change="onWebSearchProviderTypeChange(provider)">
                  <option value="exa_mcp">Exa MCP</option>
                  <option value="tavily">Tavily</option>
                </select>
              </label>
              <label class="web-search-url-field">
                <span>服务地址</span>
                <input v-model.trim="provider.url" inputmode="url" autocomplete="off" />
              </label>
              <label v-if="provider.type === 'exa_mcp'">
                <span>MCP 工具</span>
                <select v-model="provider.tool">
                  <option value="web_search_exa">web_search_exa</option>
                  <option value="web_search_advanced_exa">web_search_advanced_exa</option>
                </select>
              </label>
              <label>
                <span>密钥环境变量</span>
                <input v-model.trim="provider.api_key_env" autocomplete="off" :placeholder="provider.type === 'tavily' ? 'TAVILY_API_KEY' : '可留空'" />
              </label>
              <label>
                <span>单次超时</span>
                <div class="web-search-number-input">
                  <input v-model.number="provider.timeout_ms" type="number" min="1000" max="30000" step="1000" />
                  <span>ms</span>
                </div>
              </label>
              <label>
                <span>结果数</span>
                <input v-model.number="provider.max_results" type="number" min="1" max="10" />
              </label>
            </div>

            <div v-if="webSearchTestResult.index === index" class="web-search-test-result" :class="webSearchTestResult.ok ? 'ok' : 'bad'">
              <component :is="webSearchTestResult.ok ? CheckCircle2 : TriangleAlert" :size="16" aria-hidden="true" />
              <div>
                <strong>{{ webSearchTestResult.ok ? `测试成功 · ${webSearchTestResult.durationMS}ms` : "测试失败" }}</strong>
                <pre>{{ webSearchTestResult.text }}</pre>
              </div>
            </div>
          </article>

          <div v-if="webSearchConfig.providers.length === 0" class="empty-state plugin-empty">暂无搜索配置。</div>
        </div>
      </section>

      <section
        v-show="activeTab === 'logs'"
        id="panel-logs"
        class="panel tab-panel log-center-panel"
        role="tabpanel"
        aria-labelledby="tab-logs"
      >
        <section class="log-center-surface" aria-labelledby="logs-title">
          <div class="log-center-head">
            <div>
              <h2 id="logs-title">日志中心</h2>
              <span>LOGS</span>
            </div>
            <div class="log-center-head-meta">
              <FileClock :size="16" aria-hidden="true" />
              <span>{{ latestAppLog ? `最后更新 ${formatLogTableTime(latestAppLog.created_at)}` : "等待日志" }}</span>
            </div>
          </div>

          <div class="log-control-row">
            <div class="log-tabs" role="group" aria-label="日志类型">
              <button
                v-for="option in logViewOptions"
                :key="option.value"
                class="log-tab"
                type="button"
                :aria-pressed="logView === option.value"
                @click="selectLogView(option.value)"
              >
                <span>{{ option.label }}</span>
                <strong>{{ option.count }}</strong>
              </button>
            </div>

            <div class="log-filters">
              <label class="log-search">
                <Search :size="16" aria-hidden="true" />
                <input v-model.trim="logQuery" autocomplete="off" placeholder="搜索日志内容、模块或关键词..." />
              </label>
              <label class="log-select">
                <span class="sr-only">日志级别</span>
                <select v-model="logLevelFilter">
                  <option v-for="option in logLevelOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
                </select>
                <ChevronDown :size="15" aria-hidden="true" />
              </label>
              <div class="log-date-range" aria-label="日期范围">
                <input v-model="logStartDate" type="date" aria-label="开始日期" />
                <span>→</span>
                <input v-model="logEndDate" type="date" aria-label="结束日期" />
                <CalendarDays :size="15" aria-hidden="true" />
              </div>
              <button class="button log-refresh-button" type="button" :disabled="loadingLogs" @click="onRefreshLogs">
                <RefreshCw :size="16" aria-hidden="true" />
                <span>{{ loadingLogs ? "刷新中" : "刷新" }}</span>
              </button>
            </div>
          </div>

          <div class="log-table-wrap">
            <table class="log-table">
              <thead>
                <tr>
                  <th scope="col">时间</th>
                  <th scope="col">级别</th>
                  <th scope="col">模块 / 来源</th>
                  <th scope="col">日志内容</th>
                  <th scope="col">配置摘要</th>
                  <th scope="col">环境 / 来源</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="loadingLogs">
                  <td class="log-empty-cell" colspan="6">读取中</td>
                </tr>
                <tr v-else-if="filteredAppLogs.length === 0">
                  <td class="log-empty-cell" colspan="6">暂无匹配日志</td>
                </tr>
                <tr v-for="entry in pagedAppLogs" v-else :key="entry.id">
                  <td class="log-time-cell">{{ formatLogTableTime(entry.created_at) }}</td>
                  <td>
                    <span class="log-level-badge" :class="logLevelClass(entry)">{{ logLevelLabel(entry) }}</span>
                  </td>
                  <td class="log-module-cell">{{ logModuleLabel(entry) }}</td>
                  <td class="log-content-cell">
                    <span>{{ logContent(entry) }}</span>
                    <small v-if="entry.detail && entry.detail !== entry.message">{{ entry.detail }}</small>
                  </td>
                  <td>
                    <div class="log-config-chips">
                      <span v-for="chip in logConfigChips(entry)" :key="chip">{{ chip }}</span>
                    </div>
                  </td>
                  <td class="log-source-cell">{{ logSourceLabel(entry) }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div class="log-center-footer">
            <span>共 {{ filteredAppLogs.length }} 条记录</span>
            <div class="log-footer-controls">
              <label class="log-page-size">
                <select v-model.number="logPageSize" @change="onLogPageSizeChange">
                  <option v-for="size in logPageSizeOptions" :key="size" :value="size">{{ size }}</option>
                </select>
                <span>条/页</span>
              </label>
              <div class="log-pagination" aria-label="日志分页">
                <button class="log-page-button" type="button" :disabled="logPage <= 1" aria-label="上一页" @click="setLogPage(logPage - 1)">
                  <ChevronLeft :size="16" aria-hidden="true" />
                </button>
                <button
                  v-for="page in logPageNumbers"
                  :key="page"
                  class="log-page-button"
                  type="button"
                  :aria-current="page === logPage ? 'page' : undefined"
                  @click="setLogPage(page)"
                >
                  {{ page }}
                </button>
                <button class="log-page-button" type="button" :disabled="logPage >= logPageCount" aria-label="下一页" @click="setLogPage(logPage + 1)">
                  <ChevronRight :size="16" aria-hidden="true" />
                </button>
              </div>
              <label class="log-jump">
                <span>前往</span>
                <input :value="logPage" inputmode="numeric" aria-label="跳转页码" @change="onLogJumpChange" />
                <span>页</span>
              </label>
            </div>
          </div>
        </section>
      </section>

      <section
        v-show="activeTab === 'security'"
        id="panel-security"
        class="panel tab-panel"
        role="tabpanel"
        aria-labelledby="tab-security"
      >
        <div class="panel-head">
          <div>
            <p class="eyebrow">Access</p>
            <h2 id="security-title">访问设置</h2>
          </div>
          <div class="panel-head-meta">
            <div class="model-chip">
              <ShieldCheck :size="15" aria-hidden="true" />
              <span>{{ adminAccessSettings.configured ? "账号密码已启用" : "未启用登录" }}</span>
            </div>
            <div v-if="adminAccessSettings.managed_by_environment" class="status">
              <span>环境变量托管</span>
            </div>
          </div>
        </div>

        <div class="access-settings-layout">
          <section class="access-settings-surface" aria-labelledby="admin-account-title">
            <div class="access-section-head">
              <div>
                <h3 id="admin-account-title">管理员账号</h3>
                <span>{{ adminAccessSettings.username || "未设置邮箱" }}</span>
              </div>
              <UserRound :size="18" aria-hidden="true" />
            </div>

            <form class="access-form" @submit.prevent="onUpdateAdminEmail">
              <label class="field">
                <span>邮箱</span>
                <input v-model.trim="adminAccountForm.email" type="email" autocomplete="username" required />
              </label>
              <label class="field">
                <span>当前密码</span>
                <input v-model="adminAccountForm.currentPassword" type="password" autocomplete="current-password" required />
              </label>
              <div class="actions access-settings-actions">
                <button class="button primary" type="submit" :disabled="savingAdminAccount || !adminAccountForm.email || !adminAccountForm.currentPassword">
                  <Save :size="16" aria-hidden="true" />
                  <span>{{ savingAdminAccount ? "保存中" : "保存邮箱" }}</span>
                </button>
              </div>
            </form>

            <div class="access-divider" />

            <form class="access-form" @submit.prevent="onChangeAdminPassword">
              <div class="access-form-title">
                <KeyRound :size="17" aria-hidden="true" />
                <strong>修改密码</strong>
              </div>
              <div class="access-password-grid">
                <label class="field">
                  <span>当前密码</span>
                  <input v-model="adminPasswordForm.currentPassword" type="password" autocomplete="current-password" required />
                </label>
                <label class="field">
                  <span>新密码</span>
                  <input v-model="adminPasswordForm.newPassword" type="password" autocomplete="new-password" minlength="12" required />
                </label>
                <label class="field">
                  <span>确认新密码</span>
                  <input v-model="adminPasswordForm.passwordConfirm" type="password" autocomplete="new-password" minlength="12" required />
                </label>
              </div>
              <div class="actions access-settings-actions">
                <button class="button primary" type="submit" :disabled="changingAdminPassword || !adminPasswordReady">
                  <KeyRound :size="16" aria-hidden="true" />
                  <span>{{ changingAdminPassword ? "修改中" : "修改密码" }}</span>
                </button>
              </div>
            </form>
          </section>

          <section class="access-settings-surface" aria-labelledby="admin-sessions-title">
            <div class="access-section-head">
              <div>
                <h3 id="admin-sessions-title">登录设备</h3>
                <span>{{ adminSessions.length }} 个有效会话</span>
              </div>
              <button class="button" type="button" :disabled="loadingAdminSessions || Boolean(revokingAdminSession) || adminSessions.length <= 1" @click="onRevokeOtherAdminSessions">
                <LogOut :size="16" aria-hidden="true" />
                <span>退出其他设备</span>
              </button>
            </div>

            <div v-if="loadingAdminSessions" class="access-empty-state">正在读取设备</div>
            <div v-else-if="adminSessions.length === 0" class="access-empty-state">没有有效设备会话</div>
            <div v-else class="access-session-list">
              <article v-for="session in adminSessions" :key="session.id" class="access-session-row">
                <div class="access-session-icon"><Monitor :size="18" aria-hidden="true" /></div>
                <div class="access-session-main">
                  <div>
                    <strong>{{ session.device_name || "未知设备" }}</strong>
                    <span v-if="session.current" class="access-current-badge">当前设备</span>
                  </div>
                  <span>{{ session.ip_address || "未知 IP" }} · 最近活动 {{ formatAdminSessionTime(session.last_seen_at) }}</span>
                  <small>有效期至 {{ formatAdminSessionTime(session.expires_at) }}</small>
                </div>
                <button
                  class="icon-button"
                  type="button"
                  :disabled="revokingAdminSession === session.id"
                  :aria-label="session.current ? '退出当前设备' : '吊销设备登录'"
                  :title="session.current ? '退出当前设备' : '吊销设备登录'"
                  @click="onRevokeAdminSession(session)"
                >
                  <LogOut v-if="session.current" :size="16" aria-hidden="true" />
                  <Trash2 v-else :size="16" aria-hidden="true" />
                </button>
              </article>
            </div>
          </section>

          <section class="access-settings-surface" aria-labelledby="admin-access-title">
            <div class="access-setting-row">
              <div>
                <h3 id="admin-access-title">随机登录入口</h3>
                <span>{{ adminAccessSettings.random_suffix_enabled ? "已启用" : "已关闭" }}</span>
              </div>
              <label class="plugin-toggle" aria-label="随机登录入口">
                <input
                  v-model="adminAccessSettings.random_suffix_enabled"
                  type="checkbox"
                  :disabled="savingAdminAccess || adminAccessSettings.managed_by_environment"
                />
                <span />
              </label>
            </div>

            <div class="access-path-block">
              <span>当前登录入口</span>
              <div class="access-path-row">
                <code>{{ adminLoginURL }}</code>
                <button class="icon-button" type="button" aria-label="复制登录入口" title="复制登录入口" @click="copyAdminLoginURL">
                  <Copy :size="16" aria-hidden="true" />
                </button>
              </div>
            </div>

            <div class="actions access-settings-actions">
              <button
                v-if="adminAccessSettings.random_suffix_enabled"
                class="button"
                type="button"
                :disabled="savingAdminAccess || adminAccessSettings.managed_by_environment"
                @click="onSaveAdminAccess(true)"
              >
                <RefreshCw :size="16" aria-hidden="true" />
                <span>重新生成</span>
              </button>
              <button
                class="button primary"
                type="button"
                :disabled="savingAdminAccess || adminAccessSettings.managed_by_environment"
                @click="onSaveAdminAccess(false)"
              >
                <Save :size="16" aria-hidden="true" />
                <span>{{ savingAdminAccess ? "保存中" : "保存设置" }}</span>
              </button>
            </div>
          </section>
        </div>
      </section>

      <section
        v-show="activeTab === 'theme'"
        id="panel-theme"
        class="panel tab-panel"
        role="tabpanel"
        aria-labelledby="tab-theme"
      >
        <div class="panel-head">
          <div>
            <p class="eyebrow">Theme</p>
            <h2 id="theme-title">主题配置</h2>
          </div>
          <div class="panel-head-meta">
            <div class="model-chip">
              <Sparkles :size="15" aria-hidden="true" />
              <span>{{ themeModeLabel }}</span>
            </div>
            <div class="status" :class="themePreferences.shadows ? 'ok' : ''">
              <span>{{ themePreferences.shadows ? "阴影已开启" : "阴影已关闭" }}</span>
            </div>
          </div>
        </div>

        <div class="theme-layout">
          <section class="theme-settings-card">
            <div class="theme-group">
              <div class="theme-group-head">
                <h3>主题模式</h3>
                <p>支持跟随系统、浅色与深色三种模式。</p>
              </div>
              <div class="segmented theme-mode-segment" role="group" aria-label="Theme mode">
                <button type="button" :aria-pressed="themeMode === 'system'" @click="setThemeMode('system')">跟随系统</button>
                <button type="button" :aria-pressed="themeMode === 'light'" @click="setThemeMode('light')">浅色模式</button>
                <button type="button" :aria-pressed="themeMode === 'dark'" @click="setThemeMode('dark')">深色模式</button>
              </div>
            </div>

            <div class="theme-group">
              <div class="theme-group-head">
                <h3>主题色</h3>
                <p>切换主按钮、强调态、选中态与提示色。</p>
              </div>
              <div class="theme-swatch-grid">
                <button
                  v-for="accent in themeAccentOptions"
                  :key="accent.id"
                  class="theme-swatch"
                  :class="{ active: themePreferences.accent === accent.id }"
                  type="button"
                  :style="{ '--theme-swatch': accent.primary }"
                  :aria-label="accent.label"
                  :title="accent.label"
                  @click="setThemeAccent(accent.id)"
                >
                  <span />
                </button>
              </div>
            </div>

            <div class="theme-group">
              <div class="theme-group-head">
                <h3>界面密度</h3>
                <p>在紧凑布局和舒适布局之间切换。</p>
              </div>
              <div class="segmented theme-density-segment" role="group" aria-label="Theme density">
                <button type="button" :aria-pressed="themePreferences.density === 'comfortable'" @click="setThemeDensity('comfortable')">舒适</button>
                <button type="button" :aria-pressed="themePreferences.density === 'compact'" @click="setThemeDensity('compact')">紧凑</button>
              </div>
            </div>

            <div class="theme-group">
              <div class="theme-group-head">
                <h3>表面样式</h3>
                <p>控制卡片阴影与柔和底色的表现方式。</p>
              </div>
              <div class="theme-toggle-list">
                <label class="switch-row">
                  <span>启用卡片阴影</span>
                  <input v-model="themePreferences.shadows" type="checkbox" />
                </label>
                <label class="switch-row">
                  <span>启用柔和底色</span>
                  <input v-model="themePreferences.softSurface" type="checkbox" />
                </label>
              </div>
            </div>

            <div class="actions">
              <button class="button" type="button" @click="resetThemePreferences">
                <RotateCcw :size="16" aria-hidden="true" />
                <span>恢复默认</span>
              </button>
            </div>
          </section>

          <section class="theme-preview-card">
            <div class="theme-group-head">
              <h3>效果预览</h3>
              <p>下面会同时展示当前主题在浅色和深色场景中的效果。</p>
            </div>
            <div class="theme-preview-stack">
              <article class="theme-preview-surface light">
                <div class="theme-preview-head">
                  <strong>浅色预览</strong>
                  <span>{{ themeAccentLabel }}</span>
                </div>
                <div class="theme-preview-window">
                  <div class="theme-preview-bar">
                    <span class="dot primary" />
                    <span class="dot" />
                    <span class="dot" />
                  </div>
                  <div class="theme-preview-hero">
                    <div class="theme-preview-avatar">D</div>
                    <div class="theme-preview-lines">
                      <strong>DIANA 控制台</strong>
                      <span>浅色模式 · {{ themeDensityLabel }}</span>
                    </div>
                    <button class="theme-preview-button">保存</button>
                  </div>
                  <div class="theme-preview-cards">
                    <div />
                    <div />
                    <div />
                  </div>
                </div>
              </article>

              <article class="theme-preview-surface dark">
                <div class="theme-preview-head">
                  <strong>深色预览</strong>
                  <span>{{ themeAccentLabel }}</span>
                </div>
                <div class="theme-preview-window">
                  <div class="theme-preview-bar">
                    <span class="dot primary" />
                    <span class="dot" />
                    <span class="dot" />
                  </div>
                  <div class="theme-preview-hero">
                    <div class="theme-preview-avatar">D</div>
                    <div class="theme-preview-lines">
                      <strong>DIANA 控制台</strong>
                      <span>深色模式 · {{ themeDensityLabel }}</span>
                    </div>
                    <button class="theme-preview-button">保存</button>
                  </div>
                  <div class="theme-preview-cards">
                    <div />
                    <div />
                    <div />
                  </div>
                </div>
              </article>
            </div>
          </section>
        </div>
      </section>

    </main>

        <button v-if="updateDrawerOpen" class="update-drawer-backdrop" type="button" aria-label="关闭更新面板" @click="closeUpdateDrawer" />
        <aside v-if="updateDrawerOpen" class="update-drawer" role="dialog" aria-modal="true" aria-labelledby="update-drawer-title">
          <div class="update-drawer-head">
            <div>
              <p class="eyebrow">Update</p>
              <h2 id="update-drawer-title">系统升级</h2>
            </div>
            <button class="button" type="button" @click="closeUpdateDrawer">
              <ChevronLeft :size="16" aria-hidden="true" />
              <span>收起</span>
            </button>
          </div>

          <div class="update-drawer-body compact">
            <div class="update-state" :class="systemUpdateTone">
              <RefreshCw v-if="updatingSystem || loadingUpdateStatus || updateStatus?.updating" :size="18" class="update-spin" aria-hidden="true" />
              <CheckCircle2 v-else-if="updateStatus?.restart_required" :size="18" aria-hidden="true" />
              <TriangleAlert v-else-if="systemUpdateTone === 'warning' || systemUpdateTone === 'bad'" :size="18" aria-hidden="true" />
              <Download v-else :size="18" aria-hidden="true" />
              <div>
                <strong>{{ systemUpdateStateTitle }}</strong>
                <span>{{ systemUpdateAvailabilityText }}</span>
              </div>
            </div>

            <section class="update-info-card">
              <div class="update-info-row">
                <span>运行版本</span>
                <strong>v{{ appVersion }} · {{ systemRunningVersionLabel }}</strong>
              </div>
              <div class="update-info-row">
                <span>源码版本</span>
                <strong>{{ systemVersionLabel }}</strong>
              </div>
              <div class="update-info-row">
                <span>跟踪分支</span>
                <strong>{{ updateStatus?.upstream || updateStatus?.branch || "未识别" }}</strong>
              </div>
              <div class="update-info-row">
                <span>GitHub 链接</span>
                <a v-if="systemGitHubURL" class="update-github-link" :href="systemGitHubURL" target="_blank" rel="noreferrer">
                  <Globe :size="15" aria-hidden="true" />
                  <span>{{ systemGitHubURL }}</span>
                </a>
                <strong v-else>未配置 GitHub 链接</strong>
              </div>
              <div class="update-info-row">
                <span>更新说明</span>
                <strong>{{ systemUpdateNote }}</strong>
              </div>
            </section>

            <p v-if="systemUpdateBlockingText" class="update-warning-text">
              <TriangleAlert :size="15" aria-hidden="true" />
              <span>{{ systemUpdateBlockingText }}</span>
            </p>

            <pre v-if="updateOutput" class="update-output">{{ updateOutput }}</pre>

            <div class="update-actions">
              <button class="button" type="button" :disabled="loadingUpdateStatus || updatingSystem || updateStatus?.updating" @click="refreshUpdateStatus(true, true)">
                <RefreshCw :size="16" :class="{ 'update-spin': loadingUpdateStatus }" aria-hidden="true" />
                <span>{{ loadingUpdateStatus ? "检查中" : "检查更新" }}</span>
              </button>
              <button class="button primary" type="button" :disabled="systemUpgradeDisabled" @click="performSystemUpdate">
                <Download :size="16" aria-hidden="true" />
                <span>{{ updatingSystem ? "正在升级" : systemUpgradeActionLabel }}</span>
              </button>
            </div>
          </div>
        </aside>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import {
  Activity,
  ArrowDown,
  ArrowUp,
  Bell,
  Bot,
  BotMessageSquare,
  Braces,
  BrainCircuit,
  BookOpen,
  Cable,
  CalendarDays,
  CheckCircle2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Copy,
  Cpu,
  CloudSun,
  Download,
  Eye,
  EyeOff,
  FileClock,
  FileText,
  Globe,
  ClipboardCheck,
  Clock3,
  LayoutGrid,
  Image,
  Link2,
  ListChecks,
  KeyRound,
  LogOut,
  MemoryStick,
  MessageCircle,
  Monitor,
  MoreHorizontal,
  MoreVertical,
  PanelLeftOpen,
  Paperclip,
  Pencil,
  PlugZap,
  Plus,
  Power,
  PowerOff,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  Send,
  Server,
  ShieldCheck,
  Smile,
  SplitSquareHorizontal,
  Sparkles,
  Star,
  TerminalSquare,
  X,
  Trash2,
  TriangleAlert,
  Upload,
  UserRound,
  Users,
  Wifi,
  Wrench,
  Zap
} from "@lucide/vue";
import {
  activateQQBotProfile,
  activateConfigProfile,
  cloneQQBotProfile,
  cloneConfigProfile,
  deleteQQBotProfile,
  changeAdminPassword,
  exportConfig,
  getAdminAccessSettings,
  importConfigProfiles,
  deleteConfigProfile,
  getConfig,
  getWebSearchConfig,
  getQQBotFeatures,
  getQQBotConfig,
  getQQBotAutoInfo,
  getQQBotDashboardStats,
  getQQBotStatus,
  getUpdateStatus,
  pullFromGitHub,
  installPlugin,
  listAppLogs,
  listAdminSessions,
  listLLMModels,
  listQQBotTasks,
  listPlugins,
  logoutAdmin,
  getQQBotGroupAdminConfig,
  requestQQBotGroupAdminChallenge,
  revokeAdminSession,
  revokeOtherAdminSessions,
  saveConfig,
  saveAdminAccessSettings,
  saveWebSearchConfig,
  saveQQBotConfig,
  saveQQBotGroupAdminConfig,
  setPluginEnabled,
  getQQGroupTest,
  sendQQGroupTest,
  startQQBot,
  stopQQBot,
  testLLM,
  testWebSearchProvider,
  updateAdminEmail,
  uninstallPlugin,
  verifyQQBotGroupAdmin,
  rememberAdminLoginPath,
  type AdminAccessSettings,
  type AdminAuthSession,
  type AppLogEntry,
  type AppLogKind,
  type AppLogLevel,
  type APIFormat,
  type LLMConfig,
  type LLMModelInfo,
  type PluginState,
  type Provider,
  type QQBotConfig,
  type QQBotAutoInfo,
  type QQBotDashboardStats,
  type QQBotFeatureFlags,
  type QQBotGroupAdminConfigResponse,
  type QQBotGroupConfig,
  type QQBotTask,
  type QQGroupTestResponse,
  type ReplyRule,
  type ReplyRuleAction,
  type QQBotStatus,
  type UpdateStatus,
  type WebSearchConfig,
  type WebSearchProviderConfig
} from "./api";

type IconComponent = typeof Bot;

interface LLMFormState {
  id: string;
  name: string;
  group: string;
  description: string;
  provider: Provider;
  model: string;
  imageModel: string;
  imageBaseURL: string;
  imageOrigin: string;
  imageTimeoutMS: number;
  userAgent: string;
  apiKey: string;
  apiKeyConfigured: boolean;
  baseURL: string;
  apiFormat: APIFormat;
  temperature: number | null;
  reasoningEffort: string;
  contextWindowTokens: number;
  maxContextTokens: number;
  maxOutputTokens: number;
  timeoutMS: number;
}

interface LLMTestResultState {
  open: boolean;
  ok: boolean;
  title: string;
  message: string;
  latencyMS: number | null;
  statusCode: number | null;
  model: string;
  testedAt: string;
}

interface HeaderRow {
  id: string;
  name: string;
  value: string;
}

interface ReplyRuleFormState {
  id: string;
  name: string;
  enabled: boolean;
  prompt: string;
  action: ReplyRuleAction;
  llmProfileID: string;
}

interface BotFormState {
  id: string;
  name: string;
  platform: string;
  avatarURL: string;
  enabled: boolean;
  oneBotEndpoint: string;
  oneBotToken: string;
  oneBotTokenConfigured: boolean;
  noneBotBridgeEnabled: boolean;
  noneBotBridgeEndpoint: string;
  noneBotBridgeToken: string;
  noneBotBridgeTokenConfigured: boolean;
  botQQ: string;
  ownerID: string;
  groupTriggers: string;
  disabledGroups: string;
  disabledUsers: string;
  welcomeEnabled: boolean;
  welcomeMessage: string;
  systemPrompt: string;
  passiveReplyRouterPrompt: string;
  passiveReplyPrompt: string;
  maxInputChars: number;
  maxReplyChars: number;
  directReplyChunkSize: number;
  forwardReplyThreshold: number;
  recallReplyMode: "llm_summary" | "original_forward";
  recallReplyAutoDeleteEnabled: boolean;
  llmQQIDMaskingEnabled: boolean;
  recentContextLimit: number;
  contextSummaryThreshold: number;
  passiveReplyChance: number;
  passiveReplyThreshold: number;
  replyRules: ReplyRuleFormState[];
  maxBotConcurrency: number;
  requestTimeoutMS: number;
  agentEnabled: boolean;
  agentWorkDir: string;
  agentMaxSteps: number;
  agentSkillRoots: string;
  agentMCPConfigPath: string;
  agentCommandAllowlist: string;
  agentCommandTimeoutMS: number;
  agentBrowserCDPURL: string;
  agentBrowserTimeoutMS: number;
}

interface TestHistoryItem {
  id: string;
  role: "user" | "assistant" | "error";
  text: string;
  at: string;
  ok: boolean;
  latencyMS?: number;
}

interface TestSession {
  id: string;
  title: string;
  preview: string;
  time: string;
  ok: boolean;
  kind: "group" | "private" | "tool" | "document";
  icon: IconComponent;
  history: TestHistoryItem[];
  output: string;
  latencyMS: number | null;
}

type PersistedTestSession = Omit<TestSession, "icon"> & { icon?: IconComponent };

interface BotGroupTestState {
  groupID: string;
  message: string;
}

interface GroupAdminState {
  form: {
    groupID: string;
    userID: string;
    code: string;
    triggers: string;
  };
  token: string;
  expiresAt: string;
  config: QQBotGroupConfig;
  plugins: PluginState[];
  sendingCode: boolean;
  verifying: boolean;
  loading: boolean;
  saving: boolean;
  notice: string;
  error: string;
}

interface UpdateHistoryItem {
  id: string;
  title: string;
  output: string;
  at: string;
  ok: boolean;
}

type ThemeMode = "system" | "light" | "dark";
type ThemeDensity = "comfortable" | "compact";
type ThemeAccentID = "rose" | "violet" | "blue" | "mint" | "green" | "amber" | "slate";

interface ThemePreferenceState {
  mode: ThemeMode;
  accent: ThemeAccentID;
  density: ThemeDensity;
  shadows: boolean;
  softSurface: boolean;
}

interface ThemeAccentOption {
  id: ThemeAccentID;
  label: string;
  primary: string;
  strong: string;
  focus: string;
  surfaceLight: string;
  surfaceDark: string;
  controlLight: string;
  controlDark: string;
  shadowLight: string;
  shadowDark: string;
}

const providerOptions = [
  { value: "openai_compatible" as const, label: "OpenAI", icon: Bot },
  { value: "gemini" as const, label: "Gemini", icon: Sparkles },
  { value: "anthropic" as const, label: "Anthropic", icon: BrainCircuit }
];
const appVersion = "0.1.0";

const defaultTestSessions: TestSession[] = [
  {
    id: "group-schedule",
    title: "项目群日程安排",
    preview: "小明：明天下午3点帮忙预定会议室...",
    time: "22:31",
    ok: true,
    kind: "group",
    icon: Users,
    history: [],
    output: "等待测试结果",
    latencyMS: null
  },
  {
    id: "private-support",
    title: "客服私聊回复",
    preview: "用户：你们的产品支持发票吗？",
    time: "21:47",
    ok: true,
    kind: "private",
    icon: MessageCircle,
    history: [],
    output: "等待测试结果",
    latencyMS: null
  },
  {
    id: "multi-turn",
    title: "多轮追问测试",
    preview: "用户：这个功能怎么用？",
    time: "20:15",
    ok: false,
    kind: "private",
    icon: BrainCircuit,
    history: [],
    output: "等待测试结果",
    latencyMS: null
  },
  {
    id: "tool-call",
    title: "工具调用测试",
    preview: "查询天气并生成出行建议",
    time: "19:32",
    ok: true,
    kind: "tool",
    icon: Wrench,
    history: [],
    output: "等待测试结果",
    latencyMS: null
  },
  {
    id: "document-qa",
    title: "文档问答测试",
    preview: "根据文档回答产品功能问题",
    time: "18:05",
    ok: true,
    kind: "document",
    icon: FileClock,
    history: [],
    output: "等待测试结果",
    latencyMS: null
  }
];

function testSessionIcon(kind: TestSession["kind"]) {
  if (kind === "group") return Users;
  if (kind === "tool") return Wrench;
  if (kind === "document") return FileClock;
  return MessageCircle;
}

function normalizeTestSession(session: Partial<PersistedTestSession>, fallback?: TestSession): TestSession {
  const kind = session.kind === "group" || session.kind === "private" || session.kind === "tool" || session.kind === "document" ? session.kind : fallback?.kind || "private";
  return {
    id: typeof session.id === "string" && session.id ? session.id : fallback?.id || `session-${Date.now()}`,
    title: typeof session.title === "string" && session.title ? session.title : fallback?.title || "新的测试会话",
    preview: typeof session.preview === "string" ? session.preview : fallback?.preview || "等待发送第一条测试消息",
    time: typeof session.time === "string" && session.time ? session.time : fallback?.time || new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
    ok: typeof session.ok === "boolean" ? session.ok : fallback?.ok ?? true,
    kind,
    icon: testSessionIcon(kind),
    history: Array.isArray(session.history) ? session.history.slice(-20) : fallback?.history || [],
    output: typeof session.output === "string" ? session.output : fallback?.output || "等待测试结果",
    latencyMS: typeof session.latencyMS === "number" || session.latencyMS === null ? session.latencyMS : fallback?.latencyMS ?? null
  };
}

function serializeTestSession(session: TestSession): Omit<TestSession, "icon"> {
  const { icon: _icon, ...rest } = session;
  return {
    ...rest,
    history: rest.history.slice(-20)
  };
}

const imageModelPresets: Record<Provider, string[]> = {
  openai_compatible: ["gpt-image-2", "gpt-image-1", "gpt-image-1-mini", "gpt-image-1.5", "dall-e-3", "dall-e-2"],
  gemini: ["imagen-4.0-generate-001", "imagen-4.0-ultra-generate-001", "imagen-3.0-generate-002"],
  anthropic: []
};

const textModelPresets: Record<Provider, LLMModelInfo[]> = {
  openai_compatible: [{ id: "gpt-4o-mini", name: "Default" }],
  gemini: [
    { id: "gemini-2.5-flash", name: "Gemini 2.5 Flash" },
    { id: "gemini-2.5-pro", name: "Gemini 2.5 Pro" }
  ],
  anthropic: [
    { id: "claude-sonnet-4-5", name: "Claude Sonnet 4.5" },
    { id: "claude-opus-4-6", name: "Claude Opus 4.6" }
  ]
};

const defaultMaxOutputTokens = 1024;
const defaultContextWindowTokens = 16384;
const defaultMaxContextTokens = 16384;
const defaultTimeoutMS = 60000;

const themeAccentOptions: ThemeAccentOption[] = [
  {
    id: "rose",
    label: "蔷薇粉",
    primary: "#ff6f9d",
    strong: "#f55c8e",
    focus: "#ff8eb4",
    surfaceLight: "#fff8fb",
    surfaceDark: "#2b1f29",
    controlLight: "#fff1f6",
    controlDark: "#352634",
    shadowLight: "0 18px 40px rgba(255, 111, 157, 0.16)",
    shadowDark: "0 18px 40px rgba(0, 0, 0, 0.34)"
  },
  {
    id: "violet",
    label: "柔雾紫",
    primary: "#9b7bff",
    strong: "#8865f6",
    focus: "#af96ff",
    surfaceLight: "#faf8ff",
    surfaceDark: "#261f31",
    controlLight: "#f3efff",
    controlDark: "#32283d",
    shadowLight: "0 18px 40px rgba(155, 123, 255, 0.16)",
    shadowDark: "0 18px 40px rgba(0, 0, 0, 0.34)"
  },
  {
    id: "blue",
    label: "晴空蓝",
    primary: "#5897ff",
    strong: "#4386f6",
    focus: "#76adff",
    surfaceLight: "#f7fbff",
    surfaceDark: "#1e2430",
    controlLight: "#eef5ff",
    controlDark: "#2a3240",
    shadowLight: "0 18px 40px rgba(88, 151, 255, 0.16)",
    shadowDark: "0 18px 40px rgba(0, 0, 0, 0.34)"
  },
  {
    id: "mint",
    label: "青瓷绿",
    primary: "#53bfa7",
    strong: "#43ae96",
    focus: "#71d0bb",
    surfaceLight: "#f4fcfa",
    surfaceDark: "#1f2c29",
    controlLight: "#ebf8f4",
    controlDark: "#2a3935",
    shadowLight: "0 18px 40px rgba(83, 191, 167, 0.16)",
    shadowDark: "0 18px 40px rgba(0, 0, 0, 0.34)"
  },
  {
    id: "green",
    label: "叶影绿",
    primary: "#63b65c",
    strong: "#52a34c",
    focus: "#82ca7c",
    surfaceLight: "#f6fcf5",
    surfaceDark: "#202c20",
    controlLight: "#eef8ec",
    controlDark: "#2d392c",
    shadowLight: "0 18px 40px rgba(99, 182, 92, 0.16)",
    shadowDark: "0 18px 40px rgba(0, 0, 0, 0.34)"
  },
  {
    id: "amber",
    label: "暖杏橙",
    primary: "#ff9a57",
    strong: "#f4863e",
    focus: "#ffb27a",
    surfaceLight: "#fff9f3",
    surfaceDark: "#30241d",
    controlLight: "#fff2e4",
    controlDark: "#3b2d25",
    shadowLight: "0 18px 40px rgba(255, 154, 87, 0.16)",
    shadowDark: "0 18px 40px rgba(0, 0, 0, 0.34)"
  },
  {
    id: "slate",
    label: "雾灰",
    primary: "#8a94a8",
    strong: "#78839a",
    focus: "#a2abc0",
    surfaceLight: "#f8f9fc",
    surfaceDark: "#232630",
    controlLight: "#f1f3f8",
    controlDark: "#2f3440",
    shadowLight: "0 18px 40px rgba(138, 148, 168, 0.16)",
    shadowDark: "0 18px 40px rgba(0, 0, 0, 0.34)"
  }
];

type TabID = "dashboard" | "llm" | "test" | "qqbot" | "group-admin" | "plugins" | "web-search" | "logs" | "security" | "theme";
type PluginFilter = "all" | "installed" | "enabled" | "official" | "community";
type PluginCategory = "all" | "analysis" | "dialog" | "tool" | "management";
type LogViewFilter = "all" | AppLogKind;
type LogLevelFilter = "all" | AppLogLevel | "warn";
type BotPlatformFilter = "all" | "telegram" | "qq" | "discord" | "slack" | "wechat" | "custom";
type BotDetailTab = "config" | "prompts" | "rules" | "events" | "messages" | "test";
type LLMEditorMode = "list" | "edit";

interface PluginDetailRow {
  label: string;
  value: string;
}

interface PluginCommandHint {
  command: string;
  description: string;
}

interface PluginSettingRow {
  label: string;
  type: "select" | "switch" | "number" | "chips";
  value?: string;
  enabled?: boolean;
  options?: string[];
  chips?: string[];
}

const tabs = [
  { id: "dashboard" as const, label: "仪表盘", icon: LayoutGrid },
  { id: "llm" as const, label: "LLM 配置", icon: BrainCircuit },
  { id: "test" as const, label: "连通测试", icon: Server },
  { id: "qqbot" as const, label: "QQ 机器人", icon: Cable },
  { id: "group-admin" as const, label: "群管理", icon: Users },
  { id: "plugins" as const, label: "插件管理", icon: PlugZap },
  { id: "web-search" as const, label: "联网搜索", icon: Globe },
  { id: "logs" as const, label: "日志中心", icon: FileClock },
  { id: "security" as const, label: "访问设置", icon: ShieldCheck },
  { id: "theme" as const, label: "主题配置", icon: Sparkles }
];
const dashboardTab = tabs[0];
const sidebarGroups: Array<{ label: string; tabs: typeof tabs }> = [
  { label: "模型", tabs: tabs.filter((tab) => tab.id === "llm" || tab.id === "test") },
  { label: "机器人", tabs: tabs.filter((tab) => tab.id === "qqbot" || tab.id === "group-admin") },
  { label: "能力", tabs: tabs.filter((tab) => tab.id === "plugins" || tab.id === "web-search") },
  { label: "系统", tabs: tabs.filter((tab) => tab.id === "logs" || tab.id === "security" || tab.id === "theme") }
];
const tabRoutes: Record<TabID, string> = {
  dashboard: "/console",
  llm: "/llm",
  test: "/test",
  qqbot: "/qqbot",
  "group-admin": "/groups",
  plugins: "/plugins",
  "web-search": "/web-search",
  logs: "/logs",
  security: "/security",
  theme: "/theme"
};
const routeTabs = new Map<string, TabID>(Object.entries(tabRoutes).map(([tab, path]) => [path, tab as TabID]));

const logLevelOptions: Array<{ value: LogLevelFilter; label: string }> = [
  { value: "all", label: "全部级别" },
  { value: "info", label: "信息" },
  { value: "warn", label: "警告" },
  { value: "error", label: "错误" }
];
const logPageSizeOptions = [20, 50, 100];

const pluginCategoryOptions: Array<{ value: PluginCategory; label: string }> = [
  { value: "all", label: "全部分类" },
  { value: "analysis", label: "解析" },
  { value: "dialog", label: "对话" },
  { value: "tool", label: "工具" },
  { value: "management", label: "管理" }
];

const botPlatformOptions: Array<{ value: BotPlatformFilter; label: string }> = [
  { value: "all", label: "全部" },
  { value: "telegram", label: "Telegram" },
  { value: "qq", label: "QQ" },
  { value: "discord", label: "Discord" },
  { value: "slack", label: "Slack" },
  { value: "wechat", label: "企业微信" },
  { value: "custom", label: "自定义" }
];

const botDetailTabs: Array<{ value: BotDetailTab; label: string }> = [
  { value: "config", label: "配置" },
  { value: "prompts", label: "提示词" },
  { value: "rules", label: "回复规则" },
  { value: "events", label: "事件日志" },
  { value: "messages", label: "消息记录" },
  { value: "test", label: "连接测试" }
];

const llmForm = reactive<LLMFormState>({
  id: "",
  name: "默认配置",
  group: "default",
  description: "",
  provider: "openai_compatible",
  model: "gpt-4o-mini",
  imageModel: "gpt-image-1",
  imageBaseURL: "",
  imageOrigin: "",
  imageTimeoutMS: 300000,
  userAgent: "diana-qq-bot",
  apiKey: "",
  apiKeyConfigured: false,
  baseURL: "",
  apiFormat: "responses",
  temperature: null,
  reasoningEffort: "",
  contextWindowTokens: defaultContextWindowTokens,
  maxContextTokens: defaultMaxContextTokens,
  maxOutputTokens: 1024,
  timeoutMS: defaultTimeoutMS
});

const botForm = reactive<BotFormState>({
  id: "",
  name: "默认机器人",
  platform: "NapCat / OneBot V11",
  avatarURL: "",
  enabled: false,
  oneBotEndpoint: "ws://127.0.0.1:18080/onebot/v11/ws",
  oneBotToken: "",
  oneBotTokenConfigured: false,
  noneBotBridgeEnabled: false,
  noneBotBridgeEndpoint: "ws://127.0.0.1:8080/onebot/v11/ws",
  noneBotBridgeToken: "",
  noneBotBridgeTokenConfigured: false,
  botQQ: "",
  ownerID: "",
  groupTriggers: "嘉然,然然,Diana,diana",
  disabledGroups: "",
  disabledUsers: "",
  welcomeEnabled: false,
  welcomeMessage: "欢迎加入本群，直接 @我 或发送触发词就可以开始聊天。",
  systemPrompt: "",
  passiveReplyRouterPrompt: "",
  passiveReplyPrompt: "",
  maxInputChars: 2000,
  maxReplyChars: 3500,
  directReplyChunkSize: 500,
  forwardReplyThreshold: 900,
  recallReplyMode: "llm_summary",
  recallReplyAutoDeleteEnabled: true,
  llmQQIDMaskingEnabled: true,
  recentContextLimit: 20,
  contextSummaryThreshold: 100,
  passiveReplyChance: 1,
  passiveReplyThreshold: 0.8,
  replyRules: [],
  maxBotConcurrency: 8,
  requestTimeoutMS: 180000,
  agentEnabled: true,
  agentWorkDir: ".",
  agentMaxSteps: 8,
  agentSkillRoots: "",
  agentMCPConfigPath: "",
  agentCommandAllowlist: "",
  agentCommandTimeoutMS: 10000,
  agentBrowserCDPURL: "http://127.0.0.1:9222",
  agentBrowserTimeoutMS: 15000
});

const botGroupTest = reactive<BotGroupTestState>({
  groupID: "",
  message: "QQ群收发测试：如果你看到这条消息，说明机器人可以发到本群。"
});
const groupAdmin = reactive<GroupAdminState>({
  form: {
    groupID: "",
    userID: "",
    code: "",
    triggers: "嘉然,然然,Diana,diana"
  },
  token: "",
  expiresAt: "",
  config: {
    group_id: "",
    enabled: true,
    group_triggers: ["嘉然", "然然", "Diana", "diana"],
    welcome_enabled: false,
    welcome_message: "欢迎加入本群，直接 @我 或发送触发词就可以开始聊天。",
    recent_context_limit: 20,
    max_reply_chars: 3500,
    passive_reply_chance: 1,
    passive_reply_threshold: 0.8,
    minimum_reply_member_level: 0,
    plugin_overrides: {}
  },
  plugins: [],
  sendingCode: false,
  verifying: false,
  loading: false,
  saving: false,
  notice: "",
  error: ""
});

const status = reactive({ text: "读取中", kind: "" });
const adminAccessSettings = reactive<AdminAccessSettings>({
  configured: false,
  username: "",
  random_suffix_enabled: false,
  login_path: "/login",
  managed_by_environment: false
});
const savingAdminAccess = ref(false);
const adminSessions = ref<AdminAuthSession[]>([]);
const loadingAdminSessions = ref(false);
const revokingAdminSession = ref("");
const savingAdminAccount = ref(false);
const changingAdminPassword = ref(false);
const adminAccountForm = reactive({ email: "", currentPassword: "" });
const adminPasswordForm = reactive({ currentPassword: "", newPassword: "", passwordConfirm: "" });
const savingLLM = ref(false);
const loadingModels = ref(false);
const testingProfileID = ref("");
const savingBot = ref(false);
const startingBot = ref(false);
const stoppingBot = ref(false);
const sendingGroupTest = ref(false);
const refreshingGroupTest = ref(false);
const testing = ref(false);
const loadingWebSearch = ref(false);
const savingWebSearch = ref(false);
const testingWebSearchIndex = ref(-1);
const webSearchTestQuery = ref("OpenAI API 最新官方文档");
const webSearchConfig = reactive<WebSearchConfig>({ providers: [] });
const webSearchSavedState = ref("");
const webSearchTestResult = reactive({ index: -1, ok: false, durationMS: 0, text: "" });
const loadingUpdateStatus = ref(false);
const updatingSystem = ref(false);
const loadingLogs = ref(false);
const pluginBusy = ref("");
const pluginQuery = ref("");
const pluginFilter = ref<PluginFilter>("all");
const pluginCategory = ref<PluginCategory>("all");
const selectedPluginID = ref("");
const pluginDetailOpen = ref(true);
const logKind = ref<AppLogKind>("operation");
const logView = ref<LogViewFilter>("all");
const logQuery = ref("");
const logLevelFilter = ref<LogLevelFilter>("all");
const logStartDate = ref("");
const logEndDate = ref("");
const logPage = ref(1);
const logPageSize = ref(20);
const message = ref("你好，用一句话回复当前模型已连通。");
const output = ref("等待测试结果");
const updateOutput = ref("等待仓库状态");
const updateHistory = ref<UpdateHistoryItem[]>([]);
const testSessions = ref<TestSession[]>(defaultTestSessions);
const selectedTestSessionID = ref(defaultTestSessions[0].id);
const testConversationMode = ref<"group" | "private">("group");
const recordTestContext = ref(true);
const testMessageStreamRef = ref<HTMLElement | null>(null);
const llmImportText = ref("");
const llmImportFileRef = ref<HTMLInputElement | null>(null);
const activeTab = ref<TabID>("dashboard");
const sidebarOpen = ref(false);
const botStatus = ref<QQBotStatus | null>(null);
const botAutoInfo = ref<QQBotAutoInfo | null>(null);
const botGroupTestResult = ref<QQGroupTestResponse | null>(null);
const botGroupTestError = ref("");
const plugins = ref<PluginState[]>([]);
const updateStatus = ref<UpdateStatus | null>(null);
const appLogs = ref<AppLogEntry[]>([]);
const qqbotTasks = ref<QQBotTask[]>([]);
const qqbotDashboardStats = ref<QQBotDashboardStats | null>(null);
const fetchedModelOptions = ref<LLMModelInfo[]>([]);
const modelMenuOpen = ref(false);
const modelSelectRef = ref<HTMLElement | null>(null);
const llmAdvancedOpen = ref(false);
const updateDrawerOpen = ref(false);
const llmProfiles = ref<LLMConfig[]>([]);
const llmActiveProfileID = ref("");
const llmEditorMode = ref<LLMEditorMode>("list");
const llmProfileQuery = ref("");
const llmMoreMenuProfileID = ref("");
const showLLMAPIKey = ref(false);
const llmHeaderRows = ref<HeaderRow[]>([]);
const botProfiles = ref<QQBotConfig[]>([]);
const botActiveProfileID = ref("");
const botProfileQuery = ref("");
const botPlatformFilter = ref<BotPlatformFilter>("all");
const botDetailTab = ref<BotDetailTab>("config");
const botDetailOpen = ref(false);
const botPage = ref(1);
const botPageSize = ref(10);
const systemPrefersDark = ref(false);
const themePreferences = reactive<ThemePreferenceState>({
  mode: "system",
  accent: "rose",
  density: "comfortable",
  shadows: true,
  softSurface: true
});
const botSectionsOpen = reactive({
  connection: true,
  groups: true,
  replies: false,
  automation: false
});
const botFeatures = reactive<QQBotFeatureFlags>({
  group_test: false
});
const llmTestResult = reactive<LLMTestResultState>({
  open: false,
  ok: false,
  title: "",
  message: "",
  latencyMS: null,
  statusCode: null,
  model: "",
  testedAt: ""
});
const activeLLMProfile = computed(() => llmProfiles.value.find((profile) => profile.id === llmActiveProfileID.value) || llmProfiles.value[0] || null);
const adminLoginURL = computed(() => `${window.location.origin}${adminAccessSettings.login_path || "/"}`);
const adminPasswordReady = computed(() =>
  adminPasswordForm.currentPassword.length > 0 &&
  adminPasswordForm.newPassword.length >= 12 &&
  adminPasswordForm.newPassword === adminPasswordForm.passwordConfirm
);
const webSearchDirty = computed(() => webSearchConfigState(webSearchConfig) !== webSearchSavedState.value);

function normalizedWebSearchProvider(provider: WebSearchProviderConfig): WebSearchProviderConfig {
  return {
    name: provider.name || "",
    type: provider.type || "exa_mcp",
    url: provider.url || "",
    tool: provider.tool || "",
    api_key_env: provider.api_key_env || "",
    api_key_configured: Boolean(provider.api_key_configured),
    timeout_ms: Number(provider.timeout_ms) || 12000,
    max_results: Number(provider.max_results) || 5,
    disabled: Boolean(provider.disabled)
  };
}

function webSearchConfigState(config: WebSearchConfig): string {
  return JSON.stringify(
    (config.providers || []).map((provider) => {
      const normalized = normalizedWebSearchProvider(provider);
      delete normalized.api_key_configured;
      return normalized;
    })
  );
}

function applyWebSearchConfig(config: WebSearchConfig) {
  webSearchConfig.providers.splice(0, webSearchConfig.providers.length, ...(config.providers || []).map(normalizedWebSearchProvider));
  webSearchConfig.config_path = config.config_path || "";
  webSearchConfig.overridden_by_env = Boolean(config.overridden_by_env);
  webSearchSavedState.value = webSearchConfigState(webSearchConfig);
  webSearchTestResult.index = -1;
  webSearchTestResult.text = "";
}

function webSearchProviderTypeLabel(type: WebSearchProviderConfig["type"]): string {
  return type === "tavily" ? "Tavily" : "Exa MCP";
}

function webSearchProviderReady(provider: WebSearchProviderConfig): boolean {
  return !provider.api_key_env || Boolean(provider.api_key_configured);
}

function webSearchProviderKeyLabel(provider: WebSearchProviderConfig): string {
  if (!provider.api_key_env) return "免密钥";
  return provider.api_key_configured ? "Key 已注入" : "Key 未注入";
}

function addWebSearchProvider() {
  const index = webSearchConfig.providers.length + 1;
  webSearchConfig.providers.push({
    name: `exa-fallback-${index}`,
    type: "exa_mcp",
    url: "https://mcp.exa.ai/mcp?tools=web_search_exa",
    tool: "web_search_exa",
    api_key_env: "",
    timeout_ms: 12000,
    max_results: 5,
    disabled: false,
    api_key_configured: true
  });
}

function removeWebSearchProvider(index: number) {
  if (webSearchConfig.providers.length <= 1) return;
  webSearchConfig.providers.splice(index, 1);
  webSearchTestResult.index = -1;
}

function moveWebSearchProvider(index: number, offset: -1 | 1) {
  const target = index + offset;
  if (target < 0 || target >= webSearchConfig.providers.length) return;
  const [provider] = webSearchConfig.providers.splice(index, 1);
  webSearchConfig.providers.splice(target, 0, provider);
  webSearchTestResult.index = -1;
}

function onWebSearchProviderTypeChange(provider: WebSearchProviderConfig) {
  if (provider.type === "tavily") {
    if (!provider.url || provider.url.includes("mcp.exa.ai")) provider.url = "https://api.tavily.com/search";
    provider.tool = "";
    if (!provider.api_key_env) provider.api_key_env = "TAVILY_API_KEY";
    provider.api_key_configured = false;
    return;
  }
  if (!provider.url || provider.url.includes("api.tavily.com")) provider.url = "https://mcp.exa.ai/mcp?tools=web_search_exa";
  if (!provider.tool) provider.tool = "web_search_exa";
  if (provider.api_key_env === "TAVILY_API_KEY") provider.api_key_env = "";
  provider.api_key_configured = !provider.api_key_env;
}

async function refreshWebSearchConfig() {
  if (webSearchDirty.value && !window.confirm("联网搜索配置还有未保存改动，确定重新载入吗？")) return;
  loadingWebSearch.value = true;
  setStatus("读取搜索配置");
  try {
    applyWebSearchConfig(await getWebSearchConfig());
    setStatus("搜索配置已刷新", "ok");
  } catch (error) {
    setStatus("搜索配置读取失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    loadingWebSearch.value = false;
  }
}

async function onSaveWebSearchConfig() {
  savingWebSearch.value = true;
  setStatus("保存搜索配置");
  try {
    applyWebSearchConfig(await saveWebSearchConfig(webSearchConfig));
    setStatus("搜索配置已保存", "ok");
  } catch (error) {
    setStatus("搜索配置保存失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    savingWebSearch.value = false;
  }
}

async function onTestWebSearchProvider(provider: WebSearchProviderConfig, index: number) {
  testingWebSearchIndex.value = index;
  webSearchTestResult.index = index;
  webSearchTestResult.ok = false;
  webSearchTestResult.durationMS = 0;
  webSearchTestResult.text = "正在搜索";
  setStatus(`测试 ${provider.name || "搜索配置"}`);
  try {
    const result = await testWebSearchProvider(provider, webSearchTestQuery.value);
    webSearchTestResult.ok = true;
    webSearchTestResult.durationMS = result.duration_ms;
    webSearchTestResult.text = result.content;
    setStatus("搜索测试成功", "ok");
  } catch (error) {
    webSearchTestResult.text = error instanceof Error ? error.message : String(error);
    setStatus("搜索测试失败", "bad");
  } finally {
    testingWebSearchIndex.value = -1;
  }
}

function providerDisplayLabel(provider?: Provider): string {
  if (provider === "openai_compatible") return "OpenAI";
  if (provider === "gemini") return "Google Gemini";
  if (provider === "anthropic") return "Anthropic";
  return "-";
}

function providerModelLabel(profile?: LLMConfig | null): string {
  if (!profile) {
    return "-";
  }
  return `${providerDisplayLabel(profile.provider)} / ${profile.model || "-"}`;
}

function defaultEndpointForProvider(provider?: Provider): string {
  if (provider === "openai_compatible") return "https://api.openai.com/v1";
  if (provider === "gemini") return "https://generativelanguage.googleapis.com";
  if (provider === "anthropic") return "https://api.anthropic.com";
  return "-";
}

function endpointLabel(rawValue?: string, provider?: Provider): string {
  const raw = (rawValue || "").trim();
  return raw || defaultEndpointForProvider(provider);
}

function profileEndpointLabel(profile?: LLMConfig | null): string {
  return endpointLabel(profile?.base_url, profile?.provider);
}

function profileUpdatedAtLabel(profile?: LLMConfig | null): string {
  if (!profile?.updated_at) {
    return "-";
  }
  const date = new Date(profile.updated_at);
  if (Number.isNaN(date.getTime())) {
    return profile.updated_at;
  }
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function profileKeyConfigured(profile?: LLMConfig | null): boolean {
  return Boolean(profile?.api_key_configured || profile?.api_key);
}

function profileGroupLabel(profile?: LLMConfig | null): string {
  return (profile?.group || "default").trim() || "default";
}

function isActiveLLMProfile(profile: LLMConfig): boolean {
  return Boolean(profile.id && profile.id === llmActiveProfileID.value);
}

function toggleLLMMoreMenu(profile: LLMConfig) {
  llmMoreMenuProfileID.value = llmMoreMenuProfileID.value === profile.id ? "" : profile.id || "";
}

function closeLLMMoreMenu() {
  llmMoreMenuProfileID.value = "";
}

function closeLLMTestResult() {
  llmTestResult.open = false;
}

function showLLMTestResult(payload: Omit<LLMTestResultState, "open">) {
  Object.assign(llmTestResult, {
    ...payload,
    open: true
  });
}

function newHeaderRow(name = "", value = ""): HeaderRow {
  return {
    id: `h-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    name,
    value
  };
}

function headerRowsFromConfig(headers?: Record<string, string>): HeaderRow[] {
  return Object.entries(headers || {})
    .filter(([name, value]) => name.trim() && String(value).trim())
    .map(([name, value]) => newHeaderRow(name, String(value)));
}

function headersFromRows(): Record<string, string> | undefined {
  const headers: Record<string, string> = {};
  for (const row of llmHeaderRows.value) {
    const name = row.name.trim();
    const value = row.value.trim();
    if (!name || !value) {
      continue;
    }
    headers[name] = value;
  }
  return Object.keys(headers).length > 0 ? headers : undefined;
}

function addLLMHeaderRow() {
  llmHeaderRows.value = [...llmHeaderRows.value, newHeaderRow()];
}

function removeLLMHeaderRow(id: string) {
  llmHeaderRows.value = llmHeaderRows.value.filter((row) => row.id !== id);
}
const activeBotProfile = computed(() => botProfiles.value.find((profile) => profile.id === botActiveProfileID.value) || botProfiles.value[0] || null);
const editingBotProfile = computed<QQBotConfig>(() => ({
  ...botPayload(),
  id: botForm.id || activeBotProfile.value?.id,
  onebot_access_token_configured: botForm.oneBotTokenConfigured,
  nonebot_bridge_token_configured: botForm.noneBotBridgeTokenConfigured
}));
function botProfileEndpointLabel(profile?: QQBotConfig | null): string {
  const raw = (profile?.onebot_reverse_ws_endpoint || "").trim();
  if (!raw) return "-";
  try {
    return new URL(raw).host;
  } catch {
    return raw;
  }
}
function botProfilePlatformLabel(profile?: QQBotConfig | null): string {
  return profile?.platform?.trim() || "NapCat / OneBot V11";
}
function botProfileEndpointDisplay(profile?: QQBotConfig | null): string {
  const raw = (profile?.onebot_reverse_ws_endpoint || "").trim();
  if (!raw) return "-";
  if (raw.length <= 38) return raw;
  return `${raw.slice(0, 34)}...`;
}
function botProfileInitial(profile?: QQBotConfig | null): string {
  const seed = profile?.name?.trim() || profile?.bot_qq?.trim() || profile?.platform?.trim() || "Q";
  return seed.slice(0, 1).toUpperCase();
}
function isActiveBotProfile(profile: QQBotConfig): boolean {
  return Boolean(profile.id && profile.id === botActiveProfileID.value);
}
function isSelectedBotProfile(profile: QQBotConfig): boolean {
  return Boolean(profile.id && botForm.id && profile.id === botForm.id);
}
function botProfileKey(profile: QQBotConfig): string {
  return profile.id || profile.name || profile.bot_qq || botProfilePlatformLabel(profile);
}
function botProfileResolvedID(profile?: QQBotConfig | null): string {
  const configured = profile?.bot_qq?.trim();
  if (configured) return configured;
  if (profile?.id === activeBotProfile.value?.id && botStatus.value?.channel.self_id) {
    return botStatus.value.channel.self_id;
  }
  return profile?.id?.trim() || "";
}
function normalizeBotPlatform(profile?: QQBotConfig | null): BotPlatformFilter {
  const value = `${profile?.platform || ""} ${profile?.onebot_reverse_ws_endpoint || ""}`.toLowerCase();
  if (/(telegram|tg|api\.telegram)/.test(value)) return "telegram";
  if (/(qq|napcat|onebot|oicq)/.test(value)) return "qq";
  if (/discord/.test(value)) return "discord";
  if (/slack/.test(value)) return "slack";
  if (/(wechat|weixin|企业微信)/.test(value)) return "wechat";
  return "custom";
}
function botPlatformIcon(profile?: QQBotConfig | null): IconComponent {
  switch (normalizeBotPlatform(profile)) {
    case "telegram":
      return Send;
    case "discord":
      return MessageCircle;
    case "slack":
      return LayoutGrid;
    case "wechat":
      return Users;
    case "custom":
      return Braces;
    case "qq":
    default:
      return BotMessageSquare;
  }
}
function botPlatformKey(profile?: QQBotConfig | null): string {
  return normalizeBotPlatform(profile);
}
function botProfileSubtitle(profile?: QQBotConfig | null): string {
  if (!profile) return "系统集成";
  if ((profile.group_triggers || []).length > 0) return "群管理";
  if (normalizeBotPlatform(profile) === "telegram") return "通知 / 推送";
  if (normalizeBotPlatform(profile) === "qq") return "群管理";
  return "系统集成";
}
function botProfileUsername(profile?: QQBotConfig | null): string {
  const id = botProfileResolvedID(profile);
  if (!id) return "@未绑定";
  return id.startsWith("@") ? id : `@${id}`;
}
const botEndpointLabel = computed(() => {
  return botProfileEndpointLabel(activeBotProfile.value);
});
const botGroupPolicyLabel = computed(() => {
  const disabled = (activeBotProfile.value?.disabled_groups || [])
    .map((item) => item.trim())
    .filter(Boolean).length;
  return disabled > 0 ? `${disabled} 个群禁用` : "全部可响应";
});
const filteredBotProfiles = computed(() => {
  const keyword = botProfileQuery.value.trim().toLowerCase();
  return botProfiles.value.filter((profile) => {
    if (botPlatformFilter.value !== "all" && normalizeBotPlatform(profile) !== botPlatformFilter.value) {
      return false;
    }
    if (!keyword) {
      return true;
    }
    const haystack = [
      profile.name,
      profile.id,
      profile.platform,
      profile.bot_qq,
      profile.owner_id,
      profile.onebot_reverse_ws_endpoint
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    return haystack.includes(keyword);
  });
});
const botPageCount = computed(() => Math.max(1, Math.ceil(filteredBotProfiles.value.length / botPageSize.value)));
const pagedBotProfiles = computed(() => {
  const page = Math.min(botPage.value, botPageCount.value);
  const start = (page - 1) * botPageSize.value;
  return filteredBotProfiles.value.slice(start, start + botPageSize.value);
});
const botPageNumbers = computed(() => {
  const total = botPageCount.value;
  const visible = Math.min(3, total);
  const start = Math.max(1, Math.min(botPage.value - 1, total - visible + 1));
  return Array.from({ length: visible }, (_, index) => start + index);
});
const runningBotCount = computed(() => botProfiles.value.filter((profile) => botRuntimeTone(profile) === "ok").length);
const connectedBotCount = computed(() => botProfiles.value.filter((profile) => botCallbackTone(profile) === "ok").length);
const disconnectedBotCount = computed(() => Math.max(0, botProfiles.value.length - connectedBotCount.value));
const botSyncLabel = computed(() => (botDirty.value ? "未保存" : "刚刚更新"));
const botSummaryName = computed(() => activeBotProfile.value?.name || "默认机器人");
const botSummaryPlatform = computed(() => botProfilePlatformLabel(activeBotProfile.value));
const botSummaryBotID = computed(() => activeBotProfile.value?.bot_qq || "-");
const botSummaryOwner = computed(() => activeBotProfile.value?.owner_id || "-");
const groupTestEvents = computed(() => botGroupTestResult.value?.recent_events || []);
const botRecentEvents = computed(() => botStatus.value?.recent_events || []);
const botRecentSyncLabel = computed(() => formatLogTime(botStatus.value?.channel.updated_at || botStatus.value?.updated_at || new Date().toISOString()));
const groupAdminVerified = computed(() => Boolean(groupAdmin.token && groupAdmin.config.group_id));
const groupAdminSessionLabel = computed(() => {
  if (!groupAdmin.expiresAt) {
    return "当前会话有效";
  }
  return `有效至 ${formatLogTime(groupAdmin.expiresAt)}`;
});
const installedPluginCount = computed(() => plugins.value.filter((plugin) => plugin.installed).length);
const enabledPluginCount = computed(() => plugins.value.filter((plugin) => plugin.installed && plugin.enabled).length);
const updatablePluginCount = computed(() => plugins.value.filter((plugin) => pluginNeedsUpdate(plugin)).length);
const filteredPlugins = computed(() => {
  const keyword = pluginQuery.value.trim().toLowerCase();
  return plugins.value
    .filter((plugin) => {
      if (pluginFilter.value === "installed" && !plugin.installed) {
        return false;
      }
      if (pluginFilter.value === "enabled" && !(plugin.installed && plugin.enabled)) {
        return false;
      }
      if (pluginFilter.value === "official" && !plugin.manifest.official) {
        return false;
      }
      if (pluginFilter.value === "community" && plugin.manifest.official) {
        return false;
      }
      if (pluginCategory.value !== "all" && pluginCategoryOf(plugin) !== pluginCategory.value) {
        return false;
      }
      if (!keyword) {
        return true;
      }
      const haystack = [
        plugin.manifest.name,
        plugin.manifest.id,
        plugin.manifest.description,
        pluginCategoryLabel(plugin),
        pluginTags(plugin).join(" ")
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(keyword);
    })
    .sort((a, b) => {
      const score = (plugin: PluginState) => {
        if (plugin.installed && plugin.enabled) return 0;
        if (plugin.installed) return 1;
        if (plugin.manifest.official) return 2;
        return 3;
      };
      const diff = score(a) - score(b);
      if (diff !== 0) return diff;
      return a.manifest.name.localeCompare(b.manifest.name, "zh-CN");
    });
});
const activePlugin = computed(() => {
  const selected = plugins.value.find((plugin) => plugin.manifest.id === selectedPluginID.value);
  if (selected) {
    return selected;
  }
  return filteredPlugins.value[0] || plugins.value[0] || null;
});

function pluginText(plugin: PluginState): string {
  return [plugin.manifest.id, plugin.manifest.name, plugin.manifest.description].join(" ").toLowerCase();
}

function pluginCategoryOf(plugin: PluginState): PluginCategory {
  const text = pluginText(plugin);
  if (text.includes("file") || text.includes("parser") || text.includes("解析")) {
    return "analysis";
  }
  if (text.includes("llm") || text.includes("skill") || text.includes("配置")) {
    return "dialog";
  }
  if (text.includes("group") || text.includes("manager") || text.includes("群")) {
    return "management";
  }
  return "tool";
}

function pluginCategoryLabel(plugin: PluginState): string {
  const labels: Record<PluginCategory, string> = {
    all: "全部",
    analysis: "解析",
    dialog: "对话",
    tool: "工具",
    management: "管理"
  };
  return labels[pluginCategoryOf(plugin)];
}

function pluginTags(plugin: PluginState): string[] {
  return [
    plugin.manifest.official ? "官方" : "第三方",
    plugin.manifest.built_in ? "内置" : "外部",
    pluginCategoryLabel(plugin)
  ];
}

function pluginTone(plugin: PluginState): string {
  const text = pluginText(plugin);
  if (text.includes("file") || text.includes("parser")) return "violet";
  if (text.includes("llm") || text.includes("skill")) return "purple";
  if (text.includes("resolver") || text.includes("link")) return "blue";
  if (text.includes("group") || text.includes("manager")) return "green";
  if (text.includes("weather")) return "amber";
  if (text.includes("schedule")) return "rose";
  if (text.includes("image")) return "indigo";
  return "slate";
}

function pluginIcon(plugin: PluginState): IconComponent {
  const text = pluginText(plugin);
  if (text.includes("file") || text.includes("parser")) return FileText;
  if (text.includes("llm") || text.includes("skill")) return BrainCircuit;
  if (text.includes("resolver") || text.includes("link")) return Link2;
  if (text.includes("group") || text.includes("manager")) return Users;
  if (text.includes("weather")) return CloudSun;
  if (text.includes("schedule")) return CalendarDays;
  if (text.includes("knowledge")) return BookOpen;
  if (text.includes("image")) return Image;
  return PlugZap;
}

function pluginAuthor(plugin: PluginState): string {
  return plugin.manifest.official ? "Diana Team" : "Community";
}

function pluginSourceLabel(plugin: PluginState): string {
  if (plugin.manifest.official && plugin.manifest.built_in) return "官方 / 内置";
  if (plugin.manifest.official) return "官方";
  return "第三方";
}

function pluginStatusLabel(plugin: PluginState): string {
  if (!plugin.installed) return "未启用";
  return plugin.enabled ? "已启用" : "未启用";
}

function pluginStatusKind(plugin: PluginState): string {
  if (!plugin.installed) return "off";
  return plugin.enabled ? "ok" : "idle";
}

function pluginNeedsUpdate(plugin: PluginState): boolean {
  return plugin.installed && !plugin.manifest.built_in && /^0\./.test(plugin.manifest.version || "");
}

function pluginPermissionLabels(plugin: PluginState): string[] {
  const map: Record<string, string> = {
    "network:http": "访问网络 / 链接",
    "message:read": "读取消息内容",
    "file:parse": "解析文件内容",
    "llm:config:write": "修改 LLM 配置"
  };
  const permissions = plugin.manifest.permissions || [];
  if (permissions.length > 0) {
    return permissions.map((permission) => map[permission] || permission);
  }
  return ["读取消息内容", "按需生成回复"];
}

function pluginCommandHints(plugin: PluginState): PluginCommandHint[] {
  const text = pluginText(plugin);
  if (text.includes("file") || text.includes("parser")) {
    return [
      { command: "/parse [文件]", description: "解析文件内容" },
      { command: "/extract [文本]", description: "提取文本要点" },
      { command: "/summary [内容]", description: "生成内容摘要" }
    ];
  }
  if (text.includes("llm") || text.includes("skill")) {
    return [
      { command: "切换到 [模型]", description: "修改当前模型名称" },
      { command: "使用 [Provider]", description: "切换模型提供商" },
      { command: "当前模型", description: "查看当前 LLM 配置" }
    ];
  }
  if (text.includes("resolver") || text.includes("link")) {
    return [
      { command: "/resolve [链接]", description: "解析链接上下文" },
      { command: "发送 URL", description: "自动补充链接摘要" }
    ];
  }
  return [
    { command: "/help", description: "查看插件帮助" },
    { command: "自然语言触发", description: "按插件能力自动处理" }
  ];
}

function pluginConfigRows(plugin: PluginState): PluginDetailRow[] {
  const text = pluginText(plugin);
  if (text.includes("file") || text.includes("parser")) {
    return [
      { label: "最大文件大小", value: "2 MB" },
      { label: "支持文件类型", value: "txt / md / json / csv / yaml / html" },
      { label: "摘要最大长度", value: "12000 字符" }
    ];
  }
  if (text.includes("resolver") || text.includes("link")) {
    return [
      { label: "网络请求", value: "HTTP / HTTPS" },
      { label: "超时时间", value: "8 秒" },
      { label: "支持平台", value: "B 站 / YouTube / X / 小红书 / 抖音" }
    ];
  }
  if (text.includes("llm") || text.includes("skill")) {
    return [
      { label: "操作范围", value: "当前激活 LLM 配置" },
      { label: "权限校验", value: "仅主人 QQ 可修改" },
      { label: "模型校验", value: "切换前校验模型列表" }
    ];
  }
  return [
    { label: "运行方式", value: plugin.manifest.built_in ? "内置插件" : "外部插件" },
    { label: "分类", value: pluginCategoryLabel(plugin) }
  ];
}

function pluginSettingRows(plugin: PluginState): PluginSettingRow[] {
  const text = pluginText(plugin);
  if (text.includes("file") || text.includes("parser")) {
    return [
      { label: "最大文件大小", type: "select", value: "50 MB", options: ["10 MB", "20 MB", "50 MB", "100 MB"] },
      { label: "支持的文件类型", type: "chips", chips: ["txt", "md", "pdf", "docx", "csv", "+5"] },
      { label: "自动生成摘要", type: "switch", enabled: true },
      { label: "使用 OCR 识别", type: "switch", enabled: true },
      { label: "摘要最大长度", type: "number", value: "1200" },
      { label: "超时时间", type: "select", value: "30 秒", options: ["10 秒", "30 秒", "60 秒"] }
    ];
  }
  if (text.includes("resolver") || text.includes("link")) {
    return [
      { label: "网络请求", type: "chips", chips: ["HTTP", "HTTPS"] },
      { label: "提取正文", type: "switch", enabled: true },
      { label: "超时时间", type: "select", value: "8 秒", options: ["5 秒", "8 秒", "15 秒"] },
      { label: "最大页面大小", type: "select", value: "2 MB", options: ["1 MB", "2 MB", "5 MB"] }
    ];
  }
  if (text.includes("llm") || text.includes("skill")) {
    return [
      { label: "操作范围", type: "select", value: "当前激活配置", options: ["当前激活配置", "所有配置"] },
      { label: "主人校验", type: "switch", enabled: true },
      { label: "模型列表校验", type: "switch", enabled: true }
    ];
  }
  return [
    { label: "自动启用", type: "switch", enabled: plugin.enabled },
    { label: "分类", type: "chips", chips: [pluginCategoryLabel(plugin)] }
  ];
}

function pluginInfoRows(plugin: PluginState): PluginDetailRow[] {
  return [
    { label: "作者", value: pluginAuthor(plugin) },
    { label: "安装状态", value: plugin.installed ? "已安装" : "未安装" },
    { label: "来源", value: pluginSourceLabel(plugin) },
    { label: "版本", value: `v${plugin.manifest.version || "0.0.0"}` }
  ];
}

function selectPlugin(plugin: PluginState) {
  selectedPluginID.value = plugin.manifest.id;
  pluginDetailOpen.value = true;
}

function closePluginDetail() {
  pluginDetailOpen.value = false;
}

// 配置列表默认直接展示已保存档案，搜索会匹配名称、模型、地址和 provider。
const filteredLLMProfiles = computed(() => {
  const keyword = llmProfileQuery.value.trim().toLowerCase();
  if (!keyword) {
    return llmProfiles.value;
  }
  return llmProfiles.value.filter((profile) => {
    const haystack = [
      profile.name,
      profile.description,
      profile.group,
      profile.id,
      profile.model,
      profile.base_url,
      profile.api_format === "chat_completions" ? "chat completions" : "responses api",
      providerDisplayLabel(profile.provider),
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    return haystack.includes(keyword);
  });
});
const llmDirty = computed(() => JSON.stringify(llmPayload()) !== JSON.stringify(lastSavedLLMPayload.value));
const botDirty = computed(() => JSON.stringify(botPayload()) !== JSON.stringify(lastSavedBotPayload.value));
const imageModelOptions = computed(() => imageModelPresets[llmForm.provider] || []);
const modelOptions = computed(() => {
  const byID = new Map<string, LLMModelInfo>();
  for (const item of [...(textModelPresets[llmForm.provider] || []), ...fetchedModelOptions.value]) {
    if (item.id) {
      byID.set(item.id, item);
    }
  }
  const currentModel = llmForm.model.trim();
  if (currentModel && !byID.has(currentModel)) {
    byID.set(currentModel, { id: currentModel, name: "Current" });
  }
  return [...byID.values()];
});
const filteredModelOptions = computed(() => {
  const keyword = llmForm.model.trim().toLowerCase();
  if (!keyword) {
    return modelOptions.value;
  }
  return modelOptions.value.filter((option) => {
    const haystack = [option.id, option.name, option.owned_by].filter(Boolean).join(" ").toLowerCase();
    return haystack.includes(keyword);
  });
});
const activeTestSession = computed(() => testSessions.value.find((session) => session.id === selectedTestSessionID.value) || testSessions.value[0]);
const activeTestHistory = computed(() => activeTestSession.value?.history || []);
const latestTestResult = computed(() => [...activeTestHistory.value].reverse().find((item) => item.role !== "user") || null);
const lastUserTestMessage = computed(() => [...activeTestHistory.value].reverse().find((item) => item.role === "user")?.text || "");
const activeTestScenarioLabel = computed(() => {
  if (activeTestSession.value?.kind === "tool") return "工具调用场景";
  if (activeTestSession.value?.kind === "document") return "文档问答场景";
  if (activeTestSession.value?.kind === "private") return "私聊回复场景";
  return "日程预订场景";
});
const testGroupID = "123456";
const testPrivateUserID = "10001";
const testMemberCount = 6;
const testToolCallCount = computed(() => activeTestHistory.value.filter((item) => item.role !== "user" && item.ok).length);
const testLatencyLabel = computed(() => (activeTestSession.value?.latencyMS ? `${activeTestSession.value.latencyMS}ms` : "-"));
const dashboardBotTone = computed(() => {
  if (!botStatus.value?.running || !activeBotProfile.value?.enabled) return "idle";
  return botStatus.value.channel.connected ? "ok" : "bad";
});
const dashboardBotStateLabel = computed(() => {
  if (!activeBotProfile.value?.enabled) return "未启用";
  if (!botStatus.value?.running) return "未运行";
  return botStatus.value.channel.connected ? "在线" : "离线";
});
const dashboardBotDetail = computed(() => {
  const endpoint = botStatus.value?.channel.endpoint || activeBotProfile.value?.onebot_reverse_ws_endpoint || "-";
  return `${botSummaryName.value} · ${endpoint}`;
});
const dashboardRecentEvents = computed(() => botRecentEvents.value.slice(0, 5));
const dashboardLogs = computed(() => appLogs.value.slice(0, 5));
const dashboardPlugins = computed(() => plugins.value.filter((plugin) => plugin.installed).slice(0, 5));
const dashboardTasks = computed(() => qqbotTasks.value.slice(0, 5));
const activeSubscriptionCount = computed(() => qqbotTasks.value.filter((task) => task.kind === "schedule" && task.status === "active").length);
const enabledWebSearchCount = computed(() => webSearchConfig.providers.filter((provider) => !provider.disabled && webSearchProviderReady(provider)).length);
const dashboardStats = computed(() => qqbotDashboardStats.value);
const dashboardServer = computed(() => dashboardStats.value?.server);
const dashboardServerCPUPercent = computed(() => clampDashboardPercent(dashboardServer.value?.cpu_usage_percent ?? 0));
const dashboardServerMemoryPercent = computed(() => clampDashboardPercent(dashboardServer.value?.memory_usage_percent ?? 0));
const dashboardServerSubtitle = computed(() => {
  const server = dashboardServer.value;
  if (!server) return "等待采样";
  const host = server.hostname || "localhost";
  return `${host} · ${server.os}/${server.arch}`;
});
const dashboardServerRuntimeLabel = computed(() => {
  const seconds = dashboardServer.value?.process_uptime_seconds ?? 0;
  if (seconds <= 0) return "运行时长 -";
  return `运行 ${formatDurationShort(seconds)}`;
});
const dashboardReplyRate = computed(() => {
  const stats = dashboardStats.value;
  if (!stats || stats.received_messages <= 0) return 0;
  return Math.min(100, Math.round((stats.replied_messages / stats.received_messages) * 100));
});
const dashboardOperationBars = computed<Array<{ label: string; value: string; percent: number; target: TabID }>>(() => {
  const stats = dashboardStats.value;
  const measures = stats?.operation_breakdown?.length
    ? stats.operation_breakdown
    : [
        { label: "文本回复", value: stats?.text_replies ?? 0 },
        { label: "生图/修图", value: (stats?.image_generations ?? 0) + (stats?.image_edits ?? 0) },
        { label: "联网搜索", value: stats?.search_calls ?? 0 },
        { label: "LLM API", value: stats?.llm_calls ?? 0 }
      ];
  const maxValue = Math.max(1, ...measures.map((item) => item.value));
  return measures.map((item) => ({
    label: item.label,
    value: formatStatNumber(item.value),
    percent: item.value > 0 ? Math.max(2, Math.round((item.value / maxValue) * 100)) : 0,
    target: dashboardTargetForMetric(item.label)
  }));
});
const dashboardHourlyMax = computed(() => {
  const buckets = dashboardStats.value?.hourly || [];
  return Math.max(1, ...buckets.map((item) => Math.max(item.messages, item.replies, item.searches + item.images)));
});
const dashboardHourlyBars = computed(() =>
  (dashboardStats.value?.hourly || []).map((item) => ({
    ...item,
    messagePercent: item.messages > 0 ? Math.max(3, Math.round((item.messages / dashboardHourlyMax.value) * 100)) : 0,
    replyPercent: item.replies > 0 ? Math.max(3, Math.round((item.replies / dashboardHourlyMax.value) * 100)) : 0,
    toolPercent: item.searches + item.images > 0 ? Math.max(3, Math.round(((item.searches + item.images) / dashboardHourlyMax.value) * 100)) : 0
  }))
);
const dashboardMetrics = computed<Array<{ label: string; value: string; detail: string; icon: IconComponent; target: TabID }>>(() => [
  {
    label: "今日消息",
    value: formatStatNumber(dashboardStats.value?.received_messages ?? 0),
    detail: dashboardStats.value?.since ? `从 ${formatDashboardTime(dashboardStats.value.since)} 起` : "收到的群聊和私聊消息",
    icon: MessageCircle,
    target: "logs"
  },
  {
    label: "今日成员",
    value: formatStatNumber(dashboardStats.value?.active_members ?? 0),
    detail: "按 QQ 号去重的今日发言成员",
    icon: Users,
    target: "group-admin"
  },
  {
    label: "今日回复",
    value: formatStatNumber(dashboardStats.value?.replied_messages ?? 0),
    detail: `回复率 ${dashboardReplyRate.value}%`,
    icon: Send,
    target: "logs"
  },
  {
    label: "生图/修图",
    value: formatStatNumber((dashboardStats.value?.image_generations ?? 0) + (dashboardStats.value?.image_edits ?? 0)),
    detail: `${formatStatNumber(dashboardStats.value?.image_generations ?? 0)} 次生图 / ${formatStatNumber(dashboardStats.value?.image_edits ?? 0)} 次修图`,
    icon: Image,
    target: "plugins"
  },
  {
    label: "联网搜索",
    value: formatStatNumber(dashboardStats.value?.search_calls ?? 0),
    detail: `${enabledWebSearchCount.value}/${webSearchConfig.providers.length} 个搜索源可用`,
    icon: Search,
    target: "web-search"
  },
  {
    label: "API 调用",
    value: formatStatNumber(dashboardStats.value?.api_calls ?? 0),
    detail: `${formatStatNumber(dashboardStats.value?.llm_calls ?? 0)} 次 LLM 调用`,
    icon: Activity,
    target: "logs"
  },
  {
    label: "Token 消耗",
    value: formatCompactNumber(dashboardStats.value?.llm_total_tokens ?? 0),
    detail: `输入 ${formatCompactNumber(dashboardStats.value?.llm_input_tokens ?? 0)} / 输出 ${formatCompactNumber(dashboardStats.value?.llm_output_tokens ?? 0)}`,
    icon: Zap,
    target: "llm"
  }
]);
const dashboardHealthItems = computed<Array<{ label: string; value: string; detail: string; tone: string; icon: IconComponent }>>(() => [
  {
    label: "OneBot",
    value: botStatus.value?.channel.connected ? "已连接" : "未连接",
    detail: botStatus.value?.channel.self_id ? `Self ID ${botStatus.value.channel.self_id}` : botStatus.value?.channel.last_error || botEndpointLabel.value,
    tone: botStatus.value?.channel.connected ? "ok" : "bad",
    icon: Wifi
  },
  {
    label: "LLM",
    value: profileKeyConfigured(activeLLMProfile.value) ? "Key 已配置" : "Key 未配置",
    detail: providerModelLabel(activeLLMProfile.value),
    tone: profileKeyConfigured(activeLLMProfile.value) ? "ok" : "bad",
    icon: BrainCircuit
  },
  {
    label: "后台任务",
    value: `${botStatus.value?.active_workers ?? 0} worker / ${botStatus.value?.active_subagent_tasks ?? 0} subagent`,
    detail: "当前正在处理的任务",
    tone: (botStatus.value?.active_workers ?? 0) + (botStatus.value?.active_subagent_tasks ?? 0) > 0 ? "busy" : "ok",
    icon: Activity
  },
  {
    label: "系统更新",
    value: systemHasUpdate.value ? `落后 ${updateStatus.value?.behind ?? 0}` : "已同步",
    detail: updateStatus.value?.upstream || "本地仓库",
    tone: systemHasUpdate.value ? "warn" : "ok",
    icon: RefreshCw
  }
]);
const logKindLabel = computed(() => (logKind.value === "error" ? "错误日志" : "操作日志"));
const latestAppLog = computed(() => appLogs.value[0] || null);
const operationLogCount = computed(() => appLogs.value.filter((entry) => entry.kind === "operation").length);
const errorLogCount = computed(() => appLogs.value.filter((entry) => entry.kind === "error").length);
const logViewOptions = computed<Array<{ value: LogViewFilter; label: string; count: number }>>(() => [
  { value: "all", label: "全部日志", count: appLogs.value.length },
  { value: "operation", label: "操作日志", count: operationLogCount.value },
  { value: "error", label: "错误日志", count: errorLogCount.value }
]);
const filteredAppLogs = computed(() => {
  const keyword = logQuery.value.trim().toLowerCase();
  const startTime = parseLogDateBoundary(logStartDate.value, "start");
  const endTime = parseLogDateBoundary(logEndDate.value, "end");
  return appLogs.value.filter((entry) => {
    if (logView.value !== "all" && entry.kind !== logView.value) {
      return false;
    }
    if (logLevelFilter.value !== "all" && logLevelValue(entry) !== logLevelFilter.value) {
      return false;
    }
    if (startTime !== null || endTime !== null) {
      const createdAt = new Date(entry.created_at).getTime();
      if (Number.isNaN(createdAt)) {
        return false;
      }
      if (startTime !== null && createdAt < startTime) {
        return false;
      }
      if (endTime !== null && createdAt > endTime) {
        return false;
      }
    }
    if (!keyword) {
      return true;
    }
    return logSearchText(entry).includes(keyword);
  });
});
const logPageCount = computed(() => Math.max(1, Math.ceil(filteredAppLogs.value.length / logPageSize.value)));
const pagedAppLogs = computed(() => {
  const page = Math.min(logPage.value, logPageCount.value);
  const start = (page - 1) * logPageSize.value;
  return filteredAppLogs.value.slice(start, start + logPageSize.value);
});
const logPageNumbers = computed(() => {
  const total = logPageCount.value;
  const visible = Math.min(5, total);
  const start = Math.max(1, Math.min(logPage.value - 2, total - visible + 1));
  return Array.from({ length: visible }, (_, index) => start + index);
});
watch(
  () => `${selectedTestSessionID.value}:${activeTestHistory.value.length}`,
  async () => {
    await nextTick();
    const stream = testMessageStreamRef.value;
    if (stream) {
      stream.scrollTop = stream.scrollHeight;
    }
  }
);
const themeMode = computed(() => themePreferences.mode);
const resolvedTheme = computed<"light" | "dark">(() => {
  if (themePreferences.mode === "system") {
    return systemPrefersDark.value ? "dark" : "light";
  }
  return themePreferences.mode;
});
const activeThemeAccent = computed(() => themeAccentOptions.find((item) => item.id === themePreferences.accent) || themeAccentOptions[0]);
const themeModeLabel = computed(() => {
  if (themePreferences.mode === "system") {
    return `跟随系统 · 当前${resolvedTheme.value === "dark" ? "深色" : "浅色"}`;
  }
  return themePreferences.mode === "dark" ? "深色模式" : "浅色模式";
});
const themeAccentLabel = computed(() => activeThemeAccent.value.label);
const themeDensityLabel = computed(() => (themePreferences.density === "compact" ? "紧凑布局" : "舒适布局"));
// 主题变量统一在这里计算，页面里的阴影、柔和表面和按钮强调色都会跟着切换。
const themeStyleVars = computed<Record<string, string>>(() => {
  const accent = activeThemeAccent.value;
  const isDark = resolvedTheme.value === "dark";
  const compact = themePreferences.density === "compact";
  const panelSoft = themePreferences.softSurface ? (isDark ? accent.surfaceDark : accent.surfaceLight) : isDark ? "#241b23" : "#ffffff";
  const controlSoft = themePreferences.softSurface ? (isDark ? accent.controlDark : accent.controlLight) : isDark ? "#2b212a" : "#ffffff";
  const cardShadow = themePreferences.shadows ? accent.shadowLight : "none";
  const floatShadow = themePreferences.shadows ? (isDark ? accent.shadowDark : accent.shadowLight) : "none";
  const buttonShadow = themePreferences.shadows ? `0 12px 24px ${hexToRGBA(accent.primary, isDark ? 0.2 : 0.22)}` : "none";
  return {
    "--primary": accent.primary,
    "--primary-strong": accent.strong,
    "--focus": accent.focus,
    "--panel-soft": panelSoft,
    "--control-soft": controlSoft,
    "--elevation-panel": floatShadow,
    "--elevation-card": cardShadow,
    "--elevation-float": floatShadow,
    "--elevation-button-primary": buttonShadow,
    "--control-height": compact ? "38px" : "42px",
    "--control-height-sm": compact ? "28px" : "32px",
    "--control-padding-x": compact ? "11px" : "12px",
    "--panel-padding": compact ? "16px" : "20px",
    "--panel-gap": compact ? "12px" : "16px",
    "--preview-accent": accent.primary
  };
});
const systemHasUpdate = computed(() => Boolean(updateStatus.value?.update_available || (updateStatus.value?.behind ?? 0) > 0));
const systemEntryTitle = computed(() => {
  if (!updateStatus.value) return "系统更新";
  return `${systemVersionLabel.value} · ${systemUpdateAvailabilityText.value}`;
});
// 头部弹层只展示适合人看的版本串，这里把分支和短提交号拼成一个紧凑标签。
const systemVersionLabel = computed(() => {
  const branch = updateStatus.value?.branch?.trim() || "";
  const commit = (updateStatus.value?.head_commit || "").trim();
  const shortCommit = commit ? commit.slice(0, 7) : "";
  if (branch && shortCommit) return `${branch}@${shortCommit}`;
  return branch || shortCommit || "读取中";
});
const systemRunningVersionLabel = computed(() => {
  const commit = (updateStatus.value?.running_commit || "").trim();
  return commit ? commit.slice(0, 7) : "开发构建";
});
const systemUpdateAvailabilityText = computed(() => {
  if (!updateStatus.value) return "读取中";
  if (updatingSystem.value || updateStatus.value.updating) return "正在拉取、构建并替换本地程序，请保持页面开启";
  if (updateStatus.value.restart_required) return "新版本已经安装，重启 Diana QQ Bot 后生效";
  if (updateStatus.value.dirty) return "源码目录存在未提交改动，升级已暂停";
  const behind = updateStatus.value.behind ?? 0;
  if (!updateStatus.value.remote_url) return "未配置远端";
  if (behind > 0) return `有 ${behind} 个更新可用`;
  if (updateStatus.value.update_available) return "源码与当前运行版本不一致，可重新构建应用";
  return "当前已是最新";
});
const systemUpdateStateTitle = computed(() => {
  if (updatingSystem.value || loadingUpdateStatus.value || updateStatus.value?.updating) return "正在处理升级";
  if (updateStatus.value?.restart_required) return "升级已就绪";
  if (updateStatus.value?.dirty) return "升级被本地改动阻止";
  if (systemHasUpdate.value) return "发现可用更新";
  return updateStatus.value ? "版本状态正常" : "等待检查";
});
const systemUpdateTone = computed(() => {
  if (updateStatus.value?.dirty || (!updateStatus.value?.apply_supported && Boolean(updateStatus.value))) return "bad";
  if (updateStatus.value?.restart_required) return "success";
  if (systemHasUpdate.value) return "warning";
  return "neutral";
});
const systemUpdateBlockingText = computed(() => {
  if (!updateStatus.value) return "";
  if (updateStatus.value.dirty) return "请先提交或移走源码目录中的本地改动，再执行升级。";
  if (!updateStatus.value.remote_url) return "当前源码目录没有配置 origin，无法在线升级。";
  if (!updateStatus.value.branch) return "当前仓库处于 detached HEAD，无法确定升级分支。";
  if (!updateStatus.value.apply_supported) return "当前部署不支持自动构建替换；Docker 部署请通过镜像更新。";
  return "";
});
const systemUpgradeDisabled = computed(() => {
  const current = updateStatus.value;
  return Boolean(
    updatingSystem.value ||
      loadingUpdateStatus.value ||
      current?.updating ||
      !current ||
      current.dirty ||
      !current.remote_url ||
      !current.branch ||
      !current.apply_supported ||
      current.restart_required
  );
});
const systemUpgradeActionLabel = computed(() => {
  if (updateStatus.value?.restart_required) return "等待重启";
  return systemHasUpdate.value ? "升级并安装" : "重新构建";
});
const systemGitHubURL = computed(() => githubURLFromRemote(updateStatus.value?.remote_url || ""));
const systemUpdateNote = computed(() => {
  return updateStatus.value?.head_subject || systemUpdateAvailabilityText.value;
});
const lastSavedLLMPayload = ref<LLMConfig | null>(null);
const lastSavedBotPayload = ref<QQBotConfig | null>(null);
const themeStorageKey = "webui-theme-v2";
const uiStateStorageKey = "webui-ui-state";
let colorSchemeQuery: MediaQueryList | null = null;

function setStatus(text: string, kind = "") {
  status.text = text;
  status.kind = kind;
}

function defaultThemePreferences(): ThemePreferenceState {
  return {
    mode: "system",
    accent: "rose",
    density: "comfortable",
    shadows: true,
    softSurface: true
  };
}

function isThemeAccentID(value: unknown): value is ThemeAccentID {
  return typeof value === "string" && themeAccentOptions.some((item) => item.id === value);
}

function normalizeThemePreferences(raw: unknown): ThemePreferenceState {
  const defaults = defaultThemePreferences();
  if (raw === "light" || raw === "dark") {
    return { ...defaults, mode: raw };
  }
  if (!raw || typeof raw !== "object") {
    return defaults;
  }
  const payload = raw as Partial<ThemePreferenceState>;
  return {
    mode: payload.mode === "light" || payload.mode === "dark" || payload.mode === "system" ? payload.mode : defaults.mode,
    accent: isThemeAccentID(payload.accent) ? payload.accent : defaults.accent,
    density: payload.density === "compact" || payload.density === "comfortable" ? payload.density : defaults.density,
    shadows: typeof payload.shadows === "boolean" ? payload.shadows : defaults.shadows,
    softSurface: typeof payload.softSurface === "boolean" ? payload.softSurface : defaults.softSurface
  };
}

function loadTheme() {
  const saved = localStorage.getItem(themeStorageKey);
  if (!saved) {
    return;
  }
  try {
    Object.assign(themePreferences, normalizeThemePreferences(JSON.parse(saved)));
  } catch {
    Object.assign(themePreferences, normalizeThemePreferences(saved));
  }
}

function loadUIState() {
  const raw = localStorage.getItem(uiStateStorageKey);
  if (!raw) {
    return;
  }
  try {
    const state = JSON.parse(raw) as {
      activeTab?: TabID;
      llmAdvancedOpen?: boolean;
      botSectionsOpen?: typeof botSectionsOpen;
      pluginFilter?: PluginFilter;
      pluginCategory?: PluginCategory;
      pluginQuery?: string;
      selectedPluginID?: string;
      pluginDetailOpen?: boolean;
      botPlatformFilter?: BotPlatformFilter;
      botDetailTab?: BotDetailTab;
      botPageSize?: number;
      logKind?: AppLogKind;
      logView?: LogViewFilter;
      logQuery?: string;
      logLevelFilter?: LogLevelFilter;
      logStartDate?: string;
      logEndDate?: string;
      logPageSize?: number;
      updateHistory?: UpdateHistoryItem[];
      message?: string;
      testHistory?: TestHistoryItem[];
      testSessions?: PersistedTestSession[];
      selectedTestSessionID?: string;
      testConversationMode?: "group" | "private";
      recordTestContext?: boolean;
      lastTestLatencyMS?: number | null;
    };
    if (state.activeTab && tabs.some((tab) => tab.id === state.activeTab)) {
      activeTab.value = state.activeTab;
    }
    if (typeof state.llmAdvancedOpen === "boolean") {
      llmAdvancedOpen.value = state.llmAdvancedOpen;
    }
    if (state.botSectionsOpen) {
      botSectionsOpen.connection = state.botSectionsOpen.connection;
      botSectionsOpen.groups = state.botSectionsOpen.groups;
      botSectionsOpen.replies = state.botSectionsOpen.replies;
      botSectionsOpen.automation = state.botSectionsOpen.automation;
    }
    if (state.pluginFilter) {
      pluginFilter.value = state.pluginFilter;
    }
    if (state.pluginCategory) {
      pluginCategory.value = state.pluginCategory;
    }
    if (typeof state.pluginQuery === "string") {
      pluginQuery.value = state.pluginQuery;
    }
    if (typeof state.selectedPluginID === "string") {
      selectedPluginID.value = state.selectedPluginID;
    }
    if (typeof state.pluginDetailOpen === "boolean") {
      pluginDetailOpen.value = state.pluginDetailOpen;
    }
    if (
      state.botPlatformFilter === "all" ||
      state.botPlatformFilter === "telegram" ||
      state.botPlatformFilter === "qq" ||
      state.botPlatformFilter === "discord" ||
      state.botPlatformFilter === "slack" ||
      state.botPlatformFilter === "wechat" ||
      state.botPlatformFilter === "custom"
    ) {
      botPlatformFilter.value = state.botPlatformFilter;
    }
    if (state.botDetailTab === "config" || state.botDetailTab === "prompts" || state.botDetailTab === "events" || state.botDetailTab === "messages" || state.botDetailTab === "test") {
      botDetailTab.value = state.botDetailTab;
    }
    if (typeof state.botPageSize === "number" && [10, 20, 50].includes(state.botPageSize)) {
      botPageSize.value = state.botPageSize;
    }
    if (state.logKind === "operation" || state.logKind === "error") {
      logKind.value = state.logKind;
    }
    if (state.logView === "all" || state.logView === "operation" || state.logView === "error") {
      logView.value = state.logView;
    } else if (state.logKind === "operation" || state.logKind === "error") {
      logView.value = state.logKind;
    }
    if (typeof state.logQuery === "string") {
      logQuery.value = state.logQuery;
    }
    if (state.logLevelFilter === "all" || state.logLevelFilter === "info" || state.logLevelFilter === "warn" || state.logLevelFilter === "error") {
      logLevelFilter.value = state.logLevelFilter;
    }
    if (typeof state.logStartDate === "string") {
      logStartDate.value = state.logStartDate;
    }
    if (typeof state.logEndDate === "string") {
      logEndDate.value = state.logEndDate;
    }
    if (typeof state.logPageSize === "number" && logPageSizeOptions.includes(state.logPageSize)) {
      logPageSize.value = state.logPageSize;
    }
    if (Array.isArray(state.updateHistory)) {
      updateHistory.value = state.updateHistory;
    }
    if (typeof state.message === "string") {
      message.value = state.message;
    }
    if (Array.isArray(state.testSessions)) {
      testSessions.value = state.testSessions.map((session, index) => normalizeTestSession(session, defaultTestSessions[index])).slice(0, 8);
    }
    if (typeof state.selectedTestSessionID === "string") {
      selectedTestSessionID.value = state.selectedTestSessionID;
    }
    if (!testSessions.value.some((session) => session.id === selectedTestSessionID.value)) {
      selectedTestSessionID.value = testSessions.value[0]?.id || defaultTestSessions[0].id;
    }
    if (Array.isArray(state.testHistory) && state.testHistory.length > 0) {
      const targetID = selectedTestSessionID.value;
      testSessions.value = testSessions.value.map((session) =>
        session.id === targetID && session.history.length === 0
          ? {
              ...session,
              history: state.testHistory!.slice(-20),
              output: state.testHistory![state.testHistory!.length - 1]?.text || session.output,
              latencyMS: typeof state.lastTestLatencyMS === "number" ? state.lastTestLatencyMS : session.latencyMS
            }
          : session
      );
    }
    if (state.testConversationMode === "group" || state.testConversationMode === "private") {
      testConversationMode.value = state.testConversationMode;
    }
    if (typeof state.recordTestContext === "boolean") {
      recordTestContext.value = state.recordTestContext;
    }
  } catch {
    localStorage.removeItem(uiStateStorageKey);
  }
}

function persistUIState() {
  localStorage.setItem(
    uiStateStorageKey,
    JSON.stringify({
      activeTab: activeTab.value,
      llmAdvancedOpen: llmAdvancedOpen.value,
      botSectionsOpen: { ...botSectionsOpen },
      pluginFilter: pluginFilter.value,
      pluginCategory: pluginCategory.value,
      pluginQuery: pluginQuery.value,
      selectedPluginID: selectedPluginID.value,
      pluginDetailOpen: pluginDetailOpen.value,
      botPlatformFilter: botPlatformFilter.value,
      botDetailTab: botDetailTab.value,
      botPageSize: botPageSize.value,
      logKind: logKind.value,
      logView: logView.value,
      logQuery: logQuery.value,
      logLevelFilter: logLevelFilter.value,
      logStartDate: logStartDate.value,
      logEndDate: logEndDate.value,
      logPageSize: logPageSize.value,
      updateHistory: updateHistory.value.slice(-10),
      message: message.value,
      testSessions: testSessions.value.slice(0, 8).map(serializeTestSession),
      selectedTestSessionID: selectedTestSessionID.value,
      testConversationMode: testConversationMode.value,
      recordTestContext: recordTestContext.value
    })
  );
}

function persistThemePreferences() {
  localStorage.setItem(themeStorageKey, JSON.stringify(themePreferences));
}

function setThemeMode(mode: ThemeMode) {
  themePreferences.mode = mode;
}

function setThemeAccent(accent: ThemeAccentID) {
  themePreferences.accent = accent;
}

function setThemeDensity(density: ThemeDensity) {
  themePreferences.density = density;
}

function resetThemePreferences() {
  Object.assign(themePreferences, defaultThemePreferences());
  setStatus("主题已恢复默认", "ok");
}

function syncSystemTheme(query?: MediaQueryList | MediaQueryListEvent) {
  const matches = query ? query.matches : colorSchemeQuery?.matches;
  systemPrefersDark.value = Boolean(matches);
}

function hexToRGBA(hex: string, alpha: number): string {
  const normalized = hex.replace("#", "").trim();
  if (normalized.length !== 6) {
    return `rgba(255, 111, 157, ${alpha})`;
  }
  const r = Number.parseInt(normalized.slice(0, 2), 16);
  const g = Number.parseInt(normalized.slice(2, 4), 16);
  const b = Number.parseInt(normalized.slice(4, 6), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

function githubURLFromRemote(remote: string): string {
  const raw = remote.trim();
  if (!raw) {
    return "";
  }
  if (raw.startsWith("git@github.com:")) {
    return `https://github.com/${raw.slice("git@github.com:".length).replace(/\.git$/, "")}`;
  }
  if (raw.startsWith("ssh://git@github.com/")) {
    return `https://github.com/${raw.slice("ssh://git@github.com/".length).replace(/\.git$/, "")}`;
  }
  try {
    const parsed = new URL(raw);
    if (parsed.hostname !== "github.com") {
      return "";
    }
    parsed.protocol = "https:";
    parsed.username = "";
    parsed.password = "";
    parsed.pathname = parsed.pathname.replace(/\.git$/, "");
    parsed.search = "";
    parsed.hash = "";
    return parsed.toString().replace(/\/$/, "");
  } catch {
    return "";
  }
}

function confirmDiscardChanges(kind: "LLM" | "机器人" | "联网搜索"): boolean {
  const dirty = kind === "LLM" ? llmEditorMode.value === "edit" && llmDirty.value : kind === "机器人" ? botDirty.value : webSearchDirty.value;
  if (!dirty) {
    return true;
  }
  return window.confirm(`${kind} 配置还有未保存改动，确定继续切换吗？`);
}

function tabFromPathname(pathname: string): TabID | null {
  const path = pathname.replace(/\/+$/, "") || "/";
  return routeTabs.get(path) || null;
}

function tabRoute(tab: TabID): string {
  return tabRoutes[tab] || tabRoutes.llm;
}

function dashboardTargetForMetric(label: string): TabID {
  if (label.includes("生图") || label.includes("修图") || label.includes("插件")) {
    return "plugins";
  }
  if (label.includes("搜索")) {
    return "web-search";
  }
  if (label.includes("Token")) {
    return "llm";
  }
  return "logs";
}

function activateTab(tab: TabID): boolean {
  if (activeTab.value === tab) {
    return true;
  }
  if (activeTab.value === "llm" && !confirmDiscardChanges("LLM")) {
    return false;
  }
  if (activeTab.value === "qqbot" && !confirmDiscardChanges("机器人")) {
    return false;
  }
  if (activeTab.value === "web-search" && !confirmDiscardChanges("联网搜索")) {
    return false;
  }
  activeTab.value = tab;
  if (tab === "logs") {
    void refreshLogs(false);
  }
  if (tab === "security") {
    void refreshAdminSessions(false);
  }
  return true;
}

function replaceCurrentRoute(tab: TabID) {
  const route = tabRoute(tab);
  if (window.location.pathname !== route) {
    window.history.replaceState({ tab }, "", route);
  }
}

function selectTab(tab: TabID) {
  if (!activateTab(tab)) {
    replaceCurrentRoute(activeTab.value);
    return;
  }
  const route = tabRoute(tab);
  if (window.location.pathname !== route) {
    window.history.pushState({ tab }, "", route);
  }
}

function syncTabFromRoute() {
  const tab = tabFromPathname(window.location.pathname);
  if (!tab) {
    replaceCurrentRoute(activeTab.value);
    return;
  }
  if (!activateTab(tab)) {
    replaceCurrentRoute(activeTab.value);
  }
}

// 更新抽屉打开时顺手刷新仓库状态，用户拿到的总是最新结果。
function toggleUpdateDrawer() {
  updateDrawerOpen.value = !updateDrawerOpen.value;
  if (updateDrawerOpen.value && !loadingUpdateStatus.value) {
    void refreshUpdateStatus(false, true);
  }
}

function closeUpdateDrawer() {
  updateDrawerOpen.value = false;
}

function setProvider(provider: Provider) {
  const currentProvider = llmForm.provider;
  const currentTextModel = llmForm.model;
  const currentImageModel = llmForm.imageModel;
  llmForm.provider = provider;
  fetchedModelOptions.value = [];
  modelMenuOpen.value = false;
  if (!currentTextModel || isPresetModel(currentProvider, currentTextModel)) {
    llmForm.model = defaultTextModel(provider);
  }
  if (!llmForm.userAgent || llmForm.userAgent === "diana-qq-bot") {
    llmForm.userAgent = defaultUserAgent(provider);
  }
  if (!currentImageModel || imageModelPresets[currentProvider]?.includes(currentImageModel)) {
    llmForm.imageModel = defaultImageModel(provider);
  }
}

function toggleModelMenu() {
  modelMenuOpen.value = !modelMenuOpen.value;
}

function selectModel(id: string) {
  llmForm.model = id;
  applyModelLimits(modelOptions.value.find((option) => option.id === id));
  modelMenuOpen.value = false;
}

function applyModelLimits(model?: LLMModelInfo) {
  const contextWindow = model?.context_window_tokens || defaultContextWindowTokens;
  llmForm.contextWindowTokens = contextWindow;
  llmForm.maxContextTokens = Math.min(llmForm.maxContextTokens || defaultMaxContextTokens, contextWindow);
  if (model?.max_output_tokens && llmForm.maxOutputTokens > 0) {
    llmForm.maxOutputTokens = Math.min(llmForm.maxOutputTokens, model.max_output_tokens);
  }
  if (llmForm.maxOutputTokens > 0 && llmForm.maxOutputTokens >= llmForm.maxContextTokens) {
    llmForm.maxOutputTokens = Math.max(1, Math.floor(llmForm.maxContextTokens / 4));
  }
}

function onGlobalPointerDown(event: MouseEvent) {
  const target = event.target as Node;
  const element = event.target instanceof Element ? event.target : null;
  if (modelMenuOpen.value && !modelSelectRef.value?.contains(target)) {
    modelMenuOpen.value = false;
  }
  if (llmMoreMenuProfileID.value && !element?.closest(".llm-more-wrap")) {
    closeLLMMoreMenu();
  }
}

function applyLLMConfig(config: LLMConfig) {
  llmProfiles.value = config.profiles || [];
  llmActiveProfileID.value = config.active_profile_id || config.id || llmProfiles.value[0]?.id || "";
  llmForm.id = config.id || "";
  llmForm.name = config.name || "默认配置";
  llmForm.group = config.group || "default";
  llmForm.description = config.description || "";
  llmForm.provider = config.provider || "openai_compatible";
  llmForm.model = config.model || defaultTextModel(llmForm.provider);
  llmForm.imageModel = config.image_model || defaultImageModel(llmForm.provider);
  llmForm.imageBaseURL = config.image_base_url || "";
  llmForm.imageOrigin = config.image_origin || "";
  llmForm.imageTimeoutMS = config.image_timeout_ms || 0;
  llmForm.userAgent = config.user_agent || defaultUserAgent(llmForm.provider);
  llmForm.apiKey = config.api_key || "";
  llmForm.apiKeyConfigured = Boolean(config.api_key_configured || config.api_key);
  llmForm.baseURL = config.base_url || "";
  llmForm.apiFormat = config.api_format || "responses";
  llmHeaderRows.value = headerRowsFromConfig(config.headers);
  llmForm.temperature = config.temperature ?? null;
  llmForm.reasoningEffort = config.reasoning_effort || "";
  llmForm.contextWindowTokens = config.context_window_tokens || defaultContextWindowTokens;
  llmForm.maxContextTokens = Math.min(config.max_context_tokens || defaultMaxContextTokens, llmForm.contextWindowTokens);
  llmForm.maxOutputTokens = config.max_output_tokens || 0;
  llmForm.timeoutMS = config.timeout_ms || 0;
  llmAdvancedOpen.value = Boolean(
    (llmForm.imageModel && llmForm.imageModel !== defaultImageModel(llmForm.provider)) ||
      Boolean(llmForm.imageBaseURL || llmForm.imageOrigin || llmForm.imageTimeoutMS) ||
      (llmForm.provider === "openai_compatible" && llmForm.userAgent && llmForm.userAgent !== defaultUserAgent(llmForm.provider)) ||
      llmForm.temperature !== null ||
      Boolean(llmForm.reasoningEffort) ||
      llmForm.maxContextTokens !== defaultMaxContextTokens ||
      (llmForm.maxOutputTokens && llmForm.maxOutputTokens !== defaultMaxOutputTokens) ||
      (llmForm.timeoutMS && llmForm.timeoutMS !== defaultTimeoutMS)
  );
  lastSavedLLMPayload.value = llmPayload();
}

async function refreshLLMConfig() {
  applyLLMConfig(await getConfig(true));
}

function resetLLMChanges() {
  if (!lastSavedLLMPayload.value) {
    return;
  }
  applyLLMConfig({
    ...lastSavedLLMPayload.value,
    profiles: llmProfiles.value
  });
  setStatus("LLM 改动已撤销", "ok");
}

function editProfile(profile: LLMConfig) {
  if (!confirmDiscardChanges("LLM")) {
    return;
  }
  closeLLMMoreMenu();
  applyLLMConfig({
    ...profile,
    active_profile_id: llmActiveProfileID.value,
    profiles: llmProfiles.value
  });
  llmEditorMode.value = "edit";
  modelMenuOpen.value = false;
}

function closeLLMEditor() {
  if (!confirmDiscardChanges("LLM")) {
    return;
  }
  if (activeLLMProfile.value) {
    applyLLMConfig({
      ...activeLLMProfile.value,
      active_profile_id: llmActiveProfileID.value,
      profiles: llmProfiles.value
    });
  }
  llmEditorMode.value = "list";
  modelMenuOpen.value = false;
}

function llmPayload(): LLMConfig {
  return {
    id: llmForm.id || undefined,
    name: llmForm.name,
    group: llmForm.group || "default",
    description: llmForm.description,
    active_profile_id: llmForm.id || undefined,
    provider: llmForm.provider,
    api_key: llmForm.apiKey || undefined,
    base_url: llmForm.baseURL,
    api_format: llmForm.provider === "openai_compatible" ? llmForm.apiFormat : undefined,
    model: llmForm.model,
    image_model: llmForm.imageModel || undefined,
    image_base_url: llmForm.imageBaseURL || undefined,
    image_origin: llmForm.imageOrigin || undefined,
    image_timeout_ms: llmForm.imageTimeoutMS || 0,
    user_agent: llmForm.provider === "openai_compatible" ? llmForm.userAgent || undefined : undefined,
    headers: headersFromRows(),
    temperature: llmForm.temperature,
    reasoning_effort: llmForm.provider === "openai_compatible" ? llmForm.reasoningEffort || undefined : undefined,
    context_window_tokens: llmForm.contextWindowTokens,
    max_context_tokens: llmForm.maxContextTokens,
    max_output_tokens: llmForm.maxOutputTokens || 0,
    timeout_ms: llmForm.timeoutMS || 0
  };
}

function selectProfile(id: string) {
  if (!confirmDiscardChanges("LLM")) {
    return;
  }
  const profile = llmProfiles.value.find((item) => item.id === id);
  if (!profile) {
    return;
  }
  applyLLMConfig({
    ...profile,
    active_profile_id: id,
    profiles: llmProfiles.value
  });
  modelMenuOpen.value = false;
}

async function onSelectProfile(id: string) {
  if (!confirmDiscardChanges("LLM")) {
    return;
  }
  try {
    closeLLMMoreMenu();
    await activateConfigProfile(id);
    await refreshLLMConfig();
    llmEditorMode.value = "list";
    setStatus("已切换 LLM 配置", "ok");
  } catch (error) {
    setStatus("切换失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

function createProfile() {
  if (!confirmDiscardChanges("LLM")) {
    return;
  }
  closeLLMMoreMenu();
  showLLMAPIKey.value = false;
  const profile: LLMConfig = {
    id: "",
    name: "",
    group: activeLLMProfile.value?.group || "default",
    description: "",
    provider: "openai_compatible",
    api_format: "responses",
    model: defaultTextModel("openai_compatible"),
    image_model: defaultImageModel("openai_compatible"),
    image_base_url: "",
    image_origin: "",
    image_timeout_ms: 300000,
    user_agent: defaultUserAgent("openai_compatible"),
    headers: undefined,
    temperature: null,
    reasoning_effort: "",
    context_window_tokens: defaultContextWindowTokens,
    max_context_tokens: defaultMaxContextTokens,
    max_output_tokens: defaultMaxOutputTokens,
    timeout_ms: defaultTimeoutMS
  };
  applyLLMConfig({
    ...profile,
    profiles: llmProfiles.value
  });
  lastSavedLLMPayload.value = null;
  llmEditorMode.value = "edit";
  modelMenuOpen.value = false;
}

function defaultImageModel(provider: Provider): string {
  return imageModelPresets[provider]?.[0] || "";
}

function defaultTextModel(provider: Provider): string {
  return textModelPresets[provider]?.[0]?.id || "";
}

function defaultUserAgent(provider: Provider): string {
  return provider === "openai_compatible" ? "diana-qq-bot" : "";
}

function isPresetModel(provider: Provider, model: string): boolean {
  const id = model.trim();
  return Boolean(id && textModelPresets[provider]?.some((option) => option.id === id));
}

function createReplyRule(rule?: ReplyRule): ReplyRuleFormState {
  return {
    id: rule?.id || `rule-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    name: rule?.name || "回复规则",
    enabled: rule?.enabled !== false,
    prompt: rule?.prompt || "",
    action: rule?.action === "voice" ? "voice" : "model",
    llmProfileID: rule?.llm_profile_id || ""
  };
}

function addReplyRule() {
  botForm.replyRules.push(createReplyRule());
  botDetailTab.value = "rules";
}

function removeReplyRule(index: number) {
  botForm.replyRules.splice(index, 1);
}

function replyRuleProfileLabel(profile: LLMConfig): string {
  const name = profile.name || profile.model || "未命名配置";
  return `${name} · ${providerDisplayLabel(profile.provider)} / ${profile.model || "-"}`;
}

function loadBotFormProfile(config: QQBotConfig) {
  botForm.id = config.id || "";
  botForm.name = config.name || "默认机器人";
  botForm.platform = config.platform || "NapCat / OneBot V11";
  botForm.avatarURL = config.avatar_url || "";
  botForm.enabled = Boolean(config.enabled);
  botForm.oneBotEndpoint = config.onebot_reverse_ws_endpoint || "ws://127.0.0.1:18080/onebot/v11/ws";
  botForm.oneBotToken = "";
  botForm.oneBotTokenConfigured = Boolean(config.onebot_access_token_configured);
  botForm.noneBotBridgeEnabled = Boolean(config.nonebot_bridge_enabled);
  botForm.noneBotBridgeEndpoint = config.nonebot_bridge_endpoint || "ws://127.0.0.1:8080/onebot/v11/ws";
  botForm.noneBotBridgeToken = "";
  botForm.noneBotBridgeTokenConfigured = Boolean(config.nonebot_bridge_token_configured);
  botForm.botQQ = config.bot_qq || "";
  botForm.ownerID = config.owner_id || "";
  botForm.groupTriggers = (config.group_triggers || []).join(",");
  botForm.disabledGroups = (config.disabled_groups || []).join(",");
  botForm.disabledUsers = (config.disabled_users || []).join(",");
  botForm.welcomeEnabled = Boolean(config.welcome_enabled);
  botForm.welcomeMessage = config.welcome_message || "欢迎加入本群，直接 @我 或发送触发词就可以开始聊天。";
  botForm.systemPrompt = config.system_prompt || "";
  botForm.passiveReplyRouterPrompt = config.passive_reply_router_prompt || "";
  botForm.passiveReplyPrompt = config.passive_reply_prompt || "";
  botForm.maxInputChars = config.max_input_chars || 2000;
  botForm.maxReplyChars = config.max_reply_chars || 3500;
  botForm.directReplyChunkSize = config.direct_reply_chunk_size || 900;
  botForm.forwardReplyThreshold = config.forward_reply_threshold || 900;
  botForm.recallReplyMode = config.recall_reply_mode || "llm_summary";
  botForm.recallReplyAutoDeleteEnabled = config.recall_reply_auto_delete_enabled !== false;
  botForm.llmQQIDMaskingEnabled = config.llm_qq_id_masking_enabled !== false;
  botForm.recentContextLimit = config.recent_context_limit ?? 20;
  botForm.contextSummaryThreshold = config.context_summary_threshold || 100;
  botForm.passiveReplyChance = config.passive_reply_chance || 1;
  botForm.passiveReplyThreshold = config.passive_reply_threshold || 0.8;
  botForm.replyRules = (config.reply_rules || []).map((rule) => createReplyRule(rule));
  botForm.maxBotConcurrency = config.max_bot_concurrency || 8;
  botForm.requestTimeoutMS = config.request_timeout_ms || 180000;
  botForm.agentEnabled = Boolean(config.agent_enabled);
  botForm.agentWorkDir = config.agent_work_dir || ".";
  botForm.agentMaxSteps = config.agent_max_steps || 8;
  botForm.agentSkillRoots = (config.agent_skill_roots || []).join(",");
  botForm.agentMCPConfigPath = config.agent_mcp_config_path || "";
  botForm.agentCommandAllowlist = (config.agent_command_allowlist || []).join(",");
  botForm.agentCommandTimeoutMS = config.agent_command_timeout_ms || 10000;
  botForm.agentBrowserCDPURL = config.agent_browser_cdp_url || "http://127.0.0.1:9222";
  botForm.agentBrowserTimeoutMS = config.agent_browser_timeout_ms || 15000;
  lastSavedBotPayload.value = botPayload();
}

function applyQQAutoInfo(info?: QQBotAutoInfo | null, options: { persistCandidateOwner?: boolean } = {}): boolean {
  if (!info) return false;
  let changed = false;
  const botQQ = (info.bot_qq || "").trim();
  if (botQQ && botForm.botQQ !== botQQ) {
    botForm.botQQ = botQQ;
    changed = true;
  }
  const avatarURL = (info.avatar_url || "").trim();
  if (avatarURL && botForm.avatarURL !== avatarURL && (!botForm.avatarURL || /qlogo\.cn/.test(botForm.avatarURL))) {
    botForm.avatarURL = avatarURL;
    changed = true;
  }
  const nickname = (info.nickname || "").trim();
  if (nickname && shouldReplaceBotDefaultName(botForm.name)) {
    botForm.name = nickname;
    changed = true;
  }
  const verifiedOwner = groupAdminVerified.value ? groupAdmin.form.userID.trim() : "";
  if (!botForm.ownerID && options.persistCandidateOwner && verifiedOwner) {
    botForm.ownerID = verifiedOwner;
    changed = true;
  }
  const groupID = firstAutoGroupID(info);
  if (groupID && !botGroupTest.groupID) {
    botGroupTest.groupID = groupID;
  }
  if (groupID && !groupAdmin.form.groupID) {
    groupAdmin.form.groupID = groupID;
  }
  return changed;
}

function shouldReplaceBotDefaultName(name: string): boolean {
  const normalized = name.trim();
  return !normalized || normalized === "默认机器人" || normalized === "未命名机器人";
}

function firstAutoGroupID(info: QQBotAutoInfo): string {
  return (info.recent_group_id || info.groups?.[0]?.group_id || "").trim();
}

async function refreshQQBotAutoInfo(options: { save?: boolean; quiet?: boolean } = {}): Promise<boolean> {
  try {
    const info = await getQQBotAutoInfo();
    botAutoInfo.value = info;
    const changed = applyQQAutoInfo(info, { persistCandidateOwner: true });
    if (changed && options.save) {
      applyBotConfig(await saveQQBotConfig(botPayload()));
      await refreshBotStatus();
    } else if (changed) {
      const payload = botPayload();
      patchActiveBotProfile(payload);
    }
    if (!options.quiet) {
      const label = info.bot_qq ? `已获取 QQ ${info.bot_qq}` : "未获取到登录 QQ";
      setStatus(label, info.bot_qq ? "ok" : undefined);
    }
    return changed;
  } catch (error) {
    if (!options.quiet) {
      setStatus("自动获取 QQ 信息失败", "bad");
      output.value = error instanceof Error ? error.message : String(error);
    }
    return false;
  }
}

function patchActiveBotProfile(payload: QQBotConfig) {
  if (!botForm.id) return;
  botProfiles.value = botProfiles.value.map((profile) => {
    if (profile.id !== botForm.id) return profile;
    return {
      ...profile,
      name: payload.name,
      avatar_url: payload.avatar_url,
      bot_qq: payload.bot_qq,
      owner_id: payload.owner_id
    };
  });
}

function applyBotConfig(config: QQBotConfig) {
  botProfiles.value = config.profiles || [];
  botActiveProfileID.value = config.active_profile_id || config.id || botProfiles.value[0]?.id || "";
  const active = botProfiles.value.find((profile) => profile.id === botActiveProfileID.value) || config;
  loadBotFormProfile(active);
}

async function refreshBotConfig() {
  setStatus("同步机器人配置");
  const [config, statusPayload] = await Promise.all([getQQBotConfig(), getQQBotStatus().catch(() => botStatus.value)]);
  applyBotConfig(config);
  if (statusPayload) {
    botStatus.value = statusPayload;
    plugins.value = statusPayload.plugins || plugins.value;
  }
  const changed = await refreshQQBotAutoInfo({ save: true, quiet: true });
  setStatus(changed ? "已同步并补全 QQ 信息" : "机器人配置已同步", "ok");
}

function resetBotChanges() {
  if (!lastSavedBotPayload.value) {
    return;
  }
  loadBotFormProfile(lastSavedBotPayload.value);
  setStatus("机器人改动已撤销", "ok");
}

function closeBotDetail() {
  if (!confirmDiscardChanges("机器人")) {
    return;
  }
  if (lastSavedBotPayload.value) {
    loadBotFormProfile(lastSavedBotPayload.value);
  }
  botDetailOpen.value = false;
}

function botPayload(): QQBotConfig {
  return {
    id: botForm.id || undefined,
    name: botForm.name,
    platform: botForm.platform,
    avatar_url: botForm.avatarURL || undefined,
    enabled: botForm.enabled,
    onebot_reverse_ws_endpoint: botForm.oneBotEndpoint,
    onebot_access_token: botForm.oneBotToken || undefined,
    nonebot_bridge_enabled: botForm.noneBotBridgeEnabled,
    nonebot_bridge_endpoint: botForm.noneBotBridgeEndpoint,
    nonebot_bridge_token: botForm.noneBotBridgeToken || undefined,
    bot_qq: botForm.botQQ,
    owner_id: botForm.ownerID,
    group_triggers: botForm.groupTriggers
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean),
    disabled_groups: botForm.disabledGroups
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean),
    disabled_users: botForm.disabledUsers
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean),
    welcome_enabled: botForm.welcomeEnabled,
    welcome_message: botForm.welcomeMessage,
    system_prompt: botForm.systemPrompt,
    passive_reply_router_prompt: botForm.passiveReplyRouterPrompt,
    passive_reply_prompt: botForm.passiveReplyPrompt,
    max_input_chars: botForm.maxInputChars || 0,
    max_reply_chars: botForm.maxReplyChars || 0,
    direct_reply_chunk_size: botForm.directReplyChunkSize || 0,
    forward_reply_threshold: botForm.forwardReplyThreshold || 0,
    recall_reply_mode: botForm.recallReplyMode,
    recall_reply_auto_delete_enabled: botForm.recallReplyAutoDeleteEnabled,
    llm_qq_id_masking_enabled: botForm.llmQQIDMaskingEnabled,
    recent_context_limit: botForm.recentContextLimit || 0,
    context_summary_threshold: botForm.contextSummaryThreshold || 0,
    passive_reply_chance: botForm.passiveReplyChance || 0,
    passive_reply_threshold: botForm.passiveReplyThreshold || 0,
    reply_rules: botForm.replyRules
      .map((rule) => ({
        id: rule.id,
        name: rule.name.trim() || "回复规则",
        enabled: rule.enabled,
        prompt: rule.prompt.trim(),
        action: rule.action,
        llm_profile_id: rule.llmProfileID || undefined
      }))
      .filter((rule) => rule.prompt),
    max_bot_concurrency: botForm.maxBotConcurrency || 0,
    request_timeout_ms: botForm.requestTimeoutMS || 0,
    agent_enabled: botForm.agentEnabled,
    agent_work_dir: botForm.agentWorkDir || ".",
    agent_max_steps: botForm.agentMaxSteps || 0,
    agent_skill_roots: splitCommaList(botForm.agentSkillRoots),
    agent_mcp_config_path: botForm.agentMCPConfigPath || undefined,
    agent_command_allowlist: splitCommaList(botForm.agentCommandAllowlist),
    agent_command_timeout_ms: botForm.agentCommandTimeoutMS || 0,
    agent_browser_cdp_url: botForm.agentBrowserCDPURL || undefined,
    agent_browser_timeout_ms: botForm.agentBrowserTimeoutMS || 0
  };
}

function createBotProfile() {
  if (!confirmDiscardChanges("机器人")) {
    return;
  }
  loadBotFormProfile({
    id: "",
    name: `机器人 ${botProfiles.value.length + 1}`,
    platform: "NapCat / OneBot V11",
    avatar_url: "",
    enabled: false,
    onebot_reverse_ws_endpoint: "ws://127.0.0.1:18080/onebot/v11/ws",
    nonebot_bridge_enabled: false,
    nonebot_bridge_endpoint: "ws://127.0.0.1:8080/onebot/v11/ws",
    bot_qq: "",
    owner_id: "",
    group_triggers: ["嘉然", "然然", "Diana", "diana"],
    disabled_groups: [],
    welcome_enabled: false,
    welcome_message: "欢迎加入本群，直接 @我 或发送触发词就可以开始聊天。",
    system_prompt: "",
    passive_reply_router_prompt: "",
    passive_reply_prompt: "",
    max_input_chars: 2000,
    max_reply_chars: 3500,
    direct_reply_chunk_size: 900,
    forward_reply_threshold: 900,
    recall_reply_mode: "llm_summary",
    recent_context_limit: 20,
    context_summary_threshold: 100,
    passive_reply_chance: 1,
    passive_reply_threshold: 0.8,
    max_bot_concurrency: 8,
    request_timeout_ms: 180000,
    agent_enabled: true,
    agent_work_dir: ".",
    agent_max_steps: 4,
    agent_skill_roots: [],
    agent_mcp_config_path: undefined,
    agent_command_allowlist: [],
    agent_command_timeout_ms: 10000,
    agent_browser_cdp_url: "http://127.0.0.1:9222",
    agent_browser_timeout_ms: 15000
  });
  lastSavedBotPayload.value = null;
  botDetailTab.value = "config";
  botDetailOpen.value = true;
}

function applyGroupAdminResponse(response: QQBotGroupAdminConfigResponse) {
  const config = normalizeGroupAdminConfig(response.config);
  groupAdmin.token = response.token || groupAdmin.token;
  groupAdmin.expiresAt = response.expires_at || groupAdmin.expiresAt;
  groupAdmin.form.groupID = response.group_id || config.group_id || groupAdmin.form.groupID;
  groupAdmin.form.userID = response.user_id || groupAdmin.form.userID;
  groupAdmin.form.code = "";
  groupAdmin.form.triggers = (config.group_triggers || []).join(",");
  groupAdmin.plugins = response.plugins || [];
  groupAdmin.config = config;
}

function normalizeGroupAdminConfig(config?: QQBotGroupConfig): QQBotGroupConfig {
  const current: Partial<QQBotGroupConfig> = config || {};
  return {
    group_id: current.group_id || groupAdmin.form.groupID,
    enabled: current.enabled !== false,
    enabled_set: true,
    group_triggers: current.group_triggers?.length ? current.group_triggers : ["嘉然", "然然", "Diana", "diana"],
    welcome_enabled: Boolean(current.welcome_enabled),
    welcome_message: current.welcome_message || "欢迎加入本群，直接 @我 或发送触发词就可以开始聊天。",
    recent_context_limit: current.recent_context_limit || 20,
    max_reply_chars: current.max_reply_chars || 3500,
    passive_reply_chance: Math.min(1, Math.max(0.05, Number(current.passive_reply_chance) || 1)),
    passive_reply_threshold: Math.min(1, Math.max(0.5, Number(current.passive_reply_threshold) || 0.8)),
    minimum_reply_member_level: Math.min(1000, Math.max(0, Math.trunc(Number(current.minimum_reply_member_level) || 0))),
    plugin_overrides: { ...(current.plugin_overrides || {}) },
    updated_at: current.updated_at
  };
}

function splitCommaList(value: string): string[] {
  const seen = new Set<string>();
  return value
    .split(",")
    .map((item) => item.trim())
    .filter((item) => {
      if (!item || seen.has(item)) {
        return false;
      }
      seen.add(item);
      return true;
    });
}

function groupAdminPayload(): QQBotGroupConfig {
  return {
    ...groupAdmin.config,
    group_id: groupAdmin.config.group_id || groupAdmin.form.groupID,
    enabled_set: true,
    group_triggers: splitCommaList(groupAdmin.form.triggers),
    recent_context_limit: Number(groupAdmin.config.recent_context_limit) || 20,
    max_reply_chars: Number(groupAdmin.config.max_reply_chars) || 3500,
    passive_reply_chance: Math.min(1, Math.max(0.05, Number(groupAdmin.config.passive_reply_chance) || 1)),
    passive_reply_threshold: Math.min(1, Math.max(0.5, Number(groupAdmin.config.passive_reply_threshold) || 0.8)),
    minimum_reply_member_level: Math.min(1000, Math.max(0, Math.trunc(Number(groupAdmin.config.minimum_reply_member_level) || 0))),
    plugin_overrides: { ...(groupAdmin.config.plugin_overrides || {}) }
  };
}

function groupAdminPluginEnabled(plugin: PluginState): boolean {
  const overrides = groupAdmin.config.plugin_overrides || {};
  if (Object.prototype.hasOwnProperty.call(overrides, plugin.manifest.id)) {
    return Boolean(overrides[plugin.manifest.id]);
  }
  return Boolean(plugin.installed && plugin.enabled);
}

function setGroupAdminPluginOverride(id: string, enabled: boolean) {
  groupAdmin.config.plugin_overrides = {
    ...(groupAdmin.config.plugin_overrides || {}),
    [id]: enabled
  };
}

async function onRequestGroupAdminCode() {
  groupAdmin.sendingCode = true;
  groupAdmin.error = "";
  groupAdmin.notice = "正在校验管理员身份";
  setStatus("发送验证码");
  try {
    const response = await requestQQBotGroupAdminChallenge(groupAdmin.form.groupID, groupAdmin.form.userID);
    groupAdmin.notice = response.message || "验证码已发送";
    setStatus("验证码已发送", "ok");
  } catch (error) {
    groupAdmin.error = error instanceof Error ? error.message : String(error);
    setStatus("验证失败", "bad");
  } finally {
    groupAdmin.sendingCode = false;
  }
}

async function onVerifyGroupAdmin() {
  groupAdmin.verifying = true;
  groupAdmin.error = "";
  groupAdmin.notice = "正在验证";
  setStatus("验证群管理员");
  try {
    const response = await verifyQQBotGroupAdmin(groupAdmin.form.groupID, groupAdmin.form.userID, groupAdmin.form.code);
    applyGroupAdminResponse(response);
    groupAdmin.notice = "已载入本群配置";
    setStatus("群管理已验证", "ok");
  } catch (error) {
    groupAdmin.error = error instanceof Error ? error.message : String(error);
    setStatus("验证失败", "bad");
  } finally {
    groupAdmin.verifying = false;
  }
}

async function onRefreshGroupAdminConfig() {
  if (!groupAdmin.token) {
    groupAdmin.error = "请先完成群管理员验证";
    return;
  }
  groupAdmin.loading = true;
  groupAdmin.error = "";
  setStatus("刷新群配置");
  try {
    const response = await getQQBotGroupAdminConfig(groupAdmin.token);
    applyGroupAdminResponse(response);
    groupAdmin.notice = "群配置已刷新";
    setStatus("群配置已刷新", "ok");
  } catch (error) {
    groupAdmin.error = error instanceof Error ? error.message : String(error);
    setStatus("刷新失败", "bad");
  } finally {
    groupAdmin.loading = false;
  }
}

async function onSaveGroupAdminConfig() {
  if (!groupAdmin.token) {
    groupAdmin.error = "请先完成群管理员验证";
    return;
  }
  groupAdmin.saving = true;
  groupAdmin.error = "";
  setStatus("保存群配置");
  try {
    const response = await saveQQBotGroupAdminConfig(groupAdmin.token, groupAdminPayload());
    applyGroupAdminResponse(response);
    groupAdmin.notice = "本群配置已保存";
    setStatus("群配置已保存", "ok");
  } catch (error) {
    groupAdmin.error = error instanceof Error ? error.message : String(error);
    setStatus("保存失败", "bad");
  } finally {
    groupAdmin.saving = false;
  }
}

function editBotProfile(profile: QQBotConfig) {
  if (!confirmDiscardChanges("机器人")) {
    return;
  }
  loadBotFormProfile(profile);
  botDetailOpen.value = true;
}

function applyAdminAccessSettings(settings: AdminAccessSettings) {
  Object.assign(adminAccessSettings, settings);
  adminAccountForm.email = settings.username || "";
}

async function refreshAdminSessions(showStatus = true) {
  loadingAdminSessions.value = true;
  if (showStatus) setStatus("读取登录设备");
  try {
    const response = await listAdminSessions();
    adminSessions.value = response.sessions || [];
    if (showStatus) setStatus("登录设备已更新", "ok");
  } catch (error) {
    if (showStatus) setStatus("登录设备读取失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    loadingAdminSessions.value = false;
  }
}

async function onUpdateAdminEmail() {
  if (savingAdminAccount.value || !adminAccountForm.email || !adminAccountForm.currentPassword) return;
  savingAdminAccount.value = true;
  setStatus("保存管理员邮箱");
  try {
    const result = await updateAdminEmail(adminAccountForm.email, adminAccountForm.currentPassword);
    adminAccessSettings.username = result.email;
    adminAccountForm.email = result.email;
    adminAccountForm.currentPassword = "";
    await refreshAdminSessions(false);
    setStatus("管理员邮箱已更新", "ok");
  } catch (error) {
    setStatus("管理员邮箱保存失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    savingAdminAccount.value = false;
  }
}

async function onChangeAdminPassword() {
  if (changingAdminPassword.value || !adminPasswordReady.value) return;
  changingAdminPassword.value = true;
  setStatus("修改管理员密码");
  try {
    await changeAdminPassword(adminPasswordForm.currentPassword, adminPasswordForm.newPassword, adminPasswordForm.passwordConfirm);
    adminPasswordForm.currentPassword = "";
    adminPasswordForm.newPassword = "";
    adminPasswordForm.passwordConfirm = "";
    await refreshAdminSessions(false);
    setStatus("密码已修改，其他设备已退出", "ok");
  } catch (error) {
    setStatus("密码修改失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    changingAdminPassword.value = false;
  }
}

async function onRevokeAdminSession(session: AdminAuthSession) {
  if (revokingAdminSession.value) return;
  if (!window.confirm(session.current ? "退出当前设备？" : `吊销 ${session.device_name || "此设备"} 的登录？`)) return;
  revokingAdminSession.value = session.id;
  try {
    const result = await revokeAdminSession(session.id);
    if (result.current) {
      rememberAdminLoginPath(adminAccessSettings.login_path || "/login");
      window.location.replace(adminAccessSettings.login_path || "/login");
      return;
    }
    await refreshAdminSessions(false);
    setStatus("设备登录已吊销", "ok");
  } catch (error) {
    setStatus("设备吊销失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    revokingAdminSession.value = "";
  }
}

async function onRevokeOtherAdminSessions() {
  if (revokingAdminSession.value || adminSessions.value.length <= 1) return;
  if (!window.confirm("退出除当前设备外的所有登录？")) return;
  revokingAdminSession.value = "others";
  try {
    const result = await revokeOtherAdminSessions();
    await refreshAdminSessions(false);
    setStatus(`已退出 ${result.revoked} 个设备`, "ok");
  } catch (error) {
    setStatus("其他设备退出失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    revokingAdminSession.value = "";
  }
}

function formatAdminSessionTime(value: string): string {
  return formatLogTableTime(value);
}

async function onSaveAdminAccess(regenerate = false) {
  if (savingAdminAccess.value || adminAccessSettings.managed_by_environment) {
    return;
  }
  savingAdminAccess.value = true;
  setStatus(regenerate ? "生成登录入口" : "保存访问设置");
  try {
    const saved = await saveAdminAccessSettings(adminAccessSettings.random_suffix_enabled, regenerate);
    applyAdminAccessSettings(saved);
    rememberAdminLoginPath(saved.login_path || "/");
    setStatus(regenerate ? "登录入口已更新" : "访问设置已保存", "ok");
  } catch (error) {
    setStatus("访问设置保存失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    savingAdminAccess.value = false;
  }
}

async function copyAdminLoginURL() {
  try {
    await navigator.clipboard.writeText(adminLoginURL.value);
    setStatus("登录入口已复制", "ok");
  } catch (error) {
    setStatus("复制失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function onLogoutAdmin() {
  const loginPath = adminAccessSettings.login_path || "/";
  try {
    rememberAdminLoginPath(loginPath);
    await logoutAdmin();
  } catch {
    // 退出失败时仍回到登录入口，让下一次请求重新校验会话。
  } finally {
    window.location.replace(loginPath);
  }
}

async function load() {
  try {
    const [llmConfig, botConfig, statusPayload, pluginPayload, featurePayload, webSearchPayload, adminAccessPayload, adminSessionsPayload, logsPayload, taskPayload, statsPayload, autoInfoPayload] = await Promise.all([
      getConfig(true),
      getQQBotConfig(),
      getQQBotStatus(),
      listPlugins(),
      getQQBotFeatures().catch(() => ({ group_test: false })),
      getWebSearchConfig(),
      getAdminAccessSettings(),
      listAdminSessions().catch(() => ({ sessions: [] })),
      listAppLogs(undefined, 10).catch(() => ({ logs: [] })),
      listQQBotTasks().catch(() => ({ items: [] })),
      getQQBotDashboardStats().catch(() => null),
      getQQBotAutoInfo().catch(() => null)
    ]);
    applyLLMConfig(llmConfig);
    applyBotConfig(botConfig);
    Object.assign(botFeatures, featurePayload);
    botStatus.value = statusPayload;
    const recentGroupID = statusPayload.recent_events?.find((event) => event.group_id)?.group_id;
    if (!botGroupTest.groupID && recentGroupID) {
      botGroupTest.groupID = recentGroupID;
    }
    plugins.value = pluginPayload;
    appLogs.value = sortAppLogs(logsPayload.logs || []);
    qqbotTasks.value = taskPayload.items || [];
    qqbotDashboardStats.value = statsPayload;
    botAutoInfo.value = autoInfoPayload;
    const autoChanged = applyQQAutoInfo(autoInfoPayload, { persistCandidateOwner: true });
    if (autoChanged) {
      await saveQQBotConfig(botPayload()).then(applyBotConfig).catch(() => undefined);
    }
    applyWebSearchConfig(webSearchPayload);
    applyAdminAccessSettings(adminAccessPayload);
    adminSessions.value = adminSessionsPayload.sessions || [];
    void refreshUpdateStatus(false);
    if (activeTab.value === "logs") {
      void refreshLogs(false);
    }
    setStatus("已加载", "ok");
  } catch (error) {
    setStatus("读取失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function refreshUpdateStatus(showStatus = true, refreshRemote = showStatus) {
  loadingUpdateStatus.value = true;
  if (showStatus) {
    setStatus("读取更新状态");
  }
  try {
    updateStatus.value = await getUpdateStatus(refreshRemote);
    updateOutput.value = updateStatus.value.last_update_text || "仓库状态已刷新。";
    if (showStatus) {
      updateHistory.value = [
        ...updateHistory.value.slice(-9),
        {
          id: `u-${Date.now()}`,
          title: "刷新仓库状态",
          output: updateOutput.value,
          at: new Date().toLocaleTimeString(),
          ok: true
        }
      ];
    }
    if (showStatus) {
      setStatus("更新状态已刷新", "ok");
    }
  } catch (error) {
    if (showStatus) {
      setStatus("更新状态失败", "bad");
    }
    updateOutput.value = error instanceof Error ? error.message : String(error);
    if (showStatus) {
      updateHistory.value = [
        ...updateHistory.value.slice(-9),
        {
          id: `ue-${Date.now()}`,
          title: "刷新仓库状态失败",
          output: updateOutput.value,
          at: new Date().toLocaleTimeString(),
          ok: false
        }
      ];
    }
  } finally {
    loadingUpdateStatus.value = false;
  }
}

async function performSystemUpdate() {
  if (systemUpgradeDisabled.value) return;
  updatingSystem.value = true;
  setStatus("正在升级");
  updateOutput.value = "正在拉取源码并构建应用，这可能需要几分钟。";
  try {
    const result = await pullFromGitHub();
    updateStatus.value = result.status;
    updateOutput.value = result.output || (result.updated ? "升级已完成。" : "当前已是最新版本。");
    updateHistory.value = [
      ...updateHistory.value.slice(-9),
      {
        id: `ua-${Date.now()}`,
        title: result.applied ? "升级并安装" : "检查并更新源码",
        output: updateOutput.value,
        at: new Date().toLocaleTimeString(),
        ok: true
      }
    ];
    setStatus(result.restart_required ? "升级完成，请重启" : result.updated ? "源码已更新" : "已是最新", "ok");
  } catch (error) {
    updateOutput.value = error instanceof Error ? error.message : String(error);
    updateHistory.value = [
      ...updateHistory.value.slice(-9),
      {
        id: `uerr-${Date.now()}`,
        title: "系统升级失败",
        output: updateOutput.value,
        at: new Date().toLocaleTimeString(),
        ok: false
      }
    ];
    setStatus("升级失败", "bad");
    await refreshUpdateStatus(false, false);
  } finally {
    updatingSystem.value = false;
  }
}

async function refreshDashboard() {
  loadingLogs.value = true;
  setStatus("刷新仪表盘");
  try {
    const [statusPayload, pluginPayload, logsPayload, updatePayload, taskPayload, statsPayload] = await Promise.all([
      getQQBotStatus(),
      listPlugins(),
      listAppLogs(undefined, 10),
      getUpdateStatus().catch(() => updateStatus.value),
      listQQBotTasks().catch(() => ({ items: qqbotTasks.value })),
      getQQBotDashboardStats().catch(() => qqbotDashboardStats.value)
    ]);
    botStatus.value = statusPayload;
    plugins.value = pluginPayload;
    appLogs.value = sortAppLogs(logsPayload.logs || []);
    qqbotTasks.value = taskPayload.items || [];
    qqbotDashboardStats.value = statsPayload;
    if (updatePayload) {
      updateStatus.value = updatePayload;
      updateOutput.value = updatePayload.last_update_text || updateOutput.value;
    }
    setStatus("仪表盘已刷新", "ok");
  } catch (error) {
    setStatus("仪表盘刷新失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    loadingLogs.value = false;
  }
}

async function refreshLogs(showStatus = true) {
  loadingLogs.value = true;
  if (showStatus) {
    setStatus("读取日志");
  }
  try {
    const result = await listAppLogs(undefined, 500);
    appLogs.value = sortAppLogs(result.logs || []);
    if (showStatus) {
      setStatus(`日志 ${appLogs.value.length} 条`, "ok");
    }
  } catch (error) {
    if (showStatus) {
      setStatus("日志失败", "bad");
    }
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    loadingLogs.value = false;
  }
}

function selectLogView(view: LogViewFilter) {
  logView.value = view;
  if (view !== "all") {
    logKind.value = view;
  }
  setLogPage(1);
}

async function selectLogKind(kind: AppLogKind) {
  selectLogView(kind);
}

async function onRefreshLogs() {
  await refreshLogs(true);
}

function sortAppLogs(logs: AppLogEntry[]): AppLogEntry[] {
  return [...logs].sort((left, right) => {
    const leftTime = new Date(left.created_at).getTime();
    const rightTime = new Date(right.created_at).getTime();
    return (Number.isNaN(rightTime) ? 0 : rightTime) - (Number.isNaN(leftTime) ? 0 : leftTime);
  });
}

function setLogPage(page: number) {
  const next = Math.trunc(page);
  if (!Number.isFinite(next)) {
    logPage.value = 1;
    return;
  }
  logPage.value = Math.min(Math.max(next, 1), logPageCount.value);
}

function onLogPageSizeChange() {
  setLogPage(1);
}

function onLogJumpChange(event: Event) {
  const input = event.target as HTMLInputElement;
  setLogPage(Number.parseInt(input.value, 10));
  input.value = String(logPage.value);
}

function formatLogTime(value: string): string {
  return formatLogTableTime(value);
}

function formatDashboardTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  const pad = (input: number) => String(input).padStart(2, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function formatStatNumber(value: number): string {
  return new Intl.NumberFormat("zh-CN").format(Math.max(0, Math.round(Number(value) || 0)));
}

function formatCompactNumber(value: number): string {
  const numeric = Math.max(0, Math.round(Number(value) || 0));
  if (numeric >= 10000) {
    return `${(numeric / 10000).toFixed(numeric >= 100000 ? 0 : 1)}万`;
  }
  return formatStatNumber(numeric);
}

function formatBytes(value: number): string {
  const numeric = Math.max(0, Number(value) || 0);
  if (numeric <= 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let unitIndex = 0;
  let size = numeric;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  const digits = unitIndex >= 3 ? 2 : unitIndex === 0 ? 0 : 1;
  return `${size.toFixed(digits)} ${units[unitIndex]}`;
}

function formatPercent(value: number): string {
  const numeric = clampDashboardPercent(value);
  return `${numeric.toFixed(numeric >= 10 ? 0 : 1)}%`;
}

function clampDashboardPercent(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.max(0, Math.min(100, Number(value)));
}

function formatDurationShort(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  if (days > 0) return `${days}天 ${hours}小时`;
  if (hours > 0) return `${hours}小时 ${minutes}分`;
  if (minutes > 0) return `${minutes}分`;
  return `${total}秒`;
}

function formatLogTableTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value || "-";
  }
  const pad = (input: number) => String(input).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function formatLogMetadata(metadata?: Record<string, unknown>): string {
  if (!metadata || Object.keys(metadata).length === 0) {
    return "";
  }
  return Object.entries(metadata)
    .map(([key, value]) => `${key}: ${stringifyLogValue(value)}`)
    .join(" · ");
}

function parseLogDateBoundary(value: string, side: "start" | "end"): number | null {
  if (!value) {
    return null;
  }
  const parsed = new Date(`${value}T${side === "start" ? "00:00:00" : "23:59:59"}`).getTime();
  return Number.isNaN(parsed) ? null : parsed;
}

function stringifyLogValue(value: unknown): string {
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function logSearchText(entry: AppLogEntry): string {
  return [
    entry.action,
    entry.message,
    entry.detail,
    entry.actor,
    entry.target,
    entry.kind,
    entry.level,
    formatLogMetadata(entry.metadata)
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function logLevelValue(entry: AppLogEntry): Exclude<LogLevelFilter, "all"> {
  if (entry.kind === "error" || entry.level === "error") {
    return "error";
  }
  const haystack = `${entry.action} ${entry.message} ${entry.detail || ""}`.toLowerCase();
  if (/(warn|warning|警告|失败|缺少|超时|timeout|不可用|异常)/i.test(haystack)) {
    return "warn";
  }
  return "info";
}

function logLevelLabel(entry: AppLogEntry): string {
  const level = logLevelValue(entry);
  if (level === "error") {
    return "错误";
  }
  if (level === "warn") {
    return "警告";
  }
  return "信息";
}

function logLevelClass(entry: AppLogEntry): string {
  return logLevelValue(entry);
}

function logModuleLabel(entry: AppLogEntry): string {
  return entry.action || entry.target || "-";
}

function logContent(entry: AppLogEntry): string {
  return entry.message || entry.detail || "-";
}

function logSourceLabel(entry: AppLogEntry): string {
  return entry.actor || "web:unknown";
}

function firstLogMetadataValue(metadata: Record<string, unknown> | undefined, keys: string[]): string {
  if (!metadata) {
    return "";
  }
  for (const key of keys) {
    const value = stringifyLogValue(metadata[key]).trim();
    if (value) {
      return value;
    }
  }
  return "";
}

function compactLogChip(value: string): string {
  return value.length > 38 ? `${value.slice(0, 35)}...` : value;
}

function logConfigChips(entry: AppLogEntry): string[] {
  const metadata = entry.metadata;
  const chips = [
    firstLogMetadataValue(metadata, ["model", "new_model", "old_model"]),
    firstLogMetadataValue(metadata, ["provider", "new_provider", "old_provider"]),
    firstLogMetadataValue(metadata, ["profile_name", "profile_id"]),
    firstLogMetadataValue(metadata, ["base_url"])
  ]
    .map(compactLogChip)
    .filter(Boolean);
  const unique = Array.from(new Set(chips));
  return unique.length > 0 ? unique.slice(0, 3) : ["-", "-"];
}

function setBotPlatformFilter(filter: BotPlatformFilter) {
  botPlatformFilter.value = filter;
  setBotPage(1);
}

function setBotPage(page: number) {
  const next = Math.trunc(page);
  if (!Number.isFinite(next)) {
    botPage.value = 1;
    return;
  }
  botPage.value = Math.min(Math.max(next, 1), botPageCount.value);
}

function botUsesCurrentForm(profile?: QQBotConfig | null): boolean {
  if (!profile) {
    return true;
  }
  return Boolean(profile.id && botForm.id && profile.id === botForm.id);
}

function botCredentialLabel(profile?: QQBotConfig | null): string {
  const configured = botUsesCurrentForm(profile)
    ? botForm.oneBotTokenConfigured || Boolean(botForm.oneBotToken.trim())
    : Boolean(profile?.onebot_access_token_configured);
  return configured ? "有效" : "无效";
}

function botCredentialTone(profile?: QQBotConfig | null): string {
  return botCredentialLabel(profile) === "有效" ? "ok" : "bad";
}

function botCallbackLabel(profile?: QQBotConfig | null): string {
  const endpoint = botUsesCurrentForm(profile) ? botForm.oneBotEndpoint : profile?.onebot_reverse_ws_endpoint;
  if (!endpoint?.trim()) {
    return "未配置";
  }
  if (profile?.id && profile.id === botActiveProfileID.value && botStatus.value?.channel.connected) {
    return "已连接";
  }
  return "已配置";
}

function botCallbackTone(profile?: QQBotConfig | null): string {
  return botCallbackLabel(profile) === "未配置" ? "warn" : "ok";
}

function botRuntimeLabel(profile?: QQBotConfig | null): string {
  const enabled = botUsesCurrentForm(profile) ? botForm.enabled : Boolean(profile?.enabled);
  if (!enabled) {
    return "已停止";
  }
  if (profile?.id && profile.id === botActiveProfileID.value) {
    return botStatus.value?.running ? "运行中" : "待启动";
  }
  return "待启动";
}

function botRuntimeTone(profile?: QQBotConfig | null): string {
  const label = botRuntimeLabel(profile);
  if (label === "运行中") return "ok";
  if (label === "待启动") return "warn";
  return "muted";
}

async function onTestBotConnection(profile?: QQBotConfig | null) {
  if (profile?.id && profile.id !== botForm.id) {
    editBotProfile(profile);
  }
  setStatus("测试机器人连接");
  try {
    await refreshBotStatus();
    const activeConnected = Boolean(profile?.id && profile.id === botActiveProfileID.value && botStatus.value?.channel.connected);
    if (activeConnected || botCallbackLabel(profile) !== "未配置") {
      setStatus(activeConnected ? "机器人已连接" : "配置已就绪", "ok");
      return;
    }
    setStatus("连接未配置", "bad");
  } catch (error) {
    setStatus("测试失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function onToggleBotProfile(profile: QQBotConfig, event: Event) {
  const input = event.target as HTMLInputElement;
  if (!confirmDiscardChanges("机器人")) {
    input.checked = Boolean(profile.enabled);
    return;
  }
  loadBotFormProfile({ ...profile, enabled: input.checked });
  await onSaveBot();
}

async function refreshBotStatus() {
  botStatus.value = await getQQBotStatus();
  plugins.value = botStatus.value.plugins;
  const recentGroupID = botStatus.value.recent_events?.find((event) => event.group_id)?.group_id;
  if (!botGroupTest.groupID && recentGroupID) {
    botGroupTest.groupID = recentGroupID;
  }
}

async function onRefreshGroupTest() {
  const groupID = botGroupTest.groupID.trim();
  if (!groupID) {
    setStatus("请填写群号", "bad");
    return;
  }
  refreshingGroupTest.value = true;
  botGroupTestError.value = "";
  setStatus("刷新群记录");
  try {
    botGroupTestResult.value = await getQQGroupTest(groupID);
    botStatus.value = botGroupTestResult.value.status;
    plugins.value = botStatus.value.plugins;
    setStatus(`群记录 ${groupTestEvents.value.length}`, "ok");
  } catch (error) {
    setStatus("刷新失败", "bad");
    botGroupTestError.value = error instanceof Error ? error.message : String(error);
    output.value = botGroupTestError.value;
  } finally {
    refreshingGroupTest.value = false;
  }
}

async function onSendGroupTest() {
  const groupID = botGroupTest.groupID.trim();
  const text = botGroupTest.message.trim();
  if (!groupID || !text) {
    setStatus("请填写群号和消息", "bad");
    return;
  }
  sendingGroupTest.value = true;
  botGroupTestError.value = "";
  setStatus("发送群消息");
  try {
    botGroupTestResult.value = await sendQQGroupTest(groupID, text);
    botStatus.value = botGroupTestResult.value.status;
    plugins.value = botStatus.value.plugins;
    setStatus("群消息已发送", "ok");
  } catch (error) {
    setStatus("发送失败", "bad");
    botGroupTestError.value = error instanceof Error ? error.message : String(error);
    output.value = botGroupTestError.value;
  } finally {
    sendingGroupTest.value = false;
  }
}

async function onSelectBotProfile(id: string) {
  if (!id || !confirmDiscardChanges("机器人")) {
    return;
  }
  try {
    applyBotConfig(await activateQQBotProfile(id));
    await refreshBotStatus();
    setStatus("机器人配置已切换", "ok");
  } catch (error) {
    setStatus("切换失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function onCloneBotProfile(profile?: QQBotConfig) {
  const id = profile?.id || botForm.id;
  if (!id) {
    return;
  }
  try {
    applyBotConfig(await cloneQQBotProfile(id));
    await refreshBotStatus();
    setStatus("机器人配置已复制", "ok");
  } catch (error) {
    setStatus("复制失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function onDeleteBotProfile(profile?: QQBotConfig) {
  const target = profile || botProfiles.value.find((item) => item.id === botForm.id);
  if (!target?.id || botProfiles.value.length <= 1) {
    return;
  }
  if (!window.confirm(`确定删除机器人配置「${target.name || "未命名机器人"}」吗？`)) {
    return;
  }
  try {
    applyBotConfig(await deleteQQBotProfile(target.id));
    await refreshBotStatus();
    setStatus("机器人配置已删除", "ok");
  } catch (error) {
    setStatus("删除失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function onSaveLLM() {
  savingLLM.value = true;
  setStatus("保存中");
  try {
    await saveConfig(llmPayload());
    await refreshLLMConfig();
    llmEditorMode.value = "list";
    showLLMAPIKey.value = false;
    setStatus("LLM 已保存", "ok");
  } catch (error) {
    setStatus("保存失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    savingLLM.value = false;
  }
}

async function onDeleteProfile(profile?: LLMConfig) {
  closeLLMMoreMenu();
  const target = profile || llmProfiles.value.find((item) => item.id === llmForm.id);
  if (!target?.id || llmProfiles.value.length <= 1) {
    return;
  }
  if (!window.confirm(`确定删除配置「${target.name || "未命名配置"}」吗？`)) {
    return;
  }
  try {
    await deleteConfigProfile(target.id);
    await refreshLLMConfig();
    llmEditorMode.value = "list";
    setStatus("LLM 配置已删除", "ok");
  } catch (error) {
    setStatus("删除失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function onCloneProfile(profile?: LLMConfig) {
  closeLLMMoreMenu();
  const id = profile?.id || llmForm.id;
  if (!id) {
    return;
  }
  try {
    await cloneConfigProfile(id);
    await refreshLLMConfig();
    llmEditorMode.value = "list";
    setStatus("LLM 配置已复制", "ok");
  } catch (error) {
    setStatus("复制失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function onExportProfiles() {
  closeLLMMoreMenu();
  try {
    const exported = await exportConfig();
    const blob = new Blob(
      [
        JSON.stringify(
          {
            active_profile_id: exported.active_profile_id,
            profiles: exported.profiles || []
          },
          null,
          2
        )
      ],
      { type: "application/json" }
    );
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `llm-profiles-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-")}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
    setStatus("LLM 配置已导出", "ok");
  } catch (error) {
    setStatus("导出失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

async function onExportProfile(profile: LLMConfig) {
  closeLLMMoreMenu();
  if (!profile.id) {
    return;
  }
  try {
    const exported = await exportConfig();
    const target = (exported.profiles || []).find((item) => item.id === profile.id);
    if (!target) {
      throw new Error("profile not found in export payload");
    }
    const blob = new Blob(
      [
        JSON.stringify(
          {
            active_profile_id: profile.id,
            profiles: [target]
          },
          null,
          2
        )
      ],
      { type: "application/json" }
    );
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    const name = (profile.name || "llm-profile").trim().replace(/[^\w\u4e00-\u9fa5.-]+/g, "-");
    anchor.href = url;
    anchor.download = `${name || "llm-profile"}-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-")}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
    setStatus("LLM 配置已导出", "ok");
  } catch (error) {
    setStatus("导出失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

function openImportProfiles() {
  llmImportFileRef.value?.click();
}

async function onImportProfilesFileChange(event: Event) {
  const input = event.target as HTMLInputElement | null;
  const file = input?.files?.[0];
  if (!file) {
    return;
  }
  try {
    const raw = await file.text();
    llmImportText.value = raw;
    const parsed = JSON.parse(raw) as Pick<LLMConfig, "active_profile_id" | "profiles">;
    await importConfigProfiles(parsed);
    await refreshLLMConfig();
    llmEditorMode.value = "list";
    setStatus("LLM 配置已导入", "ok");
  } catch (error) {
    setStatus("导入失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    if (input) {
      input.value = "";
    }
  }
}

async function onTestProfile(profile: LLMConfig) {
  closeLLMMoreMenu();
  testingProfileID.value = profile.id || profile.name || "profile";
  output.value = "发送中";
  setStatus("测试中");
  const prompt = message.value || "你好，用一句话回复当前模型已连通。";
  const sessionID = selectedTestSessionID.value;
  const now = new Date().toLocaleTimeString();
  const start = performance.now();
  const userItem: TestHistoryItem = { id: `u-${Date.now()}`, role: "user", text: `测试配置「${profile.name || "未命名配置"}」：${prompt}`, at: now, ok: true };
  const historyWithUser = [...activeTestHistory.value.slice(-18), userItem];
  patchTestSession(sessionID, { history: historyWithUser, output: "发送中", latencyMS: null });
  try {
    const result = await testLLM(prompt, profile);
    const latencyMS = Math.max(1, Math.round(performance.now() - start));
    output.value = result.text || JSON.stringify(result, null, 2);
    patchTestSession(sessionID, {
      history: [...historyWithUser, { id: `a-${Date.now()}`, role: "assistant", text: output.value, at: new Date().toLocaleTimeString(), ok: true, latencyMS }],
      output: output.value,
      latencyMS
    });
    updateTestSession(sessionID, prompt, true);
    showLLMTestResult({
      ok: true,
      title: "连接成功",
      message: `与 ${providerDisplayLabel(profile.provider)} (${result.model || profile.model || "-"}) 的连接测试通过。`,
      latencyMS,
      statusCode: 200,
      model: result.model || profile.model || "-",
      testedAt: new Date().toLocaleString()
    });
    setStatus("连通", "ok");
  } catch (error) {
    const latencyMS = Math.max(1, Math.round(performance.now() - start));
    const messageText = error instanceof Error ? error.message : String(error);
    setStatus("测试失败", "bad");
    output.value = messageText;
    patchTestSession(sessionID, {
      history: [...historyWithUser, { id: `e-${Date.now()}`, role: "error", text: output.value, at: new Date().toLocaleTimeString(), ok: false, latencyMS }],
      output: output.value,
      latencyMS
    });
    updateTestSession(sessionID, prompt, false);
    showLLMTestResult({
      ok: false,
      title: "连接失败",
      message: messageText,
      latencyMS,
      statusCode: null,
      model: profile.model || "-",
      testedAt: new Date().toLocaleString()
    });
  } finally {
    testingProfileID.value = "";
  }
}

async function onRefreshModels() {
  loadingModels.value = true;
  setStatus("读取模型");
  try {
    const result = await listLLMModels(llmPayload());
    fetchedModelOptions.value = result.models || [];
    modelMenuOpen.value = true;
    if (!llmForm.model && fetchedModelOptions.value[0]?.id) {
      llmForm.model = fetchedModelOptions.value[0].id;
    }
    const currentModel = fetchedModelOptions.value.find((option) => option.id === llmForm.model);
    if (currentModel?.context_window_tokens) {
      applyModelLimits(currentModel);
    }
    setStatus(`模型 ${fetchedModelOptions.value.length}`, "ok");
  } catch (error) {
    setStatus("模型失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    loadingModels.value = false;
  }
}

async function onSaveBot() {
  savingBot.value = true;
  setStatus("保存机器人");
  try {
    applyBotConfig(await saveQQBotConfig(botPayload()));
    await refreshBotStatus();
    setStatus("机器人已保存", "ok");
  } catch (error) {
    setStatus("保存失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    savingBot.value = false;
  }
}

async function onStartBot() {
  startingBot.value = true;
  setStatus("启动机器人");
  try {
    botStatus.value = await startQQBot();
    plugins.value = botStatus.value.plugins;
    await refreshQQBotAutoInfo({ save: true, quiet: true });
    setStatus("机器人已启动", "ok");
  } catch (error) {
    setStatus("启动失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    startingBot.value = false;
  }
}

async function onStopBot() {
  stoppingBot.value = true;
  setStatus("停止机器人");
  try {
    botStatus.value = await stopQQBot();
    plugins.value = botStatus.value.plugins;
    setStatus("机器人已停止", "ok");
  } catch (error) {
    setStatus("停止失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    stoppingBot.value = false;
  }
}

async function onInstallPlugin(id: string) {
  pluginBusy.value = id;
  try {
    await installPlugin(id);
    await refreshBotStatus();
    setStatus("插件已安装", "ok");
  } catch (error) {
    setStatus("插件失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    pluginBusy.value = "";
  }
}

async function onUninstallPlugin(id: string) {
  pluginBusy.value = id;
  try {
    await uninstallPlugin(id);
    await refreshBotStatus();
    setStatus("插件已卸载", "ok");
  } catch (error) {
    setStatus("插件失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    pluginBusy.value = "";
  }
}

async function onTogglePlugin(plugin: PluginState, enabled: boolean) {
  pluginBusy.value = plugin.manifest.id;
  try {
    await setPluginEnabled(plugin.manifest.id, enabled);
    await refreshBotStatus();
    setStatus("插件已更新", "ok");
  } catch (error) {
    setStatus("插件失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    pluginBusy.value = "";
  }
}

async function onRefreshPlugins() {
  pluginBusy.value = "__refresh__";
  setStatus("刷新插件");
  try {
    plugins.value = await listPlugins();
    setStatus("插件已刷新", "ok");
  } catch (error) {
    setStatus("插件失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  } finally {
    pluginBusy.value = "";
  }
}

async function onInstallSelectedPlugin() {
  const target = activePlugin.value && !activePlugin.value.installed
    ? activePlugin.value
    : plugins.value.find((plugin) => !plugin.installed);
  if (!target) {
    setStatus("暂无可安装插件", "ok");
    return;
  }
  selectPlugin(target);
  await onInstallPlugin(target.manifest.id);
}

function onPluginUpdate(plugin: PluginState) {
  selectPlugin(plugin);
  setStatus("插件更新入口待接入", "ok");
}

function pluginDetailPrimaryLabel(plugin: PluginState): string {
  if (!plugin.installed) return "安装插件";
  return plugin.enabled ? "禁用插件" : "启用插件";
}

async function onDetailPrimaryPluginAction(plugin: PluginState) {
  if (!plugin.installed) {
    await onInstallPlugin(plugin.manifest.id);
    return;
  }
  await onTogglePlugin(plugin, !plugin.enabled);
}

function onResetPluginSettings(plugin: PluginState) {
  selectPlugin(plugin);
  setStatus("插件设置已恢复默认", "ok");
}

function onSavePluginSettings(plugin: PluginState) {
  selectPlugin(plugin);
  setStatus("插件设置已保存", "ok");
}

function selectTestSession(id: string) {
  selectedTestSessionID.value = id;
  const session = testSessions.value.find((item) => item.id === id);
  if (!session) {
    return;
  }
  testConversationMode.value = session.kind === "group" ? "group" : "private";
  output.value = session.output;
}

function startNewTestConversation() {
  const id = `custom-${Date.now()}`;
  const session: TestSession = {
    id,
    title: testConversationMode.value === "group" ? "新的群聊测试" : "新的私聊测试",
    preview: "等待发送第一条测试消息",
    time: new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
    ok: true,
    kind: testConversationMode.value,
    icon: testSessionIcon(testConversationMode.value),
    history: [],
    output: "等待测试结果",
    latencyMS: null
  };
  testSessions.value = [session, ...testSessions.value].slice(0, 8);
  selectedTestSessionID.value = id;
  message.value = "";
  output.value = "等待测试结果";
}

function patchTestSession(id: string, patch: Partial<Pick<TestSession, "history" | "output" | "latencyMS" | "preview" | "time" | "ok">>) {
  testSessions.value = testSessions.value.map((session) => (session.id === id ? { ...session, ...patch } : session));
}

function patchActiveTestSession(patch: Partial<Pick<TestSession, "history" | "output" | "latencyMS" | "preview" | "time" | "ok">>) {
  patchTestSession(selectedTestSessionID.value, patch);
}

async function onTest() {
  const text = message.value.trim();
  if (!text) {
    setStatus("请输入测试消息", "bad");
    return;
  }
  testing.value = true;
  output.value = "发送中";
  setStatus("测试中");
  const sessionID = selectedTestSessionID.value;
  const now = new Date().toLocaleTimeString();
  const startedAt = performance.now();
  const userItem: TestHistoryItem = { id: `u-${Date.now()}`, role: "user", text, at: now, ok: true };
  const historyWithUser = [...activeTestHistory.value.slice(-18), userItem];
  patchTestSession(sessionID, { history: historyWithUser, output: "发送中", latencyMS: null });
  try {
    const result = await testLLM(text, llmPayload());
    const latencyMS = Math.max(1, Math.round(performance.now() - startedAt));
    output.value = result.text || JSON.stringify(result, null, 2);
    patchTestSession(sessionID, {
      history: [...historyWithUser, { id: `a-${Date.now()}`, role: "assistant", text: output.value, at: new Date().toLocaleTimeString(), ok: true, latencyMS }],
      output: output.value,
      latencyMS
    });
    updateTestSession(sessionID, text, true);
    setStatus("连通", "ok");
  } catch (error) {
    const latencyMS = Math.max(1, Math.round(performance.now() - startedAt));
    setStatus("测试失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
    patchTestSession(sessionID, {
      history: [...historyWithUser, { id: `e-${Date.now()}`, role: "error", text: output.value, at: new Date().toLocaleTimeString(), ok: false, latencyMS }],
      output: output.value,
      latencyMS
    });
    updateTestSession(sessionID, text, false);
  } finally {
    testing.value = false;
  }
}

function updateTestSession(id: string, text: string, ok: boolean) {
  const time = new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  testSessions.value = testSessions.value.map((session) => {
    if (session.id !== id) {
      return session;
    }
    return {
      ...session,
      preview: `你：${text}`,
      time,
      ok
    };
  });
}

async function copyLatestTestResult() {
  if (!latestTestResult.value) {
    return;
  }
  try {
    await navigator.clipboard.writeText(latestTestResult.value.text);
    setStatus("结果已复制", "ok");
  } catch (error) {
    setStatus("复制失败", "bad");
    output.value = error instanceof Error ? error.message : String(error);
  }
}

function clearTestHistory() {
  output.value = "等待测试结果";
  patchActiveTestSession({ history: [], output: output.value, latencyMS: null });
  setStatus("记录已清空", "ok");
}

function onBeforeUnload(event: BeforeUnloadEvent) {
  if (!(llmEditorMode.value === "edit" && llmDirty.value) && !botDirty.value && !webSearchDirty.value) {
    return;
  }
  event.preventDefault();
  event.returnValue = "";
}

function onGlobalKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    if (llmTestResult.open) {
      closeLLMTestResult();
      return;
    }
    if (llmMoreMenuProfileID.value) {
      closeLLMMoreMenu();
      return;
    }
    if (activeTab.value === "llm" && llmEditorMode.value === "edit") {
      closeLLMEditor();
      return;
    }
    if (updateDrawerOpen.value) {
      updateDrawerOpen.value = false;
      return;
    }
    return;
  }
  const isSave = (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s";
  if (isSave) {
    if (activeTab.value === "llm" && llmEditorMode.value === "edit" && llmDirty.value && !savingLLM.value) {
      event.preventDefault();
      void onSaveLLM();
      return;
    }
    if (activeTab.value === "qqbot" && botDirty.value && !savingBot.value) {
      event.preventDefault();
      void onSaveBot();
      return;
    }
    if (activeTab.value === "web-search" && webSearchDirty.value && !savingWebSearch.value) {
      event.preventDefault();
      void onSaveWebSearchConfig();
    }
  }
}

onMounted(() => {
  loadTheme();
  loadUIState();
  syncTabFromRoute();
  if (window.matchMedia) {
    colorSchemeQuery = window.matchMedia("(prefers-color-scheme: dark)");
    syncSystemTheme(colorSchemeQuery);
    if (typeof colorSchemeQuery.addEventListener === "function") {
      colorSchemeQuery.addEventListener("change", syncSystemTheme);
    } else if (typeof colorSchemeQuery.addListener === "function") {
      colorSchemeQuery.addListener(syncSystemTheme);
    }
  }
  document.addEventListener("mousedown", onGlobalPointerDown);
  window.addEventListener("keydown", onGlobalKeydown);
  window.addEventListener("beforeunload", onBeforeUnload);
  window.addEventListener("popstate", syncTabFromRoute);
  void load();
});

onBeforeUnmount(() => {
  if (colorSchemeQuery) {
    if (typeof colorSchemeQuery.removeEventListener === "function") {
      colorSchemeQuery.removeEventListener("change", syncSystemTheme);
    } else if (typeof colorSchemeQuery.removeListener === "function") {
      colorSchemeQuery.removeListener(syncSystemTheme);
    }
  }
  document.removeEventListener("mousedown", onGlobalPointerDown);
  window.removeEventListener("keydown", onGlobalKeydown);
  window.removeEventListener("beforeunload", onBeforeUnload);
  window.removeEventListener("popstate", syncTabFromRoute);
});

watch(
  [resolvedTheme, themeStyleVars],
  ([theme, styleVars]) => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    for (const [name, value] of Object.entries(styleVars)) {
      document.documentElement.style.setProperty(name, value);
    }
  },
  { immediate: true }
);
watch(themePreferences, persistThemePreferences, { deep: true });
watch([botProfileQuery, botPlatformFilter], () => {
  botPage.value = 1;
});
watch([filteredBotProfiles, botPageSize], () => {
  setBotPage(botPage.value);
});
watch([logView, logQuery, logLevelFilter, logStartDate, logEndDate], () => {
  logPage.value = 1;
});
watch([filteredAppLogs, logPageSize], () => {
  setLogPage(logPage.value);
});
watch(
  [
    activeTab,
    llmAdvancedOpen,
    pluginFilter,
    pluginCategory,
    pluginQuery,
    selectedPluginID,
    pluginDetailOpen,
    botPlatformFilter,
    botDetailTab,
    botPageSize,
    logKind,
    logView,
    logQuery,
    logLevelFilter,
    logStartDate,
    logEndDate,
    logPageSize,
    message
  ],
  persistUIState
);
watch(botSectionsOpen, persistUIState, { deep: true });
watch(updateHistory, persistUIState, { deep: true });
watch(testSessions, persistUIState, { deep: true });
</script>
