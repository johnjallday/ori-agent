import js from '@eslint/js';
import globals from 'globals';
import json from '@eslint/json';
import markdown from '@eslint/markdown';
import css from '@eslint/css';
import { defineConfig } from 'eslint/config';

const appGlobals = {
  API: 'readonly',
  Logger: 'readonly',
  EventBus: 'readonly',
  DOMUtils: 'readonly',
  escapeHtml: 'readonly',
  escapeAttr: 'readonly',
  escapeJs: 'readonly',
  stripVersionSuffix: 'readonly',
  Toast: 'readonly',
  FormValidation: 'writable',
  ExternalAgents: 'readonly',
  showNotification: 'readonly',
  showPluginConfigModal: 'readonly',
  showPluginStoreModal: 'readonly',
  showPluginUploadModal: 'readonly',
  loadAgents: 'readonly',
  loadAgentsForSidebar: 'readonly',
  loadPlugins: 'readonly',
  loadPluginsForSidebar: 'readonly',
  loadSettings: 'readonly',
  loadAgent: 'readonly',
  refreshPluginList: 'readonly',
  currentAgent: 'writable',
  showToast: 'readonly',
  themeManager: 'readonly',
  addMessageToChat: 'readonly',
  appendMessageToUI: 'readonly',
  clearChatHistory: 'readonly',
  FileManager: 'readonly',
  loadAvailableProviders: 'readonly',
  studiosSystemAgents: 'writable',
  loadWorkspaceAgents: 'readonly',
  loadWorkspaces: 'readonly',
  AgentCanvas: 'readonly',
  selectedAgents: 'writable',
  availableAgents: 'writable',
  loadCanvasStudio: 'readonly',
  tasksAssignToCombiner: 'readonly',
  combinerExecute: 'readonly',
  combinerCreateTask: 'readonly',
  combinerEnsureTask: 'readonly',
  MessageTimeline: 'readonly',
  SettingsNavigation: 'readonly',
  SettingsController: 'readonly',
  showAddTaskModal: 'readonly',
  resetBaseAutoConfigState: 'readonly',
  bootstrap: 'readonly',
  marked: 'readonly',
  hljs: 'readonly',
  mermaid: 'readonly',
  ethers: 'readonly',
  web3SettingsManager: 'readonly',
  module: 'readonly',
  require: 'readonly',
  exports: 'readonly',
  DOMPurify: 'readonly'
};

export default defineConfig([
  {
    files: ['**/*.{js,mjs,cjs}'],
    plugins: { js },
    extends: ['js/recommended'],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'script',
      globals: {
        ...globals.browser,
        ...appGlobals
      }
    },
    rules: {
      'no-empty': ['error', { allowEmptyCatch: true }],
      'no-redeclare': ['error', { builtinGlobals: false }],
      'no-unused-vars': [
        'error',
        {
          vars: 'local',
          varsIgnorePattern: '^_',
          argsIgnorePattern: '^_|^(e|err|error|event|index|idx|x|y)$',
          caughtErrors: 'none'
        }
      ]
    }
  },
  {
    files: [
      'internal/web/static/js/modules/agent-canvas-animation.js',
      'internal/web/static/js/modules/agent-canvas-api.js',
      'internal/web/static/js/modules/agent-canvas-context-menu.js',
      'internal/web/static/js/modules/agent-canvas-dialogs.js',
      'internal/web/static/js/modules/agent-canvas-event-handler.js',
      'internal/web/static/js/modules/agent-canvas-events.js',
      'internal/web/static/js/modules/agent-canvas-forms.js',
      'internal/web/static/js/modules/agent-canvas-helpers.js',
      'internal/web/static/js/modules/agent-canvas-init.js',
      'internal/web/static/js/modules/agent-canvas-interactions.js',
      'internal/web/static/js/modules/agent-canvas-layout.js',
      'internal/web/static/js/modules/agent-canvas-notifications.js',
      'internal/web/static/js/modules/agent-canvas-panels.js',
      'internal/web/static/js/modules/agent-canvas-renderer.js',
      'internal/web/static/js/modules/agent-canvas-state.js',
      'internal/web/static/js/modules/agent-canvas-tasks.js',
      'internal/web/static/js/modules/agent-canvas-timeline.js',
      'internal/web/static/js/modules/agent-canvas-workflow-selector.js',
      'internal/web/static/js/modules/agent-canvas.js',
      'internal/web/static/js/modules/chat-auto-scroll.js',
      'internal/web/static/js/modules/chat-state-ui.js',
      'internal/web/static/js/modules/chat-state.js',
      'internal/web/static/js/modules/dashboard-agents.js',
      'internal/web/static/js/modules/dashboard-renderer.js',
      'internal/web/static/js/modules/dashboard-state.js',
      'internal/web/static/js/modules/dashboard-tasks.js',
      'internal/web/static/js/modules/dashboard-ui.js',
      'internal/web/static/js/modules/note-ai-assist.js',
      'internal/web/static/js/modules/note-backlinks.js',
      'internal/web/static/js/modules/note-editor.js',
      'internal/web/static/js/modules/note-page.js',
      'internal/web/static/js/modules/note-presence.js',
      'internal/web/static/js/modules/note-rail-notes.js',
      'internal/web/static/js/modules/note-routes.js',
      'internal/web/static/js/modules/note-staging.js',
      'internal/web/static/js/modules/note-tabs.js',
      'internal/web/static/js/modules/note-toc.js',
      'internal/web/static/js/modules/note-wikilinks.js',
      'internal/web/static/js/modules/onboarding.js',
      'internal/web/static/js/modules/plugin-init-banner.js',
      'internal/web/static/js/modules/renderer-connections.js',
      'internal/web/static/js/modules/renderer-nodes.js',
      'internal/web/static/js/modules/renderer-panels.js',
      'internal/web/static/js/modules/renderer-primitives.js',
      'internal/web/static/js/modules/renderer-ui.js',
      'internal/web/static/js/modules/search-palette.js',
      'internal/web/static/js/modules/smartOnboarding.js',
      'internal/web/static/js/modules/studio-dashboard.js',
      'internal/web/static/js/modules/studio.js',
      'internal/web/static/js/modules/workspace-detail.js',
      'internal/web/static/js/modules/workspace-detail-members.js',
      'internal/web/static/js/modules/workspace-detail-directory-explorer.js',
      'internal/web/static/js/modules/workspace-detail-file-modal.js',
      'internal/web/static/js/modules/workspace-detail-mcp.js',
      'internal/web/static/js/modules/workspace-detail-skills.js',
      'internal/web/static/js/modules/workspace-detail-plugins.js',
      'internal/web/static/js/modules/workspace-detail-memory.js',
      'internal/web/static/js/modules/workspace-native-mcp.js',
      'internal/web/static/js/modules/workspace-task.js',
      'internal/web/static/js/modules/workspace-task-execution-views.js',
      'internal/web/static/js/modules/workspace-task-result-actions.js',
      'internal/web/static/js/modules/workspace-task-skill-draft.js',
      'internal/web/static/js/modules/relative-time.js',
      'internal/web/static/js/modules/workspace-run.js',
      'internal/web/static/js/modules/task-result-artifacts.js',
      'internal/web/static/js/modules/tag-input.js',
      'internal/web/static/js/modules/tag-filter-bar.js',
      'internal/web/static/js/modules/template-onboarding.js',
      'internal/web/static/js/modules/workspace-tags-card.js',
      'internal/web/static/js/modules/workspace-command.js',
    ],
    languageOptions: {
      sourceType: 'module'
    }
  },
  {
    files: ['internal/web/static/js/utils/*.js'],
    rules: {
      'no-unused-vars': 'off'
    }
  },
  {
    files: ['internal/web/static/js/modules/*.test.js'],
    languageOptions: {
      sourceType: 'module',
      globals: {
        ...globals.node
      }
    }
  },
  { files: ['**/*.json'], plugins: { json }, language: 'json/json', extends: ['json/recommended'] },
  { files: ['**/*.md'], plugins: { markdown }, language: 'markdown/gfm', extends: ['markdown/recommended'] },
  { files: ['**/*.css'], plugins: { css }, language: 'css/css', extends: ['css/recommended'] },
  {
    ignores: ['node_modules/', 'vendor/', '*.min.js', '**/*.min.js', 'internal/web/static/js/vendor/**', 'dist/', 'example_plugins/']
  }
]);
