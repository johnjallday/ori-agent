// Personalize Module
// Handles the personalization page: interests, tools, work style, and profile saving

(function () {
  'use strict';

  // --- Data ---

  var INTERESTS = [
    'coding',
    'web-dev',
    'mobile-dev',
    'data-science',
    'machine-learning',
    'devops',
    'cloud',
    'security',
    'databases',
    'automation',
    'design',
    'writing',
    'api-development',
    'game-dev',
    'embedded-systems'
  ];

  var TOOLS = [
    'Go',
    'Python',
    'JavaScript',
    'TypeScript',
    'Rust',
    'Java',
    'C++',
    'Ruby',
    'Docker',
    'Kubernetes',
    'PostgreSQL',
    'MySQL',
    'MongoDB',
    'Redis',
    'React',
    'Vue',
    'Node.js',
    'Git',
    'VS Code',
    'Vim',
    'Terraform',
    'AWS',
    'GCP',
    'Azure'
  ];

  var WORK_STYLES = [
    {
      value: 'detailed',
      label: 'Detailed',
      description: 'Thorough explanations with examples and context'
    },
    {
      value: 'concise',
      label: 'Concise',
      description: 'Brief, to-the-point responses focused on essentials'
    },
    {
      value: 'formal',
      label: 'Formal',
      description: 'Professional tone with structured responses'
    },
    { value: 'casual', label: 'Casual', description: 'Friendly, conversational style' }
  ];

  // --- State ---

  var selectedInterests = [];
  var selectedTools = [];
  var selectedWorkStyle = '';
  var existingProfile = null;
  var isAlreadyPersonalized = false;

  // --- Helpers ---

  function escapeHtml(text) {
    var div = document.createElement('div');
    div.textContent = text || '';
    return div.innerHTML;
  }

  function formatCategory(category) {
    var categories = {
      developer: 'Software Developer',
      devops: 'DevOps Engineer',
      designer: 'Designer',
      data_scientist: 'Data Scientist',
      writer: 'Writer / Content Creator',
      project_manager: 'Project Manager',
      general: 'General User'
    };
    return categories[category] || category || 'General User';
  }

  function showToast(message) {
    var toastBody = document.getElementById('personalizeToastBody');
    var toastEl = document.getElementById('personalizeToast');
    if (!toastBody || !toastEl) return;

    toastBody.textContent = message;

    // Use Bootstrap Toast if available, otherwise manual show
    if (typeof bootstrap !== 'undefined' && bootstrap.Toast) {
      var toast = new bootstrap.Toast(toastEl, { delay: 4000 });
      toast.show();
    } else {
      toastEl.classList.add('show');
      setTimeout(function () {
        toastEl.classList.remove('show');
      }, 4000);
    }
  }

  // --- Rendering ---

  function renderInterestsGrid() {
    var container = document.getElementById('interestsGrid');
    if (!container) return;

    var html = '';
    for (var i = 0; i < INTERESTS.length; i++) {
      var interest = INTERESTS[i];
      var isSelected = selectedInterests.indexOf(interest) !== -1;
      var label = interest.replace(/-/g, ' ').replace(/\b\w/g, function (c) {
        return c.toUpperCase();
      });
      html +=
        '<button type="button" class="personalize-tag' +
        (isSelected ? ' active' : '') +
        '" data-interest="' +
        escapeHtml(interest) +
        '">' +
        escapeHtml(label) +
        '</button>';
    }
    container.innerHTML = html;

    // Event listeners
    var buttons = container.querySelectorAll('[data-interest]');
    for (var j = 0; j < buttons.length; j++) {
      buttons[j].addEventListener('click', function () {
        var val = this.getAttribute('data-interest');
        var idx = selectedInterests.indexOf(val);
        if (idx === -1) {
          selectedInterests.push(val);
          this.classList.add('active');
        } else {
          selectedInterests.splice(idx, 1);
          this.classList.remove('active');
        }
      });
    }
  }

  function renderToolsGrid() {
    var container = document.getElementById('toolsGrid');
    if (!container) return;

    var allTools = TOOLS.slice();
    // Add any custom tools from profile that aren't in defaults
    for (var k = 0; k < selectedTools.length; k++) {
      if (allTools.indexOf(selectedTools[k]) === -1) {
        allTools.push(selectedTools[k]);
      }
    }

    var html = '';
    for (var i = 0; i < allTools.length; i++) {
      var tool = allTools[i];
      var isSelected = selectedTools.indexOf(tool) !== -1;
      html +=
        '<button type="button" class="personalize-tag' +
        (isSelected ? ' active' : '') +
        '" data-tool="' +
        escapeHtml(tool) +
        '">' +
        escapeHtml(tool) +
        '</button>';
    }
    container.innerHTML = html;

    // Event listeners
    var buttons = container.querySelectorAll('[data-tool]');
    for (var j = 0; j < buttons.length; j++) {
      buttons[j].addEventListener('click', function () {
        var val = this.getAttribute('data-tool');
        var idx = selectedTools.indexOf(val);
        if (idx === -1) {
          selectedTools.push(val);
          this.classList.add('active');
        } else {
          selectedTools.splice(idx, 1);
          this.classList.remove('active');
        }
      });
    }
  }

  function renderWorkStyleGrid() {
    var container = document.getElementById('workStyleGrid');
    if (!container) return;

    var html = '';
    for (var i = 0; i < WORK_STYLES.length; i++) {
      var ws = WORK_STYLES[i];
      var isSelected = selectedWorkStyle === ws.value;
      html +=
        '<div class="col-6 col-md-3">' +
        '  <div class="personalize-card' +
        (isSelected ? ' active' : '') +
        '" data-workstyle="' +
        escapeHtml(ws.value) +
        '" style="cursor: pointer; padding: 1rem; border: 2px solid ' +
        (isSelected ? 'var(--primary-color)' : 'var(--border-color)') +
        '; border-radius: var(--radius-md); background: var(--bg-secondary); text-align: center; transition: border-color 0.2s;">' +
        '    <div style="font-weight: 600; color: var(--text-primary); margin-bottom: 0.25rem;">' +
        escapeHtml(ws.label) +
        '</div>' +
        '    <div style="font-size: 0.8rem; color: var(--text-secondary);">' +
        escapeHtml(ws.description) +
        '</div>' +
        '  </div>' +
        '</div>';
    }
    container.innerHTML = html;

    // Event listeners
    var cards = container.querySelectorAll('[data-workstyle]');
    for (var j = 0; j < cards.length; j++) {
      cards[j].addEventListener('click', function () {
        selectedWorkStyle = this.getAttribute('data-workstyle');
        // Update visuals
        var allCards = container.querySelectorAll('[data-workstyle]');
        for (var k = 0; k < allCards.length; k++) {
          allCards[k].style.borderColor = 'var(--border-color)';
          allCards[k].classList.remove('active');
        }
        this.style.borderColor = 'var(--primary-color)';
        this.classList.add('active');
      });
    }
  }

  function renderProfileSummary(profile) {
    var summaryCard = document.getElementById('personalizeProfileSummary');
    if (!summaryCard || !profile) return;

    var badge = document.getElementById('personalizeProfileBadge');
    var text = document.getElementById('personalizeProfileText');

    if (badge) badge.textContent = formatCategory(profile.primary_category);
    if (text) text.textContent = profile.summary || '';

    if (profile.primary_category || profile.summary) {
      summaryCard.classList.remove('d-none');
    }
  }

  function updateSaveButton() {
    var btnText = document.getElementById('saveBtnText');
    if (!btnText) return;

    if (isAlreadyPersonalized) {
      btnText.textContent = 'Update Profile';
    } else {
      btnText.innerHTML = 'Save &amp; Earn 25 XP';
    }
  }

  // --- API Actions ---

  function loadProfile() {
    if (typeof API === 'undefined' || typeof API.get !== 'function') return;

    API.get('/api/onboarding/user-profile')
      .then(function (data) {
        existingProfile = data && data.profile;
        if (!existingProfile) return;

        // Pre-populate from existing profile
        if (existingProfile.interests && existingProfile.interests.length > 0) {
          selectedInterests = existingProfile.interests.slice();
        }
        if (existingProfile.preferred_tools && existingProfile.preferred_tools.length > 0) {
          selectedTools = existingProfile.preferred_tools.slice();
        }
        if (existingProfile.work_style) {
          selectedWorkStyle = existingProfile.work_style;
        }
        if (existingProfile.description) {
          var desc = document.getElementById('personalizeDescription');
          if (desc) desc.value = existingProfile.description;
        }

        // Check if already personalized
        if (
          existingProfile.personalized_at &&
          existingProfile.personalized_at !== '0001-01-01T00:00:00Z'
        ) {
          isAlreadyPersonalized = true;
        }

        renderProfileSummary(existingProfile);
        renderInterestsGrid();
        renderToolsGrid();
        renderWorkStyleGrid();
        updateSaveButton();
      })
      .catch(function () {
        // No profile yet, render defaults
        renderInterestsGrid();
        renderToolsGrid();
        renderWorkStyleGrid();
        updateSaveButton();
      });
  }

  function detectApps() {
    var btn = document.getElementById('detectAppsBtn');
    if (!btn) return;

    btn.disabled = true;
    btn.innerHTML =
      '<span class="spinner-border spinner-border-sm me-2" role="status"></span>' + 'Detecting...';

    fetch('/api/onboarding/detect', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' }
    })
      .then(function (resp) {
        return resp.json();
      })
      .then(function (data) {
        var apps = (data && data.apps) || [];
        var listEl = document.getElementById('detectedAppsList');
        var containerEl = document.getElementById('detectedAppsContainer');
        if (listEl && containerEl) {
          if (apps.length > 0) {
            var html = '';
            for (var i = 0; i < apps.length; i++) {
              html +=
                '<span class="badge" style="background: var(--bg-tertiary); color: var(--text-secondary); font-weight: normal; font-size: 0.75rem;">' +
                escapeHtml(apps[i].name || apps[i]) +
                '</span>';
            }
            containerEl.innerHTML = html;
            listEl.classList.remove('d-none');
          } else {
            containerEl.innerHTML =
              '<span style="color: var(--text-muted); font-size: 0.85rem;">No apps detected.</span>';
            listEl.classList.remove('d-none');
          }
        }
      })
      .catch(function () {
        showToast('Failed to detect apps. Please try again.');
      })
      .finally(function () {
        btn.disabled = false;
        btn.innerHTML =
          '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">' +
          '<path d="M9.5,3A6.5,6.5 0 0,1 16,9.5C16,11.11 15.41,12.59 14.44,13.73L14.71,14H15.5L20.5,19L19,20.5L14,15.5V14.71L13.73,14.44C12.59,15.41 11.11,16 9.5,16A6.5,6.5 0 0,1 3,9.5A6.5,6.5 0 0,1 9.5,3M9.5,5C7,5 5,7 5,9.5C5,12 7,14 9.5,14C12,14 14,12 14,9.5C14,7 12,5 9.5,5Z"/>' +
          '</svg>' +
          'Scan recent apps';
      });
  }

  function savePersonalization() {
    var btn = document.getElementById('savePersonalizeBtn');
    if (!btn) return;

    var description = '';
    var descEl = document.getElementById('personalizeDescription');
    if (descEl) description = descEl.value.trim();

    btn.disabled = true;
    btn.innerHTML =
      '<span class="spinner-border spinner-border-sm me-2" role="status"></span>' + 'Saving...';

    fetch('/api/onboarding/personalize', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        interests: selectedInterests,
        preferred_tools: selectedTools,
        work_style: selectedWorkStyle,
        description: description
      })
    })
      .then(function (resp) {
        if (!resp.ok) throw new Error('Failed to save');
        return resp.json();
      })
      .then(function (data) {
        if (data.success) {
          var msg = 'Profile saved successfully!';
          if (data.xp_awarded > 0) {
            msg += ' You earned ' + data.xp_awarded + ' XP!';
          }
          showToast(msg);

          // Update state
          isAlreadyPersonalized = true;
          existingProfile = data.profile;
          updateSaveButton();

          var statusText = document.getElementById('saveStatusText');
          if (statusText) {
            statusText.textContent = 'Last saved just now';
          }
        }
      })
      .catch(function () {
        showToast('Failed to save personalization. Please try again.');
      })
      .finally(function () {
        btn.disabled = false;
        var btnText = document.getElementById('saveBtnText');
        if (btnText) {
          if (isAlreadyPersonalized) {
            btnText.textContent = 'Update Profile';
          } else {
            btnText.innerHTML = 'Save &amp; Earn 25 XP';
          }
        }
        btn.innerHTML =
          '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">' +
          '<path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>' +
          '</svg>' +
          '<span id="saveBtnText">' +
          (isAlreadyPersonalized ? 'Update Profile' : 'Save &amp; Earn 25 XP') +
          '</span>';
      });
  }

  function addCustomTool() {
    var input = document.getElementById('customToolInput');
    if (!input) return;

    var tool = input.value.trim();
    if (!tool) return;

    if (selectedTools.indexOf(tool) === -1) {
      selectedTools.push(tool);
      renderToolsGrid();
    }
    input.value = '';
  }

  // --- Styles ---

  function injectStyles() {
    var style = document.createElement('style');
    style.textContent =
      '.personalize-tag {' +
      '  display: inline-flex; align-items: center; padding: 0.4rem 0.85rem;' +
      '  border: 1px solid var(--border-color); border-radius: 20px;' +
      '  background: var(--bg-secondary); color: var(--text-primary);' +
      '  font-size: 0.85rem; cursor: pointer; transition: all 0.2s;' +
      '  user-select: none;' +
      '}' +
      '.personalize-tag:hover {' +
      '  border-color: var(--primary-color); background: var(--bg-tertiary);' +
      '}' +
      '.personalize-tag.active {' +
      '  background: var(--primary-color); color: white; border-color: var(--primary-color);' +
      '}';
    document.head.appendChild(style);
  }

  // --- Init ---

  function init() {
    injectStyles();

    // Render defaults first (will be updated when profile loads)
    renderInterestsGrid();
    renderToolsGrid();
    renderWorkStyleGrid();

    // Load existing profile
    loadProfile();

    // Event listeners
    var detectBtn = document.getElementById('detectAppsBtn');
    if (detectBtn) detectBtn.addEventListener('click', detectApps);

    var saveBtn = document.getElementById('savePersonalizeBtn');
    if (saveBtn) saveBtn.addEventListener('click', savePersonalization);

    var addToolBtn = document.getElementById('addCustomToolBtn');
    if (addToolBtn) addToolBtn.addEventListener('click', addCustomTool);

    var customInput = document.getElementById('customToolInput');
    if (customInput) {
      customInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          addCustomTool();
        }
      });
    }
  }

  document.addEventListener('DOMContentLoaded', init);
})();
