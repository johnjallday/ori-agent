(function() {
  const root = document.getElementById('behaviorStudioPage');
  if (!root) {
    return;
  }

  const ROLE_OPTIONS = [
    { value: 'general', label: 'General' },
    { value: 'researcher', label: 'Researcher' },
    { value: 'analyzer', label: 'Analyzer' },
    { value: 'synthesizer', label: 'Synthesizer' },
    { value: 'validator', label: 'Validator' },
    { value: 'specialist', label: 'Specialist' },
    { value: 'orchestrator', label: 'Orchestrator' }
  ];
  const PARAM_TYPE_OPTIONS = ['string', 'number', 'boolean', 'array', 'object'];
  const OUTPUT_FIELD_TYPES = ['string', 'number', 'integer', 'boolean', 'object', 'array'];
  const ORCHESTRATION_OPTIONS = [
    { value: 'graph', label: 'Graph' },
    { value: 'sequential', label: 'Sequential' }
  ];
  const COMBINATION_OPTIONS = [
    { value: 'structured_outputs', label: 'Structured Outputs' },
    { value: 'json_map', label: 'JSON Map' },
    { value: 'concat', label: 'Concatenate Text' },
    { value: 'last_result', label: 'Last Result' }
  ];
  const NS_PER_MINUTE = 60 * 1000000000;

  function notify(message, type = 'info', options = {}) {
    if (typeof window.notifyToast === 'function') {
      window.notifyToast(message, type, options);
      return;
    }
    if (window.Toast && typeof window.Toast.show === 'function') {
      window.Toast.show(message, type, options);
      return;
    }
    window.alert(String(message || ''));
  }

  function slugify(value) {
    const trimmed = String(value || '').trim().toLowerCase();
    if (!trimmed) {
      return '';
    }
    return trimmed
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 64);
  }

  function deepClone(value) {
    return JSON.parse(JSON.stringify(value));
  }

  function titleCase(value) {
    return String(value || '')
      .replace(/[_-]+/g, ' ')
      .replace(/\b\w/g, (match) => match.toUpperCase());
  }

  function safeArray(value) {
    return Array.isArray(value) ? value : [];
  }

  function safeObject(value) {
    return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
  }

  function minutesFromDuration(durationValue) {
    const numeric = Number(durationValue || 0);
    if (!Number.isFinite(numeric) || numeric <= 0) {
      return 0;
    }
    return Math.max(1, Math.round(numeric / NS_PER_MINUTE));
  }

  function durationFromMinutes(minutesValue) {
    const numeric = Number(minutesValue || 0);
    if (!Number.isFinite(numeric) || numeric <= 0) {
      return 0;
    }
    return Math.round(numeric * NS_PER_MINUTE);
  }

  function formatJSON(value) {
    if (!value || (typeof value === 'object' && Object.keys(value).length === 0)) {
      return '';
    }
    try {
      return JSON.stringify(value, null, 2);
    } catch (_error) {
      return '';
    }
  }

  function formatValueForInput(value, type) {
    if (value === null || value === undefined || value === '') {
      return '';
    }

    switch (type) {
      case 'number':
        return String(value);
      case 'boolean':
        return value ? 'true' : 'false';
      case 'array':
      case 'object':
        return formatJSON(value);
      default:
        return String(value);
    }
  }

  function truncate(value, maxLength = 90) {
    const text = String(value || '').trim();
    if (text.length <= maxLength) {
      return text;
    }
    return `${text.slice(0, maxLength - 1)}…`;
  }

  function buildBlankSchema() {
    return {
      name: '',
      description: '',
      strict: true,
      fields: [buildBlankSchemaField()]
    };
  }

  function buildBlankSchemaField() {
    return {
      name: '',
      type: 'string',
      description: '',
      required: true
    };
  }

  function normalizeSchema(schema) {
    if (!schema || typeof schema !== 'object') {
      return null;
    }
    const fields = safeArray(schema.fields)
      .map((field) => ({
        name: String(field?.name || '').trim(),
        type: OUTPUT_FIELD_TYPES.includes(String(field?.type || '').trim()) ? String(field.type).trim() : 'string',
        description: String(field?.description || '').trim(),
        required: Boolean(field?.required)
      }));

    return {
      name: String(schema.name || '').trim(),
      description: String(schema.description || '').trim(),
      strict: schema.strict !== false,
      fields: fields.length > 0 ? fields : [buildBlankSchemaField()]
    };
  }

  function serializeSchema(schema) {
    if (!schema) {
      return null;
    }
    const normalized = normalizeSchema(schema);
    const fields = safeArray(normalized?.fields)
      .filter((field) => String(field.name || '').trim() !== '')
      .map((field) => ({
        name: String(field.name || '').trim(),
        type: OUTPUT_FIELD_TYPES.includes(String(field.type || '').trim()) ? String(field.type).trim() : 'string',
        description: String(field.description || '').trim(),
        required: Boolean(field.required)
      }));

    if (fields.length === 0) {
      return null;
    }

    return {
      name: String(normalized.name || '').trim(),
      description: String(normalized.description || '').trim(),
      strict: normalized.strict !== false,
      fields
    };
  }

  function buildBlankParameter(index) {
    return {
      name: `param_${index + 1}`,
      type: 'string',
      description: '',
      required: false,
      default_value: '',
      _defaultText: ''
    };
  }

  function normalizeParameter(parameter, index) {
    const type = PARAM_TYPE_OPTIONS.includes(String(parameter?.type || '').trim())
      ? String(parameter.type).trim()
      : 'string';
    const defaultValue = parameter && Object.prototype.hasOwnProperty.call(parameter, 'default_value')
      ? parameter.default_value
      : '';

    return {
      name: String(parameter?.name || `param_${index + 1}`).trim(),
      type,
      description: String(parameter?.description || '').trim(),
      required: Boolean(parameter?.required),
      default_value: defaultValue,
      _defaultText: formatValueForInput(defaultValue, type)
    };
  }

  function buildBlankStep(index) {
    return {
      id: `step-${index + 1}`,
      name: `Step ${index + 1}`,
      role: 'general',
      agent_name: '',
      description: '',
      details: '',
      depends_on: [],
      priority: 3,
      timeout: durationFromMinutes(10),
      context: {},
      _contextText: '',
      output_schema: null
    };
  }

  function normalizeStep(step, index) {
    const normalizedRole = ROLE_OPTIONS.some((option) => option.value === String(step?.role || '').trim())
      ? String(step.role).trim()
      : 'general';

    return {
      id: String(step?.id || `step-${index + 1}`).trim(),
      name: String(step?.name || '').trim(),
      role: normalizedRole,
      agent_name: String(step?.agent_name || '').trim(),
      description: String(step?.description || '').trim(),
      details: String(step?.details || '').trim(),
      depends_on: safeArray(step?.depends_on).map((dep) => String(dep || '').trim()).filter(Boolean),
      priority: Math.min(5, Math.max(1, Number(step?.priority || 3) || 3)),
      timeout: Number(step?.timeout || 0) || durationFromMinutes(10),
      context: safeObject(step?.context),
      _contextText: formatJSON(step?.context),
      output_schema: normalizeSchema(step?.output_schema)
    };
  }

  function buildBlankTemplate() {
    const blank = {
      id: '',
      name: '',
      description: '',
      category: '',
      source: 'custom',
      required_roles: [],
      parameters: [buildBlankParameter(0)],
      steps: [buildBlankStep(0)],
      orchestration_mode: 'graph',
      result_combination_mode: 'structured_outputs',
      combination_instruction: '',
      output_schema: buildBlankSchema(),
      created_at: '',
      updated_at: ''
    };
    return blank;
  }

  function normalizeTemplate(template) {
    if (!template || typeof template !== 'object') {
      return buildBlankTemplate();
    }

    return {
      id: String(template.id || '').trim(),
      name: String(template.name || '').trim(),
      description: String(template.description || '').trim(),
      category: String(template.category || '').trim(),
      source: String(template.source || 'custom').trim() || 'custom',
      required_roles: safeArray(template.required_roles).map((role) => String(role || '').trim()).filter(Boolean),
      parameters: safeArray(template.parameters).map((parameter, index) => normalizeParameter(parameter, index)),
      steps: safeArray(template.steps).map((step, index) => normalizeStep(step, index)),
      orchestration_mode: ORCHESTRATION_OPTIONS.some((option) => option.value === String(template.orchestration_mode || '').trim())
        ? String(template.orchestration_mode).trim()
        : 'graph',
      result_combination_mode: COMBINATION_OPTIONS.some((option) => option.value === String(template.result_combination_mode || '').trim())
        ? String(template.result_combination_mode).trim()
        : 'structured_outputs',
      combination_instruction: String(template.combination_instruction || '').trim(),
      output_schema: normalizeSchema(template.output_schema),
      created_at: template.created_at || '',
      updated_at: template.updated_at || ''
    };
  }

  function uniqueRoles(steps) {
    return Array.from(new Set(
      safeArray(steps)
        .map((step) => String(step?.role || '').trim())
        .filter(Boolean)
    ));
  }

  function parseValueByType(rawValue, type, label) {
    const raw = String(rawValue || '').trim();
    if (raw === '') {
      return undefined;
    }

    switch (type) {
      case 'number': {
        const parsed = Number(raw);
        if (!Number.isFinite(parsed)) {
          throw new Error(`${label} must be a number`);
        }
        return parsed;
      }
      case 'boolean':
        return raw === 'true';
      case 'array': {
        const parsed = JSON.parse(raw);
        if (!Array.isArray(parsed)) {
          throw new Error(`${label} must be a JSON array`);
        }
        return parsed;
      }
      case 'object': {
        const parsed = JSON.parse(raw);
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
          throw new Error(`${label} must be a JSON object`);
        }
        return parsed;
      }
      default:
        return raw;
    }
  }

  function detectGraphCycle(steps) {
    const stepIds = new Set(safeArray(steps).map((step) => step.id));
    const indegree = new Map();
    const adjacency = new Map();

    safeArray(steps).forEach((step) => {
      indegree.set(step.id, 0);
      adjacency.set(step.id, []);
    });

    safeArray(steps).forEach((step) => {
      safeArray(step.depends_on).forEach((depId) => {
        if (!stepIds.has(depId)) {
          return;
        }
        adjacency.get(depId).push(step.id);
        indegree.set(step.id, (indegree.get(step.id) || 0) + 1);
      });
    });

    const queue = [];
    indegree.forEach((count, stepId) => {
      if (count === 0) {
        queue.push(stepId);
      }
    });

    let visited = 0;
    while (queue.length > 0) {
      const currentId = queue.shift();
      visited += 1;
      const neighbors = adjacency.get(currentId) || [];
      neighbors.forEach((neighborId) => {
        const next = (indegree.get(neighborId) || 0) - 1;
        indegree.set(neighborId, next);
        if (next === 0) {
          queue.push(neighborId);
        }
      });
    }

    return visited !== safeArray(steps).length;
  }

  function buildGraphLayout(steps) {
    const normalizedSteps = safeArray(steps).filter((step) => step && step.id);
    const stepIds = new Set(normalizedSteps.map((step) => step.id));
    const indegree = new Map();
    const dependents = new Map();
    const layerById = new Map();

    normalizedSteps.forEach((step) => {
      indegree.set(step.id, 0);
      dependents.set(step.id, []);
    });

    normalizedSteps.forEach((step) => {
      safeArray(step.depends_on).forEach((depId) => {
        if (!stepIds.has(depId)) {
          return;
        }
        indegree.set(step.id, (indegree.get(step.id) || 0) + 1);
        dependents.get(depId).push(step.id);
      });
    });

    const queue = normalizedSteps
      .filter((step) => (indegree.get(step.id) || 0) === 0)
      .map((step) => step.id);

    const order = [];
    while (queue.length > 0) {
      const currentId = queue.shift();
      order.push(currentId);
      const currentLayer = layerById.get(currentId) || 0;
      const neighbors = dependents.get(currentId) || [];
      neighbors.forEach((neighborId) => {
        layerById.set(neighborId, Math.max(layerById.get(neighborId) || 0, currentLayer + 1));
        const next = (indegree.get(neighborId) || 0) - 1;
        indegree.set(neighborId, next);
        if (next === 0) {
          queue.push(neighborId);
        }
      });
    }

    if (order.length !== normalizedSteps.length) {
      return null;
    }

    const layers = [];
    normalizedSteps.forEach((step) => {
      const layerIndex = layerById.get(step.id) || 0;
      if (!layers[layerIndex]) {
        layers[layerIndex] = [];
      }
      layers[layerIndex].push(step);
    });

    const maxLayerSize = Math.max(...layers.map((layer) => layer.length), 1);
    const width = Math.max(720, layers.length * 220 + 120);
    const height = Math.max(240, maxLayerSize * 130 + 120);
    const positions = {};

    layers.forEach((layer, layerIndex) => {
      const x = 110 + layerIndex * 220;
      const totalHeight = Math.max(1, layer.length - 1) * 120;
      const startY = height / 2 - totalHeight / 2;
      layer.forEach((step, index) => {
        positions[step.id] = {
          x,
          y: startY + index * 120
        };
      });
    });

    return {
      width,
      height,
      positions
    };
  }

  function getRoleAccent(role) {
    const palette = {
      researcher: '#38bdf8',
      analyzer: '#f97316',
      synthesizer: '#22c55e',
      validator: '#ef4444',
      specialist: '#eab308',
      orchestrator: '#8b5cf6',
      general: '#94a3b8'
    };
    return palette[String(role || 'general').trim()] || palette.general;
  }

  class BehaviorStudio {
    constructor(rootElement) {
      this.root = rootElement;
      this.listEl = document.getElementById('behaviorStudioList');
      this.editorEl = document.getElementById('behaviorStudioEditor');
      this.previewEl = document.getElementById('behaviorStudioPreview');
      this.searchInput = document.getElementById('behaviorStudioSearch');
      this.sourceFilter = document.getElementById('behaviorStudioSourceFilter');
      this.categoryFilter = document.getElementById('behaviorStudioCategoryFilter');
      this.newButton = document.getElementById('behaviorStudioNewBtn');
      this.importButton = document.getElementById('behaviorStudioImportBtn');
      this.importInput = document.getElementById('behaviorStudioImportInput');
      this.countEl = document.getElementById('behaviorStudioCount');
      this.customCountEl = document.getElementById('behaviorStudioCustomCount');
      this.selectedLabelEl = document.getElementById('behaviorStudioSelectedLabel');
      this.launchModalEl = document.getElementById('behaviorLaunchModal');
      this.launchModalBodyEl = document.getElementById('behaviorLaunchModalBody');
      this.launchModalSubmitEl = document.getElementById('behaviorLaunchSubmit');
      this.launchModal = this.launchModalEl && window.bootstrap
        ? new window.bootstrap.Modal(this.launchModalEl)
        : null;
      this.beforeUnloadHandler = (event) => {
        if (!this.state.isDirty) {
          return;
        }
        event.preventDefault();
        event.returnValue = '';
      };

      this.state = {
        templates: [],
        filteredTemplates: [],
        selectedTemplateId: '',
        originalTemplateId: '',
        draft: buildBlankTemplate(),
        search: '',
        sourceFilter: 'all',
        categoryFilter: 'all',
        isDirty: false,
        workspaces: [],
        launchState: null,
        launchAgents: [],
        launchWorkspaceName: ''
      };
    }

    getRequestedTemplateId() {
      const params = new URLSearchParams(window.location.search);
      return String(params.get('template') || '').trim();
    }

    syncTemplateQueryParam(templateId = '') {
      const url = new URL(window.location.href);
      const safeTemplateId = String(templateId || '').trim();
      if (safeTemplateId) {
        url.searchParams.set('template', safeTemplateId);
      } else {
        url.searchParams.delete('template');
      }
      window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`);
    }

    async init() {
      this.bindEvents();
      await Promise.all([
        this.loadTemplates(),
        this.loadWorkspaces()
      ]);
      const requestedTemplateId = this.getRequestedTemplateId();
      if (requestedTemplateId) {
        const requestedTemplate = this.state.templates.find((template) => template.id === requestedTemplateId);
        if (requestedTemplate) {
          this.selectTemplate(requestedTemplate.id, { skipDirtyCheck: true });
          return;
        }
        notify(`Behavior "${requestedTemplateId}" was not found.`, 'warning');
      }
      this.ensureSelection();
      this.syncTemplateQueryParam(this.state.selectedTemplateId || '');
      this.render();
    }

    bindEvents() {
      window.addEventListener('beforeunload', this.beforeUnloadHandler);

      this.searchInput?.addEventListener('input', (event) => {
        this.state.search = String(event.target.value || '');
        this.applyFilters();
        this.renderSidebar();
      });

      this.sourceFilter?.addEventListener('change', (event) => {
        this.state.sourceFilter = String(event.target.value || 'all');
        this.applyFilters();
        this.renderSidebar();
      });

      this.categoryFilter?.addEventListener('change', (event) => {
        this.state.categoryFilter = String(event.target.value || 'all');
        this.applyFilters();
        this.renderSidebar();
      });

      this.newButton?.addEventListener('click', () => this.startNewBehavior());
      this.importButton?.addEventListener('click', () => this.importInput?.click());
      this.importInput?.addEventListener('change', (event) => this.handleImport(event));

      this.listEl?.addEventListener('click', (event) => this.handleListClick(event));
      this.editorEl?.addEventListener('click', (event) => this.handleEditorClick(event));
      this.editorEl?.addEventListener('input', (event) => this.handleEditorInput(event));
      this.editorEl?.addEventListener('change', (event) => this.handleEditorInput(event));
      this.previewEl?.addEventListener('click', (event) => this.handlePreviewClick(event));
      this.launchModalBodyEl?.addEventListener('input', (event) => this.handleLaunchInput(event));
      this.launchModalBodyEl?.addEventListener('change', (event) => this.handleLaunchInput(event));
      this.launchModalSubmitEl?.addEventListener('click', () => this.instantiateBehavior());
    }

    async fetchJSON(url, options = {}) {
      const response = await fetch(url, options);
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || `Request failed: ${response.status}`);
      }
      return response.json();
    }

    async loadTemplates() {
      const payload = await this.fetchJSON('/api/orchestration/templates');
      const templates = safeArray(payload.templates).map((template) => normalizeTemplate(template));
      templates.sort((left, right) => {
        if (left.source !== right.source) {
          return left.source === 'custom' ? -1 : 1;
        }
        return left.name.localeCompare(right.name);
      });
      this.state.templates = templates;
      this.applyFilters();
    }

    async loadWorkspaces() {
      try {
        const payload = await this.fetchJSON('/api/workspaces');
        const workspaces = safeArray(payload.workspaces || payload.folders).map((workspace) => ({
          id: String(workspace.id || '').trim(),
          name: String(workspace.name || 'Untitled Workspace').trim(),
          description: String(workspace.description || '').trim(),
          agents: safeArray(workspace.agents)
        }));
        workspaces.sort((left, right) => left.name.localeCompare(right.name));
        this.state.workspaces = workspaces;
      } catch (error) {
        console.error('Failed to load workspaces', error);
        this.state.workspaces = [];
      }
    }

    async loadWorkspaceAgents(workspaceId) {
      if (!workspaceId) {
        this.state.launchAgents = [];
        this.state.launchWorkspaceName = '';
        return;
      }

      try {
        const payload = await this.fetchJSON(`/api/workspaces/${encodeURIComponent(workspaceId)}`);
        this.state.launchAgents = safeArray(payload.agents).map((agent) => String(agent || '').trim()).filter(Boolean);
        this.state.launchWorkspaceName = String(payload.name || '').trim();
      } catch (error) {
        console.error('Failed to load workspace agents', error);
        this.state.launchAgents = [];
        this.state.launchWorkspaceName = '';
      }
    }

    applyFilters() {
      const searchNeedle = this.state.search.trim().toLowerCase();
      this.state.filteredTemplates = this.state.templates.filter((template) => {
        const matchesSearch = !searchNeedle ||
          template.name.toLowerCase().includes(searchNeedle) ||
          template.description.toLowerCase().includes(searchNeedle) ||
          template.category.toLowerCase().includes(searchNeedle);
        const matchesSource = this.state.sourceFilter === 'all' || template.source === this.state.sourceFilter;
        const matchesCategory = this.state.categoryFilter === 'all' || template.category === this.state.categoryFilter;
        return matchesSearch && matchesSource && matchesCategory;
      });
    }

    ensureSelection() {
      const current = this.state.templates.find((template) => template.id === this.state.selectedTemplateId);
      if (current) {
        return;
      }

      const firstTemplate = this.state.templates[0];
      if (firstTemplate) {
        this.selectTemplate(firstTemplate.id, { skipDirtyCheck: true });
        return;
      }

      this.state.selectedTemplateId = '';
      this.state.originalTemplateId = '';
      this.state.draft = buildBlankTemplate();
      this.state.isDirty = false;
      this.state.launchState = this.buildLaunchState();
    }

    confirmDiscardChanges() {
      if (!this.state.isDirty) {
        return true;
      }
      return window.confirm('Discard unsaved behavior changes?');
    }

    startNewBehavior() {
      if (!this.confirmDiscardChanges()) {
        return;
      }
      const blank = buildBlankTemplate();
      this.state.selectedTemplateId = '';
      this.state.originalTemplateId = '';
      this.state.draft = blank;
      this.state.isDirty = true;
      this.state.launchState = this.buildLaunchState();
      this.syncTemplateQueryParam('');
      this.render();
    }

    selectTemplate(templateId, { skipDirtyCheck = false } = {}) {
      if (!skipDirtyCheck && !this.confirmDiscardChanges()) {
        return;
      }
      const template = this.state.templates.find((item) => item.id === templateId);
      if (!template) {
        return;
      }
      this.state.selectedTemplateId = template.id;
      this.state.originalTemplateId = template.id;
      this.state.draft = deepClone(template);
      this.state.isDirty = false;
      this.state.launchState = this.buildLaunchState();
      this.syncTemplateQueryParam(template.id);
      this.render();
    }

    duplicateSelectedBehavior() {
      const current = this.state.draft;
      if (!current) {
        return;
      }
      if (!this.confirmDiscardChanges()) {
        return;
      }

      const clone = deepClone(current);
      clone.source = 'custom';
      clone.id = this.buildUniqueTemplateId(`${current.id || slugify(current.name) || 'behavior'}-copy`);
      clone.name = current.name ? `${current.name} Copy` : 'Behavior Copy';
      clone.created_at = '';
      clone.updated_at = '';
      this.state.selectedTemplateId = '';
      this.state.originalTemplateId = '';
      this.state.draft = normalizeTemplate(clone);
      this.state.isDirty = true;
      this.state.launchState = this.buildLaunchState();
      this.syncTemplateQueryParam('');
      this.render();
    }

    async deleteSelectedBehavior() {
      const current = this.state.draft;
      if (!current || !current.id || current.source !== 'custom') {
        notify('Only custom behaviors can be deleted from this page.', 'warning');
        return;
      }

      const confirmed = window.confirm(`Delete "${current.name || current.id}"?`);
      if (!confirmed) {
        return;
      }

      try {
        await fetch(`/api/orchestration/templates?id=${encodeURIComponent(current.id)}`, {
          method: 'DELETE'
        }).then(async (response) => {
          if (!response.ok) {
            throw new Error(await response.text());
          }
        });
        notify('Behavior deleted.', 'success');
        await this.loadTemplates();
        this.state.selectedTemplateId = '';
        this.state.originalTemplateId = '';
        this.ensureSelection();
        this.syncTemplateQueryParam(this.state.selectedTemplateId || '');
        this.render();
      } catch (error) {
        console.error('Failed to delete behavior', error);
        notify(error.message || 'Failed to delete behavior.', 'error');
      }
    }

    async saveBehavior() {
      try {
        const payload = this.buildTemplatePayload();
        const existing = this.state.templates.find((template) => template.id === payload.id);
        if (existing && payload.id !== this.state.originalTemplateId) {
          throw new Error(`A behavior with id "${payload.id}" already exists. Use a different id.`);
        }

        const response = await fetch('/api/orchestration/templates', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        });
        if (!response.ok) {
          throw new Error(await response.text());
        }

        const saved = normalizeTemplate(await response.json());
        notify('Behavior saved.', 'success');
        await this.loadTemplates();
        this.state.selectedTemplateId = saved.id;
        this.state.originalTemplateId = saved.id;
        this.state.draft = saved;
        this.state.isDirty = false;
        this.state.launchState = this.buildLaunchState();
        this.syncTemplateQueryParam(saved.id);
        this.render();
      } catch (error) {
        console.error('Failed to save behavior', error);
        notify(error.message || 'Failed to save behavior.', 'error');
      }
    }

    buildUniqueTemplateId(baseValue) {
      const base = slugify(baseValue) || 'behavior';
      const reserved = new Set(this.state.templates.map((template) => template.id));
      if (!reserved.has(base)) {
        return base;
      }
      let suffix = 2;
      while (reserved.has(`${base}-${suffix}`)) {
        suffix += 1;
      }
      return `${base}-${suffix}`;
    }

    parseStepContext(step, index) {
      const rawText = String(step._contextText || '').trim();
      if (!rawText) {
        return {};
      }
      try {
        const parsed = JSON.parse(rawText);
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
          throw new Error('must be a JSON object');
        }
        return parsed;
      } catch (error) {
        throw new Error(`Step ${index + 1} context ${error.message}`);
      }
    }

    buildTemplatePayload() {
      const draft = this.state.draft;
      const payload = normalizeTemplate(deepClone(draft));
      payload.id = payload.id || this.buildUniqueTemplateId(payload.name || 'behavior');
      payload.name = String(payload.name || '').trim();
      payload.description = String(payload.description || '').trim();
      payload.category = String(payload.category || '').trim();
      payload.source = 'custom';

      if (!payload.name) {
        throw new Error('Behavior name is required.');
      }

      const parameters = [];
      safeArray(payload.parameters).forEach((parameter, index) => {
        const name = String(parameter.name || '').trim();
        if (!name) {
          return;
        }
        const type = PARAM_TYPE_OPTIONS.includes(String(parameter.type || '').trim())
          ? String(parameter.type).trim()
          : 'string';
        const normalized = {
          name,
          type,
          description: String(parameter.description || '').trim(),
          required: Boolean(parameter.required)
        };
        if (String(parameter._defaultText || '').trim() !== '') {
          normalized.default_value = parseValueByType(parameter._defaultText, type, `Default value for ${name}`);
        }
        parameters.push(normalized);
      });

      const stepIds = new Set();
      const steps = safeArray(payload.steps).map((step, index) => {
        const label = String(step.name || step.description || `step-${index + 1}`).trim();
        const stepId = String(step.id || '').trim() || slugify(label) || `step-${index + 1}`;
        if (stepIds.has(stepId)) {
          throw new Error(`Step IDs must be unique. Duplicate id: ${stepId}`);
        }
        stepIds.add(stepId);
        return {
          id: stepId,
          name: String(step.name || '').trim(),
          role: ROLE_OPTIONS.some((option) => option.value === String(step.role || '').trim())
            ? String(step.role).trim()
            : 'general',
          agent_name: String(step.agent_name || '').trim(),
          description: String(step.description || '').trim() || String(step.name || '').trim(),
          details: String(step.details || '').trim(),
          depends_on: safeArray(step.depends_on).map((dep) => String(dep || '').trim()).filter(Boolean),
          priority: Math.min(5, Math.max(1, Number(step.priority || 3) || 3)),
          timeout: durationFromMinutes(minutesFromDuration(step.timeout)),
          context: this.parseStepContext(step, index),
          output_schema: serializeSchema(step.output_schema)
        };
      });

      if (steps.length === 0) {
        throw new Error('Add at least one step to the behavior.');
      }

      steps.forEach((step) => {
        if (!step.description) {
          throw new Error(`Step "${step.id}" needs a description.`);
        }
        step.depends_on.forEach((dependencyId) => {
          if (dependencyId === step.id) {
            throw new Error(`Step "${step.id}" cannot depend on itself.`);
          }
          if (!stepIds.has(dependencyId)) {
            throw new Error(`Step "${step.id}" depends on unknown step "${dependencyId}".`);
          }
        });
      });

      if (detectGraphCycle(steps)) {
        throw new Error('Step graph contains a cycle. Remove circular dependencies before saving.');
      }

      return {
        id: payload.id,
        name: payload.name,
        description: payload.description,
        category: payload.category,
        source: 'custom',
        required_roles: uniqueRoles(steps),
        parameters,
        steps,
        orchestration_mode: payload.orchestration_mode || 'graph',
        result_combination_mode: payload.result_combination_mode || 'structured_outputs',
        combination_instruction: String(payload.combination_instruction || '').trim(),
        output_schema: serializeSchema(payload.output_schema)
      };
    }

    buildLaunchState() {
      const draft = normalizeTemplate(this.state.draft);
      const parameterInputs = {};
      safeArray(draft.parameters).forEach((parameter) => {
        parameterInputs[parameter.name] = formatValueForInput(parameter.default_value, parameter.type);
      });
      return {
        workspaceId: '',
        fallbackAgent: '',
        orchestrationMode: '',
        resultCombinationMode: '',
        combinationInstruction: String(draft.combination_instruction || '').trim(),
        parameterInputs,
        agentAssignments: {}
      };
    }

    openLaunchModal() {
      if (!this.state.draft || !this.state.draft.name) {
        notify('Select or create a behavior first.', 'warning');
        return;
      }
      if (this.state.isDirty) {
        notify('Save the behavior before instantiating it into a workspace.', 'warning');
        return;
      }
      this.state.launchState = this.buildLaunchState();
      this.state.launchAgents = [];
      this.state.launchWorkspaceName = '';
      this.renderLaunchModal();
      this.launchModal?.show();
    }

    handleListClick(event) {
      const actionButton = event.target.closest('[data-list-action]');
      if (actionButton) {
        const action = actionButton.dataset.listAction;
        const templateId = actionButton.dataset.templateId;
        if (action === 'duplicate') {
          const template = this.state.templates.find((item) => item.id === templateId);
          if (!template) {
            return;
          }
          if (!this.confirmDiscardChanges()) {
            return;
          }
          this.state.draft = normalizeTemplate(template);
          this.state.isDirty = false;
          this.duplicateSelectedBehavior();
        } else if (action === 'delete') {
          const template = this.state.templates.find((item) => item.id === templateId);
          if (!template) {
            return;
          }
          if (this.state.selectedTemplateId !== template.id) {
            this.state.selectedTemplateId = template.id;
            this.state.originalTemplateId = template.id;
            this.state.draft = deepClone(template);
            this.state.isDirty = false;
          }
          this.deleteSelectedBehavior();
        }
        return;
      }

      const card = event.target.closest('[data-template-id]');
      if (!card) {
        return;
      }
      this.selectTemplate(card.dataset.templateId);
    }

    handlePreviewClick(event) {
      const action = event.target.closest('[data-preview-action]');
      if (!action) {
        return;
      }
      const actionName = action.dataset.previewAction;
      switch (actionName) {
        case 'save':
          this.saveBehavior();
          break;
        case 'duplicate':
          this.duplicateSelectedBehavior();
          break;
        case 'delete':
          this.deleteSelectedBehavior();
          break;
        case 'export':
          this.exportSelectedBehavior();
          break;
        case 'launch':
          this.openLaunchModal();
          break;
        default:
          break;
      }
    }

    exportSelectedBehavior() {
      try {
        const payload = this.buildTemplatePayload();
        const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
        const url = window.URL.createObjectURL(blob);
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = `${payload.id || slugify(payload.name) || 'behavior'}.json`;
        document.body.appendChild(anchor);
        anchor.click();
        document.body.removeChild(anchor);
        window.URL.revokeObjectURL(url);
      } catch (error) {
        notify(error.message || 'Failed to export behavior.', 'error');
      }
    }

    async handleImport(event) {
      const file = event.target.files && event.target.files[0];
      if (!file) {
        return;
      }
      try {
        const text = await file.text();
        const parsed = JSON.parse(text);
        const imported = normalizeTemplate(parsed.templates?.[0] || parsed);
        if (!this.confirmDiscardChanges()) {
          return;
        }
        imported.source = 'custom';
        imported.id = this.buildUniqueTemplateId(imported.id || slugify(imported.name) || 'imported-behavior');
        imported.name = imported.name || file.name.replace(/\.json$/i, '');
        this.state.selectedTemplateId = '';
        this.state.originalTemplateId = '';
        this.state.draft = imported;
        this.state.isDirty = true;
        this.state.launchState = this.buildLaunchState();
        this.syncTemplateQueryParam('');
        this.render();
        notify('Behavior imported. Save it to persist.', 'success');
      } catch (error) {
        console.error('Failed to import behavior', error);
        notify(error.message || 'Failed to import behavior JSON.', 'error');
      } finally {
        if (this.importInput) {
          this.importInput.value = '';
        }
      }
    }

    handleEditorClick(event) {
      const action = event.target.closest('[data-action]');
      if (!action) {
        return;
      }

      const actionName = action.dataset.action;
      const stepIndex = Number(action.dataset.stepIndex);
      const paramIndex = Number(action.dataset.paramIndex);
      const fieldIndex = Number(action.dataset.fieldIndex);

      switch (actionName) {
        case 'add-param':
          this.state.draft.parameters.push(buildBlankParameter(this.state.draft.parameters.length));
          this.markDirty(true);
          break;
        case 'remove-param':
          this.state.draft.parameters.splice(paramIndex, 1);
          if (this.state.draft.parameters.length === 0) {
            this.state.draft.parameters.push(buildBlankParameter(0));
          }
          this.markDirty(true);
          break;
        case 'add-step':
          this.state.draft.steps.push(buildBlankStep(this.state.draft.steps.length));
          this.markDirty(true);
          break;
        case 'remove-step':
          this.removeStep(stepIndex);
          break;
        case 'move-step-up':
          this.moveStep(stepIndex, -1);
          break;
        case 'move-step-down':
          this.moveStep(stepIndex, 1);
          break;
        case 'enable-template-schema':
          this.state.draft.output_schema = buildBlankSchema();
          this.markDirty(true);
          break;
        case 'disable-template-schema':
          this.state.draft.output_schema = null;
          this.markDirty(true);
          break;
        case 'add-template-schema-field':
          this.ensureTemplateSchema();
          this.state.draft.output_schema.fields.push(buildBlankSchemaField());
          this.markDirty(true);
          break;
        case 'remove-template-schema-field':
          this.ensureTemplateSchema();
          this.state.draft.output_schema.fields.splice(fieldIndex, 1);
          if (this.state.draft.output_schema.fields.length === 0) {
            this.state.draft.output_schema.fields.push(buildBlankSchemaField());
          }
          this.markDirty(true);
          break;
        case 'enable-step-schema':
          this.state.draft.steps[stepIndex].output_schema = buildBlankSchema();
          this.markDirty(true);
          break;
        case 'disable-step-schema':
          this.state.draft.steps[stepIndex].output_schema = null;
          this.markDirty(true);
          break;
        case 'add-step-schema-field':
          this.ensureStepSchema(stepIndex);
          this.state.draft.steps[stepIndex].output_schema.fields.push(buildBlankSchemaField());
          this.markDirty(true);
          break;
        case 'remove-step-schema-field':
          this.ensureStepSchema(stepIndex);
          this.state.draft.steps[stepIndex].output_schema.fields.splice(fieldIndex, 1);
          if (this.state.draft.steps[stepIndex].output_schema.fields.length === 0) {
            this.state.draft.steps[stepIndex].output_schema.fields.push(buildBlankSchemaField());
          }
          this.markDirty(true);
          break;
        default:
          break;
      }
    }

    handleEditorInput(event) {
      const target = event.target;
      if (!(target instanceof HTMLElement)) {
        return;
      }

      if (target.dataset.field) {
        const field = target.dataset.field;
        this.state.draft[field] = target.type === 'checkbox' ? target.checked : target.value;
        if (field === 'name' && !this.state.draft.id && target.value) {
          this.state.draft.id = slugify(target.value);
        }
        this.markDirty(false);
        return;
      }

      if (target.dataset.paramField) {
        const index = Number(target.dataset.paramIndex);
        const parameter = this.state.draft.parameters[index];
        if (!parameter) {
          return;
        }
        const field = target.dataset.paramField;
        if (field === 'required') {
          parameter.required = target.checked;
        } else if (field === 'type') {
          parameter.type = target.value;
          parameter._defaultText = formatValueForInput(parameter.default_value, parameter.type);
          this.markDirty(true);
          return;
        } else if (field === 'default_value') {
          parameter._defaultText = target.value;
        } else {
          parameter[field] = target.value;
        }
        this.markDirty(field === 'name' || field === 'type');
        return;
      }

      if (target.dataset.stepField) {
        const stepIndex = Number(target.dataset.stepIndex);
        const step = this.state.draft.steps[stepIndex];
        if (!step) {
          return;
        }
        const field = target.dataset.stepField;
        const previousId = step.id;
        if (field === 'priority') {
          step.priority = Number(target.value || 3);
        } else if (field === 'timeout_minutes') {
          step.timeout = durationFromMinutes(target.value);
        } else if (field === 'context_text') {
          step._contextText = target.value;
        } else {
          step[field] = target.type === 'checkbox' ? target.checked : target.value;
        }
        if (field === 'id' && previousId !== step.id) {
          this.rewireDependencies(previousId, step.id);
          this.markDirty(true);
          return;
        }
        this.markDirty(field === 'name');
        return;
      }

      if (target.dataset.stepDependency) {
        const stepIndex = Number(target.dataset.stepIndex);
        const step = this.state.draft.steps[stepIndex];
        if (!step) {
          return;
        }
        const dependencyId = target.dataset.stepDependency;
        if (target.checked) {
          if (!step.depends_on.includes(dependencyId)) {
            step.depends_on.push(dependencyId);
          }
        } else {
          step.depends_on = step.depends_on.filter((value) => value !== dependencyId);
        }
        this.markDirty(true);
        return;
      }

      if (target.dataset.schemaField) {
        const schema = this.resolveSchemaTarget(target.dataset.schemaScope, Number(target.dataset.stepIndex));
        if (!schema) {
          return;
        }
        const field = target.dataset.schemaField;
        if (field === 'strict') {
          schema.strict = target.checked;
        } else {
          schema[field] = target.value;
        }
        this.markDirty(false);
        return;
      }

      if (target.dataset.schemaItemField) {
        const schema = this.resolveSchemaTarget(target.dataset.schemaScope, Number(target.dataset.stepIndex));
        if (!schema) {
          return;
        }
        const field = safeArray(schema.fields)[Number(target.dataset.fieldIndex)];
        if (!field) {
          return;
        }
        const fieldName = target.dataset.schemaItemField;
        if (fieldName === 'required') {
          field.required = target.checked;
        } else {
          field[fieldName] = target.value;
        }
        this.markDirty(false);
      }
    }

    ensureTemplateSchema() {
      if (!this.state.draft.output_schema) {
        this.state.draft.output_schema = buildBlankSchema();
      }
    }

    ensureStepSchema(stepIndex) {
      const step = this.state.draft.steps[stepIndex];
      if (step && !step.output_schema) {
        step.output_schema = buildBlankSchema();
      }
    }

    resolveSchemaTarget(scope, stepIndex) {
      if (scope === 'template') {
        this.ensureTemplateSchema();
        return this.state.draft.output_schema;
      }
      if (scope === 'step') {
        this.ensureStepSchema(stepIndex);
        return this.state.draft.steps[stepIndex]?.output_schema || null;
      }
      return null;
    }

    moveStep(stepIndex, offset) {
      const nextIndex = stepIndex + offset;
      if (stepIndex < 0 || nextIndex < 0 || nextIndex >= this.state.draft.steps.length) {
        return;
      }
      const [step] = this.state.draft.steps.splice(stepIndex, 1);
      this.state.draft.steps.splice(nextIndex, 0, step);
      this.markDirty(true);
    }

    removeStep(stepIndex) {
      const [removed] = this.state.draft.steps.splice(stepIndex, 1);
      if (!removed) {
        return;
      }
      this.state.draft.steps.forEach((step) => {
        step.depends_on = safeArray(step.depends_on).filter((dependencyId) => dependencyId !== removed.id);
      });
      if (this.state.draft.steps.length === 0) {
        this.state.draft.steps.push(buildBlankStep(0));
      }
      this.markDirty(true);
    }

    rewireDependencies(previousId, nextId) {
      const oldId = String(previousId || '').trim();
      const newId = String(nextId || '').trim();
      if (!oldId || !newId || oldId === newId) {
        return;
      }
      this.state.draft.steps.forEach((step) => {
        step.depends_on = safeArray(step.depends_on).map((dependencyId) => dependencyId === oldId ? newId : dependencyId);
      });
    }

    markDirty(rerenderEditor) {
      this.state.isDirty = true;
      this.renderSidebar();
      if (rerenderEditor) {
        this.renderEditor();
      }
      this.renderPreview();
    }

    render() {
      this.renderSidebar();
      this.renderEditor();
      this.renderPreview();
      this.renderLaunchModal();
    }

    renderSidebar() {
      const categories = Array.from(new Set(
        this.state.templates
          .map((template) => template.category)
          .filter(Boolean)
      )).sort((left, right) => left.localeCompare(right));

      if (this.categoryFilter) {
        const currentValue = this.state.categoryFilter;
        this.categoryFilter.innerHTML = [
          '<option value="all">All categories</option>',
          ...categories.map((category) => `<option value="${escapeAttr(category)}">${escapeHtml(category)}</option>`)
        ].join('');
        this.categoryFilter.value = currentValue;
      }

      const selectedId = this.state.selectedTemplateId;
      this.listEl.innerHTML = this.state.filteredTemplates.map((template) => {
        const isSelected = template.id === selectedId;
        const stepCount = safeArray(template.steps).length;
        return `
          <article class="behavior-card ${isSelected ? 'is-selected' : ''}" data-template-id="${escapeAttr(template.id)}">
            <div class="behavior-card__head">
              <div>
                <div class="behavior-card__title">${escapeHtml(template.name || template.id || 'Untitled')}</div>
                <div class="behavior-card__meta">
                  <span class="behavior-badge behavior-badge--${escapeAttr(template.source || 'custom')}">${escapeHtml(template.source || 'custom')}</span>
                  <span>${stepCount} step${stepCount === 1 ? '' : 's'}</span>
                  ${template.category ? `<span>${escapeHtml(template.category)}</span>` : ''}
                </div>
              </div>
              <div class="behavior-card__actions">
                <button class="behavior-icon-btn" type="button" data-list-action="duplicate" data-template-id="${escapeAttr(template.id)}" title="Duplicate">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor"><path d="M19,21H9V7H19M19,3H9C7.89,3 7,3.89 7,5V17A2,2 0 0,0 9,19H19A2,2 0 0,0 21,17V5C21,3.89 20.1,3 19,3M5,7H3V19A2,2 0 0,0 5,21H17V19H5V7Z"/></svg>
                </button>
                ${template.source === 'custom' ? `
                  <button class="behavior-icon-btn" type="button" data-list-action="delete" data-template-id="${escapeAttr(template.id)}" title="Delete">
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor"><path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3M7,6H17V19H7M9,8V17H11V8M13,8V17H15V8Z"/></svg>
                  </button>
                ` : ''}
              </div>
            </div>
            <p class="behavior-card__summary">${escapeHtml(truncate(template.description || 'No description yet.', 108) || 'No description yet.')}</p>
          </article>
        `;
      }).join('');

      if (this.state.filteredTemplates.length === 0) {
        this.listEl.innerHTML = `
          <div class="behavior-empty-state">
            <div class="behavior-empty-state__title">No behaviors match these filters.</div>
            <div class="behavior-empty-state__copy">Clear the search or start a new behavior from scratch.</div>
          </div>
        `;
      }

      if (this.countEl) {
        this.countEl.textContent = String(this.state.templates.length);
      }
      if (this.customCountEl) {
        this.customCountEl.textContent = String(this.state.templates.filter((template) => template.source === 'custom').length);
      }
      if (this.selectedLabelEl) {
        const label = this.state.draft?.name || this.state.draft?.id || 'New behavior';
        this.selectedLabelEl.textContent = this.state.isDirty ? `${label} · Unsaved` : label;
      }
    }

    renderEditor() {
      const draft = normalizeTemplate(this.state.draft);
      const isBuiltin = draft.source === 'builtin';
      const stepCards = safeArray(draft.steps).map((step, index) => this.renderStepCard(step, index)).join('');
      const parameterRows = safeArray(draft.parameters).map((parameter, index) => this.renderParameterRow(parameter, index)).join('');
      const schemaEditor = this.renderSchemaEditor(draft.output_schema, { scope: 'template', heading: 'Parent Structured Output' });

      this.editorEl.innerHTML = `
        <section class="behavior-section">
          <div class="behavior-section__head">
            <div>
              <div class="behavior-section__eyebrow">Behavior Editor</div>
              <h2 class="behavior-section__title">Template metadata and execution rules</h2>
            </div>
            ${isBuiltin ? '<span class="behavior-readonly-pill">Built-in · duplicate to edit</span>' : '<span class="behavior-readonly-pill behavior-readonly-pill--active">Custom</span>'}
          </div>
          <fieldset class="behavior-editor-fieldset" ${isBuiltin ? 'disabled' : ''}>
            <div class="behavior-form-grid behavior-form-grid--meta">
              <label class="behavior-field">
                <span class="behavior-field__label">Name</span>
                <input class="behavior-input" type="text" data-field="name" value="${escapeAttr(draft.name)}" placeholder="Launch Readiness Review">
              </label>
              <label class="behavior-field">
                <span class="behavior-field__label">Behavior ID</span>
                <input class="behavior-input behavior-input--mono" type="text" data-field="id" value="${escapeAttr(draft.id)}" placeholder="launch-readiness-review">
              </label>
              <label class="behavior-field">
                <span class="behavior-field__label">Category</span>
                <input class="behavior-input" type="text" data-field="category" value="${escapeAttr(draft.category)}" placeholder="release">
              </label>
            </div>
            <label class="behavior-field">
              <span class="behavior-field__label">Description</span>
              <textarea class="behavior-textarea" rows="3" data-field="description" placeholder="Describe what this reusable behavior orchestrates.">${escapeHtml(draft.description)}</textarea>
            </label>

            <div class="behavior-subsection">
              <div class="behavior-subsection__head">
                <div>
                  <div class="behavior-subsection__title">Execution Model</div>
                  <div class="behavior-subsection__copy">Choose how the parent task runs and how child results are combined.</div>
                </div>
              </div>
              <div class="behavior-form-grid behavior-form-grid--rules">
                <label class="behavior-field">
                  <span class="behavior-field__label">Orchestration</span>
                  <select class="behavior-input" data-field="orchestration_mode">
                    ${ORCHESTRATION_OPTIONS.map((option) => `<option value="${option.value}" ${draft.orchestration_mode === option.value ? 'selected' : ''}>${option.label}</option>`).join('')}
                  </select>
                </label>
                <label class="behavior-field">
                  <span class="behavior-field__label">Result Combination</span>
                  <select class="behavior-input" data-field="result_combination_mode">
                    ${COMBINATION_OPTIONS.map((option) => `<option value="${option.value}" ${draft.result_combination_mode === option.value ? 'selected' : ''}>${option.label}</option>`).join('')}
                  </select>
                </label>
              </div>
              <label class="behavior-field">
                <span class="behavior-field__label">Combination Instruction</span>
                <textarea class="behavior-textarea behavior-textarea--compact" rows="2" data-field="combination_instruction" placeholder="Optional note for the final aggregation step.">${escapeHtml(draft.combination_instruction)}</textarea>
              </label>
            </div>

            <div class="behavior-subsection">
              <div class="behavior-subsection__head">
                <div>
                  <div class="behavior-subsection__title">Parameters</div>
                  <div class="behavior-subsection__copy">Expose inputs that users fill before they instantiate this behavior.</div>
                </div>
                <button class="behavior-btn behavior-btn--secondary" type="button" data-action="add-param">Add parameter</button>
              </div>
              <div class="behavior-stack">${parameterRows}</div>
            </div>

            ${schemaEditor}

            <div class="behavior-subsection">
              <div class="behavior-subsection__head">
                <div>
                  <div class="behavior-subsection__title">Step Graph</div>
                  <div class="behavior-subsection__copy">Each step can depend on any earlier or later step id. The preview panel renders the graph.</div>
                </div>
                <button class="behavior-btn behavior-btn--secondary" type="button" data-action="add-step">Add step</button>
              </div>
              <div class="behavior-step-list">${stepCards}</div>
            </div>
          </fieldset>
        </section>
      `;
    }

    renderParameterRow(parameter, index) {
      const usesTextarea = parameter.type === 'array' || parameter.type === 'object';
      return `
        <div class="behavior-parameter-row">
          <div class="behavior-parameter-row__grid">
            <label class="behavior-field">
              <span class="behavior-field__label">Name</span>
              <input class="behavior-input behavior-input--mono" type="text" data-param-index="${index}" data-param-field="name" value="${escapeAttr(parameter.name)}">
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Type</span>
              <select class="behavior-input" data-param-index="${index}" data-param-field="type">
                ${PARAM_TYPE_OPTIONS.map((option) => `<option value="${option}" ${parameter.type === option ? 'selected' : ''}>${titleCase(option)}</option>`).join('')}
              </select>
            </label>
            <label class="behavior-field behavior-field--toggle">
              <span class="behavior-field__label">Required</span>
              <input type="checkbox" data-param-index="${index}" data-param-field="required" ${parameter.required ? 'checked' : ''}>
            </label>
          </div>
          <label class="behavior-field">
            <span class="behavior-field__label">Description</span>
            <input class="behavior-input" type="text" data-param-index="${index}" data-param-field="description" value="${escapeAttr(parameter.description)}" placeholder="What this parameter controls">
          </label>
          <label class="behavior-field">
            <span class="behavior-field__label">Default Value</span>
            ${usesTextarea ? `
              <textarea class="behavior-textarea behavior-textarea--compact behavior-input--mono" rows="3" data-param-index="${index}" data-param-field="default_value" placeholder='${parameter.type === 'array' ? '[\"value\"]' : '{"key":"value"}'}'>${escapeHtml(parameter._defaultText)}</textarea>
            ` : parameter.type === 'boolean' ? `
              <select class="behavior-input" data-param-index="${index}" data-param-field="default_value">
                <option value="">No default</option>
                <option value="true" ${parameter._defaultText === 'true' ? 'selected' : ''}>True</option>
                <option value="false" ${parameter._defaultText === 'false' ? 'selected' : ''}>False</option>
              </select>
            ` : `
              <input class="behavior-input ${parameter.type === 'number' ? 'behavior-input--mono' : ''}" type="${parameter.type === 'number' ? 'number' : 'text'}" data-param-index="${index}" data-param-field="default_value" value="${escapeAttr(parameter._defaultText)}" placeholder="${parameter.type === 'number' ? '0' : 'Default value'}">
            `}
          </label>
          <div class="behavior-parameter-row__footer">
            <button class="behavior-link-btn" type="button" data-action="remove-param" data-param-index="${index}">Remove parameter</button>
          </div>
        </div>
      `;
    }

    renderStepCard(step, index) {
      const otherSteps = this.state.draft.steps.filter((candidate, candidateIndex) => candidateIndex !== index);
      const timeoutMinutes = minutesFromDuration(step.timeout);
      const schemaEditor = this.renderSchemaEditor(step.output_schema, { scope: 'step', stepIndex: index, heading: 'Step Structured Output' });
      return `
        <article class="behavior-step-card">
          <div class="behavior-step-card__head">
            <div>
              <div class="behavior-step-card__eyebrow">Step ${index + 1}</div>
              <h3 class="behavior-step-card__title">${escapeHtml(step.name || step.description || `Step ${index + 1}`)}</h3>
            </div>
            <div class="behavior-step-card__actions">
              <button class="behavior-icon-btn" type="button" data-action="move-step-up" data-step-index="${index}" title="Move up">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M7,15L12,10L17,15H7Z"/></svg>
              </button>
              <button class="behavior-icon-btn" type="button" data-action="move-step-down" data-step-index="${index}" title="Move down">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M7,10L12,15L17,10H7Z"/></svg>
              </button>
              <button class="behavior-icon-btn" type="button" data-action="remove-step" data-step-index="${index}" title="Remove step">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3M7,6H17V19H7M9,8V17H11V8M13,8V17H15V8Z"/></svg>
              </button>
            </div>
          </div>

          <div class="behavior-form-grid behavior-form-grid--step">
            <label class="behavior-field">
              <span class="behavior-field__label">Step Name</span>
              <input class="behavior-input" type="text" data-step-index="${index}" data-step-field="name" value="${escapeAttr(step.name)}" placeholder="Collect evidence">
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Step ID</span>
              <input class="behavior-input behavior-input--mono" type="text" data-step-index="${index}" data-step-field="id" value="${escapeAttr(step.id)}" placeholder="collect-evidence">
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Role</span>
              <select class="behavior-input" data-step-index="${index}" data-step-field="role">
                ${ROLE_OPTIONS.map((option) => `<option value="${option.value}" ${step.role === option.value ? 'selected' : ''}>${option.label}</option>`).join('')}
              </select>
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Pinned Agent <span class="behavior-field__optional">(optional)</span></span>
              <input class="behavior-input" type="text" data-step-index="${index}" data-step-field="agent_name" value="${escapeAttr(step.agent_name)}" placeholder="Leave blank to assign at launch">
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Priority</span>
              <input class="behavior-input behavior-input--mono" type="number" min="1" max="5" data-step-index="${index}" data-step-field="priority" value="${escapeAttr(step.priority)}">
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Timeout (minutes)</span>
              <input class="behavior-input behavior-input--mono" type="number" min="1" step="1" data-step-index="${index}" data-step-field="timeout_minutes" value="${escapeAttr(timeoutMinutes)}">
            </label>
          </div>

          <label class="behavior-field">
            <span class="behavior-field__label">Task Description</span>
            <textarea class="behavior-textarea behavior-textarea--compact" rows="2" data-step-index="${index}" data-step-field="description" placeholder="What this step should accomplish.">${escapeHtml(step.description)}</textarea>
          </label>

          <label class="behavior-field">
            <span class="behavior-field__label">Details</span>
            <textarea class="behavior-textarea behavior-textarea--compact" rows="2" data-step-index="${index}" data-step-field="details" placeholder="Optional extra instructions.">${escapeHtml(step.details)}</textarea>
          </label>

          <div class="behavior-field">
            <span class="behavior-field__label">Depends On</span>
            <div class="behavior-dependency-grid">
              ${otherSteps.length === 0 ? '<div class="behavior-field__hint">No other steps yet.</div>' : otherSteps.map((candidate) => `
                <label class="behavior-dependency-pill">
                  <input type="checkbox" data-step-index="${index}" data-step-dependency="${escapeAttr(candidate.id)}" ${safeArray(step.depends_on).includes(candidate.id) ? 'checked' : ''}>
                  <span>${escapeHtml(candidate.name || candidate.description || candidate.id)}</span>
                </label>
              `).join('')}
            </div>
          </div>

          <label class="behavior-field">
            <span class="behavior-field__label">Context JSON</span>
            <textarea class="behavior-textarea behavior-textarea--code" rows="5" data-step-index="${index}" data-step-field="context_text" placeholder='{"focus":"security"}'>${escapeHtml(step._contextText || '')}</textarea>
          </label>

          ${schemaEditor}
        </article>
      `;
    }

    renderSchemaEditor(schema, { scope, stepIndex, heading }) {
      if (!schema) {
        return `
          <div class="behavior-subsection behavior-subsection--schema">
            <div class="behavior-subsection__head">
              <div>
                <div class="behavior-subsection__title">${escapeHtml(heading)}</div>
                <div class="behavior-subsection__copy">Require this task to return valid JSON matching a schema.</div>
              </div>
              <button class="behavior-btn behavior-btn--secondary" type="button" data-action="${scope === 'template' ? 'enable-template-schema' : 'enable-step-schema'}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''}>Enable schema</button>
            </div>
          </div>
        `;
      }

      return `
        <div class="behavior-subsection behavior-subsection--schema">
          <div class="behavior-subsection__head">
            <div>
              <div class="behavior-subsection__title">${escapeHtml(heading)}</div>
              <div class="behavior-subsection__copy">These fields are enforced at runtime.</div>
            </div>
            <div class="behavior-subsection__actions">
              <button class="behavior-btn behavior-btn--secondary" type="button" data-action="${scope === 'template' ? 'add-template-schema-field' : 'add-step-schema-field'}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''}>Add field</button>
              <button class="behavior-link-btn" type="button" data-action="${scope === 'template' ? 'disable-template-schema' : 'disable-step-schema'}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''}>Disable schema</button>
            </div>
          </div>
          <div class="behavior-form-grid behavior-form-grid--schema">
            <label class="behavior-field">
              <span class="behavior-field__label">Schema Name</span>
              <input class="behavior-input" type="text" data-schema-scope="${scope}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''} data-schema-field="name" value="${escapeAttr(schema.name)}" placeholder="release_decision">
            </label>
            <label class="behavior-field behavior-field--toggle">
              <span class="behavior-field__label">Strict</span>
              <input type="checkbox" data-schema-scope="${scope}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''} data-schema-field="strict" ${schema.strict ? 'checked' : ''}>
            </label>
          </div>
          <label class="behavior-field">
            <span class="behavior-field__label">Schema Description</span>
            <textarea class="behavior-textarea behavior-textarea--compact" rows="2" data-schema-scope="${scope}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''} data-schema-field="description" placeholder="What the JSON object represents.">${escapeHtml(schema.description)}</textarea>
          </label>
          <div class="behavior-schema-field-list">
            ${safeArray(schema.fields).map((field, fieldIndex) => `
              <div class="behavior-schema-field-row">
                <div class="behavior-form-grid behavior-form-grid--schema-row">
                  <label class="behavior-field">
                    <span class="behavior-field__label">Field</span>
                    <input class="behavior-input behavior-input--mono" type="text" data-schema-scope="${scope}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''} data-field-index="${fieldIndex}" data-schema-item-field="name" value="${escapeAttr(field.name)}" placeholder="summary">
                  </label>
                  <label class="behavior-field">
                    <span class="behavior-field__label">Type</span>
                    <select class="behavior-input" data-schema-scope="${scope}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''} data-field-index="${fieldIndex}" data-schema-item-field="type">
                      ${OUTPUT_FIELD_TYPES.map((option) => `<option value="${option}" ${field.type === option ? 'selected' : ''}>${titleCase(option)}</option>`).join('')}
                    </select>
                  </label>
                  <label class="behavior-field behavior-field--toggle">
                    <span class="behavior-field__label">Required</span>
                    <input type="checkbox" data-schema-scope="${scope}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''} data-field-index="${fieldIndex}" data-schema-item-field="required" ${field.required ? 'checked' : ''}>
                  </label>
                </div>
                <label class="behavior-field">
                  <span class="behavior-field__label">Description</span>
                  <input class="behavior-input" type="text" data-schema-scope="${scope}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''} data-field-index="${fieldIndex}" data-schema-item-field="description" value="${escapeAttr(field.description)}" placeholder="Explain this field">
                </label>
                <div class="behavior-parameter-row__footer">
                  <button class="behavior-link-btn" type="button" data-action="${scope === 'template' ? 'remove-template-schema-field' : 'remove-step-schema-field'}" ${scope === 'step' ? `data-step-index="${stepIndex}"` : ''} data-field-index="${fieldIndex}">Remove field</button>
                </div>
              </div>
            `).join('')}
          </div>
        </div>
      `;
    }

    renderPreview() {
      const draft = normalizeTemplate(this.state.draft);
      const graphLayout = buildGraphLayout(draft.steps);
      const graphMarkup = graphLayout ? this.renderGraphSVG(draft.steps, graphLayout) : `
        <div class="behavior-graph-empty">
          <div class="behavior-graph-empty__title">Graph preview unavailable</div>
          <div class="behavior-graph-empty__copy">Resolve duplicate ids or circular dependencies to render the step graph.</div>
        </div>
      `;
      const isBuiltin = draft.source === 'builtin';
      const stepCount = safeArray(draft.steps).length;
      const parameterCount = safeArray(draft.parameters).filter((parameter) => String(parameter.name || '').trim()).length;
      const schema = serializeSchema(draft.output_schema);
      this.previewEl.innerHTML = `
        <section class="behavior-preview-card">
          <div class="behavior-preview-card__hero">
            <div>
              <div class="behavior-preview-card__eyebrow">Behavior Summary</div>
              <h2 class="behavior-preview-card__title">${escapeHtml(draft.name || 'New behavior')}</h2>
              <p class="behavior-preview-card__copy">${escapeHtml(draft.description || 'Describe the reusable orchestration this behavior should execute.')}</p>
            </div>
            <div class="behavior-preview-card__status ${this.state.isDirty ? 'is-dirty' : ''}">
              ${this.state.isDirty ? 'Unsaved draft' : (isBuiltin ? 'Built-in' : 'Saved')}
            </div>
          </div>

          <div class="behavior-preview-card__actions">
            <button class="behavior-btn behavior-btn--primary" type="button" data-preview-action="launch" ${this.state.isDirty ? 'disabled' : ''}>Add to workspace</button>
            <button class="behavior-btn behavior-btn--secondary" type="button" data-preview-action="save" ${isBuiltin ? 'disabled' : ''}>Save</button>
            <button class="behavior-btn behavior-btn--secondary" type="button" data-preview-action="duplicate">${isBuiltin ? 'Fork' : 'Duplicate'}</button>
            <button class="behavior-btn behavior-btn--secondary" type="button" data-preview-action="export">Export</button>
            <button class="behavior-btn behavior-btn--danger" type="button" data-preview-action="delete" ${draft.source !== 'custom' ? 'disabled' : ''}>Delete</button>
          </div>

          <div class="behavior-preview-stats">
            <div class="behavior-preview-stat">
              <span class="behavior-preview-stat__label">Steps</span>
              <strong>${stepCount}</strong>
            </div>
            <div class="behavior-preview-stat">
              <span class="behavior-preview-stat__label">Parameters</span>
              <strong>${parameterCount}</strong>
            </div>
            <div class="behavior-preview-stat">
              <span class="behavior-preview-stat__label">Mode</span>
              <strong>${escapeHtml(draft.orchestration_mode)}</strong>
            </div>
            <div class="behavior-preview-stat">
              <span class="behavior-preview-stat__label">Combine</span>
              <strong>${escapeHtml(draft.result_combination_mode)}</strong>
            </div>
          </div>

          <div class="behavior-preview-panel">
            <div class="behavior-preview-panel__head">
              <h3>Step Graph</h3>
              <span>${stepCount} node${stepCount === 1 ? '' : 's'}</span>
            </div>
            <div class="behavior-graph-frame">${graphMarkup}</div>
          </div>

          <div class="behavior-preview-panel">
            <div class="behavior-preview-panel__head">
              <h3>Structured Output</h3>
              <span>${schema ? `${schema.fields.length} field${schema.fields.length === 1 ? '' : 's'}` : 'Disabled'}</span>
            </div>
            ${schema ? `
              <div class="behavior-output-summary">
                ${schema.fields.map((field) => `
                  <div class="behavior-output-chip">
                    <span class="behavior-output-chip__name">${escapeHtml(field.name)}</span>
                    <span class="behavior-output-chip__meta">${escapeHtml(field.type)}${field.required ? ' · required' : ''}</span>
                  </div>
                `).join('')}
              </div>
            ` : '<div class="behavior-output-summary behavior-output-summary--empty">Parent output is free-form.</div>'}
          </div>

          <details class="behavior-json-inspector">
            <summary>Raw behavior JSON</summary>
            <pre>${escapeHtml(JSON.stringify(this.safePreviewPayload(), null, 2))}</pre>
          </details>
        </section>
      `;
    }

    renderGraphSVG(steps, graphLayout) {
      const positions = graphLayout.positions;
      const edges = safeArray(steps).flatMap((step) => {
        const target = positions[step.id];
        if (!target) {
          return [];
        }
        return safeArray(step.depends_on).map((dependencyId) => {
          const source = positions[dependencyId];
          if (!source) {
            return '';
          }
          const controlOffset = Math.max(60, (target.x - source.x) / 2);
          const path = `M ${source.x + 72} ${source.y} C ${source.x + 72 + controlOffset} ${source.y}, ${target.x - 72 - controlOffset} ${target.y}, ${target.x - 72} ${target.y}`;
          return `<path d="${path}" class="behavior-graph-edge"></path>`;
        });
      }).join('');

      const nodes = safeArray(steps).map((step) => {
        const position = positions[step.id];
        if (!position) {
          return '';
        }
        const accent = getRoleAccent(step.role);
        const label = truncate(step.name || step.description || step.id, 28);
        const sublabel = truncate(step.agent_name || step.role || 'general', 20);
        return `
          <g transform="translate(${position.x - 72}, ${position.y - 34})">
            <rect width="144" height="68" rx="18" fill="rgba(255,255,255,0.94)" stroke="${accent}" stroke-width="2.2"></rect>
            <rect width="144" height="10" rx="10" fill="${accent}" opacity="0.88"></rect>
            <text x="16" y="34" class="behavior-graph-label">${escapeHtml(label)}</text>
            <text x="16" y="52" class="behavior-graph-sublabel">${escapeHtml(sublabel)}</text>
          </g>
        `;
      }).join('');

      return `
        <svg class="behavior-graph-svg" viewBox="0 0 ${graphLayout.width} ${graphLayout.height}" preserveAspectRatio="xMidYMid meet">
          <defs>
            <filter id="behaviorNodeShadow" x="-20%" y="-20%" width="140%" height="140%">
              <feDropShadow dx="0" dy="14" stdDeviation="12" flood-color="rgba(15, 23, 42, 0.18)"></feDropShadow>
            </filter>
          </defs>
          <rect width="${graphLayout.width}" height="${graphLayout.height}" fill="transparent"></rect>
          <g fill="none" stroke-linecap="round">${edges}</g>
          <g filter="url(#behaviorNodeShadow)">${nodes}</g>
        </svg>
      `;
    }

    safePreviewPayload() {
      try {
        return this.buildTemplatePayload();
      } catch (_error) {
        return normalizeTemplate(this.state.draft);
      }
    }

    renderLaunchModal() {
      if (!this.launchModalBodyEl) {
        return;
      }
      const draft = normalizeTemplate(this.state.draft);
      const launchState = this.state.launchState || this.buildLaunchState();
      const workspaceOptions = this.state.workspaces.map((workspace) => `
        <option value="${escapeAttr(workspace.id)}" ${launchState.workspaceId === workspace.id ? 'selected' : ''}>${escapeHtml(workspace.name)}</option>
      `).join('');
      const parameterFields = safeArray(draft.parameters)
        .filter((parameter) => String(parameter.name || '').trim() !== '')
        .map((parameter) => this.renderLaunchParameterField(parameter, launchState))
        .join('');
      const agentOptions = this.state.launchAgents.map((agentName) => `
        <option value="${escapeAttr(agentName)}">${escapeHtml(agentName)}</option>
      `).join('');
      const agentAssignments = safeArray(draft.steps).map((step) => `
        <label class="behavior-field">
          <span class="behavior-field__label">${escapeHtml(step.name || step.id)}</span>
          <select class="behavior-input" data-launch-field="assignment" data-step-id="${escapeAttr(step.id)}">
            <option value="">Use behavior default${step.agent_name ? ` (${escapeHtml(step.agent_name)})` : ''}</option>
            ${this.state.launchAgents.map((agentName) => `
              <option value="${escapeAttr(agentName)}" ${launchState.agentAssignments[step.id] === agentName ? 'selected' : ''}>${escapeHtml(agentName)}</option>
            `).join('')}
          </select>
        </label>
      `).join('');

      this.launchModalBodyEl.innerHTML = `
        <div class="behavior-launch-grid">
          <div class="behavior-launch-panel">
            <div class="behavior-subsection__head">
              <div>
                <div class="behavior-subsection__title">Workspace</div>
                <div class="behavior-subsection__copy">Instantiate this behavior into an existing workspace.</div>
              </div>
            </div>
            <label class="behavior-field">
              <span class="behavior-field__label">Target Workspace</span>
              <select class="behavior-input" data-launch-field="workspaceId">
                <option value="">Select a workspace</option>
                ${workspaceOptions}
              </select>
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Fallback Agent</span>
              <select class="behavior-input" data-launch-field="fallbackAgent" ${this.state.launchAgents.length === 0 ? 'disabled' : ''}>
                <option value="">Leave unassigned unless a step pins an agent</option>
                ${this.state.launchAgents.map((agentName) => `<option value="${escapeAttr(agentName)}" ${launchState.fallbackAgent === agentName ? 'selected' : ''}>${escapeHtml(agentName)}</option>`).join('')}
              </select>
            </label>
            <div class="behavior-launch-note">
              ${this.state.launchWorkspaceName
                ? `Workspace loaded: <strong>${escapeHtml(this.state.launchWorkspaceName)}</strong>`
                : 'Select a workspace to load available agents.'}
            </div>
          </div>

          <div class="behavior-launch-panel">
            <div class="behavior-subsection__head">
              <div>
                <div class="behavior-subsection__title">Runtime Overrides</div>
                <div class="behavior-subsection__copy">Leave blank to use the behavior defaults.</div>
              </div>
            </div>
            <label class="behavior-field">
              <span class="behavior-field__label">Orchestration Override</span>
              <select class="behavior-input" data-launch-field="orchestrationMode">
                <option value="">Use behavior default (${escapeHtml(draft.orchestration_mode)})</option>
                ${ORCHESTRATION_OPTIONS.map((option) => `<option value="${option.value}" ${launchState.orchestrationMode === option.value ? 'selected' : ''}>${option.label}</option>`).join('')}
              </select>
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Combination Override</span>
              <select class="behavior-input" data-launch-field="resultCombinationMode">
                <option value="">Use behavior default (${escapeHtml(draft.result_combination_mode)})</option>
                ${COMBINATION_OPTIONS.map((option) => `<option value="${option.value}" ${launchState.resultCombinationMode === option.value ? 'selected' : ''}>${option.label}</option>`).join('')}
              </select>
            </label>
            <label class="behavior-field">
              <span class="behavior-field__label">Combination Instruction</span>
              <textarea class="behavior-textarea behavior-textarea--compact" rows="2" data-launch-field="combinationInstruction" placeholder="Optional override for the final aggregation note.">${escapeHtml(launchState.combinationInstruction || '')}</textarea>
            </label>
          </div>
        </div>

        <div class="behavior-launch-panel">
          <div class="behavior-subsection__head">
            <div>
              <div class="behavior-subsection__title">Parameters</div>
              <div class="behavior-subsection__copy">These values are rendered into the behavior template before tasks are created.</div>
            </div>
          </div>
          ${parameterFields || '<div class="behavior-launch-note">This behavior has no parameters.</div>'}
        </div>

        <div class="behavior-launch-panel">
          <div class="behavior-subsection__head">
            <div>
              <div class="behavior-subsection__title">Step Assignments</div>
              <div class="behavior-subsection__copy">Override agents per step when the behavior should use specific workspace members.</div>
            </div>
          </div>
          <div class="behavior-form-grid behavior-form-grid--launch-steps">
            ${agentAssignments}
          </div>
        </div>
      `;
    }

    renderLaunchParameterField(parameter, launchState) {
      const value = launchState.parameterInputs[parameter.name] ?? '';
      switch (parameter.type) {
        case 'boolean':
          return `
            <label class="behavior-field">
              <span class="behavior-field__label">${escapeHtml(parameter.name)}</span>
              <select class="behavior-input" data-launch-field="parameter" data-parameter-name="${escapeAttr(parameter.name)}">
                <option value="" ${value === '' ? 'selected' : ''}>No value</option>
                <option value="true" ${value === 'true' ? 'selected' : ''}>True</option>
                <option value="false" ${value === 'false' ? 'selected' : ''}>False</option>
              </select>
              ${parameter.description ? `<span class="behavior-field__hint">${escapeHtml(parameter.description)}</span>` : ''}
            </label>
          `;
        case 'array':
        case 'object':
          return `
            <label class="behavior-field">
              <span class="behavior-field__label">${escapeHtml(parameter.name)}</span>
              <textarea class="behavior-textarea behavior-textarea--code" rows="4" data-launch-field="parameter" data-parameter-name="${escapeAttr(parameter.name)}" placeholder="${parameter.type === 'array' ? '[\"value\"]' : '{"key":"value"}'}">${escapeHtml(value)}</textarea>
              ${parameter.description ? `<span class="behavior-field__hint">${escapeHtml(parameter.description)}</span>` : ''}
            </label>
          `;
        case 'number':
          return `
            <label class="behavior-field">
              <span class="behavior-field__label">${escapeHtml(parameter.name)}</span>
              <input class="behavior-input behavior-input--mono" type="number" data-launch-field="parameter" data-parameter-name="${escapeAttr(parameter.name)}" value="${escapeAttr(value)}">
              ${parameter.description ? `<span class="behavior-field__hint">${escapeHtml(parameter.description)}</span>` : ''}
            </label>
          `;
        default:
          return `
            <label class="behavior-field">
              <span class="behavior-field__label">${escapeHtml(parameter.name)}</span>
              <input class="behavior-input" type="text" data-launch-field="parameter" data-parameter-name="${escapeAttr(parameter.name)}" value="${escapeAttr(value)}">
              ${parameter.description ? `<span class="behavior-field__hint">${escapeHtml(parameter.description)}</span>` : ''}
            </label>
          `;
      }
    }

    async handleLaunchInput(event) {
      const target = event.target;
      if (!(target instanceof HTMLElement)) {
        return;
      }
      if (!this.state.launchState) {
        this.state.launchState = this.buildLaunchState();
      }
      const field = target.dataset.launchField;
      if (!field) {
        return;
      }

      if (field === 'workspaceId') {
        this.state.launchState.workspaceId = target.value;
        await this.loadWorkspaceAgents(target.value);
        this.renderLaunchModal();
        return;
      }
      if (field === 'fallbackAgent') {
        this.state.launchState.fallbackAgent = target.value;
        return;
      }
      if (field === 'orchestrationMode') {
        this.state.launchState.orchestrationMode = target.value;
        return;
      }
      if (field === 'resultCombinationMode') {
        this.state.launchState.resultCombinationMode = target.value;
        return;
      }
      if (field === 'combinationInstruction') {
        this.state.launchState.combinationInstruction = target.value;
        return;
      }
      if (field === 'parameter') {
        this.state.launchState.parameterInputs[target.dataset.parameterName] = target.value;
        return;
      }
      if (field === 'assignment') {
        this.state.launchState.agentAssignments[target.dataset.stepId] = target.value;
      }
    }

    async instantiateBehavior() {
      if (!this.state.launchState) {
        return;
      }
      const draft = normalizeTemplate(this.state.draft);
      const workspaceId = String(this.state.launchState.workspaceId || '').trim();
      if (!workspaceId) {
        notify('Choose a target workspace first.', 'warning');
        return;
      }

      const parameters = {};
      try {
        safeArray(draft.parameters)
          .filter((parameter) => String(parameter.name || '').trim() !== '')
          .forEach((parameter) => {
            const rawValue = this.state.launchState.parameterInputs[parameter.name];
            if (String(rawValue || '').trim() === '') {
              return;
            }
            parameters[parameter.name] = parseValueByType(rawValue, parameter.type, `Parameter ${parameter.name}`);
          });
      } catch (error) {
        notify(error.message || 'One or more parameter values are invalid.', 'error');
        return;
      }

      const agentAssignments = {};
      Object.entries(safeObject(this.state.launchState.agentAssignments)).forEach(([stepId, agentName]) => {
        if (String(agentName || '').trim() !== '') {
          agentAssignments[stepId] = String(agentName).trim();
        }
      });

      try {
        this.launchModalSubmitEl.disabled = true;
        this.launchModalSubmitEl.innerHTML = '<span class="spinner-border spinner-border-sm me-2"></span>Adding…';
        const response = await fetch('/api/orchestration/templates/instantiate', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            template_id: draft.id,
            workspace_id: workspaceId,
            agent_name: this.state.launchState.fallbackAgent || '',
            agent_assignments: agentAssignments,
            parameters,
            orchestration_mode: this.state.launchState.orchestrationMode || '',
            result_combination_mode: this.state.launchState.resultCombinationMode || '',
            combination_instruction: String(this.state.launchState.combinationInstruction || '').trim()
          })
        });
        if (!response.ok) {
          throw new Error(await response.text());
        }
        const payload = await response.json();
        notify(`Behavior added to ${this.state.launchWorkspaceName || 'workspace'}.`, 'success');
        this.launchModal?.hide();
        if (payload.parent_task?.id) {
          window.location.href = `/workspaces/${encodeURIComponent(workspaceId)}`;
          return;
        }
      } catch (error) {
        console.error('Failed to instantiate behavior', error);
        notify(error.message || 'Failed to add behavior to workspace.', 'error');
      } finally {
        this.launchModalSubmitEl.disabled = false;
        this.launchModalSubmitEl.textContent = 'Add to Workspace';
      }
    }
  }

  document.addEventListener('DOMContentLoaded', async () => {
    try {
      const studio = new BehaviorStudio(root);
      window.behaviorStudio = studio;
      await studio.init();
    } catch (error) {
      console.error('Failed to initialize behavior studio', error);
      notify(error.message || 'Failed to load the behavior studio.', 'error');
    }
  });
})();
