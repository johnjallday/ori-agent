import js from '@eslint/js';
import globals from 'globals';

export default [
  js.configs.recommended,
  {
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'script',
      globals: {
        ...globals.browser,
        // Custom globals from DX utilities
        API: 'readonly',
        Logger: 'readonly',
        EventBus: 'readonly',
        // Application globals
        Toast: 'readonly',
        FormValidation: 'writable',
        // Cross-module functions (defined in other script files)
        showNotification: 'readonly',
        showPluginConfigModal: 'readonly',
        showPluginStoreModal: 'readonly',
        showPluginUploadModal: 'readonly',
        loadAgents: 'readonly',
        loadAgentsForSidebar: 'readonly',
        loadPlugins: 'readonly',
        loadPluginsForSidebar: 'readonly',
        loadSettings: 'readonly',
        switchToAgent: 'readonly',
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
        // Combiner/orchestration functions
        tasksAssignToCombiner: 'readonly',
        combinerExecute: 'readonly',
        combinerCreateTask: 'readonly',
        combinerEnsureTask: 'readonly',
        MessageTimeline: 'readonly',
        // External libraries
        bootstrap: 'readonly',
        marked: 'readonly',
        hljs: 'readonly',
        mermaid: 'readonly',
        // Node.js (for CommonJS modules)
        module: 'readonly',
        require: 'readonly',
        exports: 'readonly'
      }
    },
    rules: {
      'no-unused-vars': ['warn', {
        argsIgnorePattern: '^_',
        varsIgnorePattern: '^_'
      }],
      'no-console': 'off',
      'no-debugger': 'warn',
      'eqeqeq': ['warn', 'always', { null: 'ignore' }],
      'curly': ['warn', 'multi-line'],
      'no-var': 'warn',
      'prefer-const': 'warn',
      'no-multiple-empty-lines': ['warn', { max: 2 }],
      'no-trailing-spaces': 'warn',
      'semi': ['warn', 'always'],
      'quotes': ['warn', 'single', { avoidEscape: true, allowTemplateLiterals: true }],
      'indent': ['warn', 2, { SwitchCase: 1 }],
      'comma-dangle': ['warn', 'never'],
      'no-prototype-builtins': 'off',
      'no-async-promise-executor': 'warn',
      'no-return-await': 'warn',
      'require-await': 'warn',
      'no-useless-escape': 'warn',
      'no-useless-catch': 'warn',
      'no-redeclare': 'warn',
      'no-case-declarations': 'warn',
      'no-dupe-keys': 'warn'
    }
  },
  // ES Module files (use import/export) - match all files that use ES modules
  {
    files: [
      'internal/web/static/js/modules/*.js',
      'internal/web/static/js/file-manager.js',
      'internal/web/static/js/pages/*.js'
    ],
    languageOptions: {
      sourceType: 'module'
    }
  },
  // Utility files - allow unused vars (they export globals)
  {
    files: ['internal/web/static/js/utils/*.js'],
    rules: {
      'no-unused-vars': 'off'
    }
  },
  {
    ignores: [
      'node_modules/',
      'vendor/',
      '*.min.js',
      'dist/',
      'example_plugins/'
    ]
  }
];
