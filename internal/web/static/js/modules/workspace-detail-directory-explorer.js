/**
 * Workspace Directory Explorer
 *
 * Self-contained UI for browsing files inside a workspace directory:
 * tree view, breadcrumbs, search, sort, file preview with caching, and
 * persisted expanded/selected state per directory.
 *
 * Extracted from workspace-detail.js. Instantiated by WorkspaceDetailPage,
 * which provides workspaceId, escapeHtml, formatFileSize, the directories
 * list, and DOM element refs through the host parameter.
 *
 * @module workspace-detail-directory-explorer
 */

function formatDate(dateString) {
  if (!dateString) return '';
  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now - date;
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  return date.toLocaleDateString();
}

export class WorkspaceDirectoryExplorer {
  constructor(host) {
    this.host = host;
    this.directory = null;
    this.source = 'reference';
    this.files = [];
    this.treeRoot = null;
    this.nodeIndex = new Map();
    this.expandedPaths = new Set();
    this.selectedPath = '';
    this.selectedType = '';
    this.searchQuery = '';
    this.sortDirection = 'asc';
    this.fileCache = new Map();
    this.previewCache = new Map();
    this.previewAbortController = null;
    this.loadToken = 0;
  }

  bindEvents() {
    const elements = this.host.elements;
    elements.directoryExplorerRefreshBtn?.addEventListener('click', () =>
      this.loadFiles({ force: true })
    );

    elements.directoryExplorerSortBtn?.addEventListener('click', () => {
      this.sortDirection = this.sortDirection === 'asc' ? 'desc' : 'asc';
      this.render();
    });

    elements.directoryExplorerSearch?.addEventListener('input', event => {
      this.searchQuery = (event.target.value || '').trim();
      this.render();
    });

    elements.directoryExplorerTree?.addEventListener('click', event => {
      const toggleButton = event.target.closest('[data-action="toggle-folder"]');
      if (toggleButton) {
        const path = this.decodeDataPath(toggleButton.dataset.path);
        this.toggleNode(path);
        return;
      }

      const nodeButton = event.target.closest('[data-action="select-node"]');
      if (nodeButton) {
        const path = this.decodeDataPath(nodeButton.dataset.path);
        const type = nodeButton.dataset.type || 'file';
        this.selectNode(path, type, { autoExpand: true });
      }
    });

    elements.directoryExplorerTree?.addEventListener('keydown', event => {
      const nodeButton = event.target.closest('[data-action="select-node"]');
      if (!nodeButton) return;
      const isVerticalArrow = event.key === 'ArrowUp' || event.key === 'ArrowDown';
      const isHorizontalArrow = event.key === 'ArrowLeft' || event.key === 'ArrowRight';
      if (!isVerticalArrow && !isHorizontalArrow) return;

      if (isVerticalArrow) {
        event.preventDefault();
        const buttons = Array.from(
          elements.directoryExplorerTree.querySelectorAll('[data-action="select-node"]')
        );
        const currentIndex = buttons.indexOf(nodeButton);
        if (currentIndex === -1) return;
        const nextIndex = event.key === 'ArrowDown'
          ? Math.min(currentIndex + 1, buttons.length - 1)
          : Math.max(currentIndex - 1, 0);
        if (nextIndex === currentIndex) return;
        const target = buttons[nextIndex];
        const targetPath = this.decodeDataPath(target.dataset.path);
        const targetType = target.dataset.type || 'file';
        // Focus the target before triggering the re-render so render()'s
        // focus-restore logic captures and re-applies the new path, not the old.
        target.focus({ preventScroll: false });
        this.selectNode(targetPath, targetType);
        return;
      }

      const path = this.decodeDataPath(nodeButton.dataset.path);
      const type = nodeButton.dataset.type || 'file';
      if (type !== 'dir') return;

      if (event.key === 'ArrowRight') {
        event.preventDefault();
        this.expandNode(path);
      } else if (event.key === 'ArrowLeft') {
        event.preventDefault();
        this.collapseNode(path);
      }
    });

    elements.directoryExplorerPreview?.addEventListener('click', event => {
      const entryButton = event.target.closest('[data-action="select-node"]');
      if (!entryButton) return;
      const path = this.decodeDataPath(entryButton.dataset.path);
      const type = entryButton.dataset.type || 'file';
      this.selectNode(path, type, { autoExpand: true });
    });

    elements.directoryExplorerBreadcrumb?.addEventListener('click', event => {
      const crumb = event.target.closest('[data-action="breadcrumb"]');
      if (!crumb) return;
      const path = this.decodeDataPath(crumb.dataset.path);
      this.selectNode(path, 'dir', { autoExpand: true });
    });

    elements.directoryExplorerModal?.addEventListener('hidden.bs.modal', () => {
      this.abortPreviewRequest();
      this.searchQuery = '';
      if (elements.directoryExplorerSearch) {
        elements.directoryExplorerSearch.value = '';
      }
    });
  }

  async open(directoryId, source = 'reference') {
    if (!directoryId) return;

    const normalizedSource = source === 'attachment' ? 'attachment' : 'reference';
    const directory = this.host.directories.find(entry => entry.id === directoryId);
    if (!directory) {
      if (window.Toast) window.Toast.error('Directory not found');
      return;
    }

    const modal = this.getModalInstance();
    if (!modal) {
      if (window.Toast) window.Toast.error('Directory explorer is unavailable');
      return;
    }

    this.abortPreviewRequest();
    this.directory = directory;
    this.source = normalizedSource;
    this.searchQuery = '';
    this.files = [];
    this.treeRoot = null;
    this.nodeIndex = new Map();
    this.selectedType = '';
    this.previewCache = new Map();

    this.loadPersistedState(directory.id);

    const elements = this.host.elements;
    if (elements.directoryExplorerSearch) {
      elements.directoryExplorerSearch.value = '';
    }
    if (elements.directoryExplorerTitle) {
      elements.directoryExplorerTitle.textContent =
        directory.name || directory.path || 'Directory Explorer';
    }
    if (elements.directoryExplorerSubtitle) {
      elements.directoryExplorerSubtitle.textContent = directory.path || '';
    }

    modal.show();
    this.renderLoading();
    await this.loadFiles();
  }

  getModalInstance() {
    const elements = this.host.elements;
    if (
      !elements.directoryExplorerModal ||
      typeof bootstrap === 'undefined' ||
      !bootstrap.Modal
    ) {
      return null;
    }

    return typeof bootstrap.Modal.getOrCreateInstance === 'function'
      ? bootstrap.Modal.getOrCreateInstance(elements.directoryExplorerModal)
      : new bootstrap.Modal(elements.directoryExplorerModal);
  }

  renderLoading(message = 'Scanning directory...') {
    const elements = this.host.elements;
    if (elements.directoryExplorerTree) {
      elements.directoryExplorerTree.innerHTML = `<div class="workspace-directory-tree-empty">${this.host.escapeHtml(message)}</div>`;
    }
    if (elements.directoryExplorerPreview) {
      elements.directoryExplorerPreview.innerHTML =
        '<div class="workspace-directory-preview-empty">Select a file to preview.</div>';
    }
    if (elements.directoryExplorerSummary) {
      elements.directoryExplorerSummary.innerHTML = '';
    }
  }

  async loadFiles({ force = false } = {}) {
    const currentDirectory = this.directory;
    if (!currentDirectory || !currentDirectory.id) return;

    const loadToken = this.loadToken + 1;
    this.loadToken = loadToken;
    const cacheKey = currentDirectory.id;

    if (this.source !== 'reference') {
      const elements = this.host.elements;
      if (elements.directoryExplorerTree) {
        elements.directoryExplorerTree.innerHTML = `
          <div class="workspace-directory-tree-empty">
            Legacy directory attachments cannot be browsed yet. Re-add this folder with the folder picker to enable Finder view.
          </div>
        `;
      }
      if (elements.directoryExplorerPreview) {
        elements.directoryExplorerPreview.innerHTML =
          '<div class="workspace-directory-preview-empty">Preview unavailable.</div>';
      }
      return;
    }

    const cached = this.fileCache.get(cacheKey);
    if (!force && cached && Array.isArray(cached.files)) {
      this.files = cached.files;
      const { root, nodeIndex } = this.buildTree(cached.files);
      this.treeRoot = root;
      this.nodeIndex = nodeIndex;
      if (this.selectedPath && !nodeIndex.has(this.selectedPath)) {
        this.selectedPath = '';
        this.selectedType = '';
      }
      this.selectDefault();
      this.render();
      return;
    }

    this.renderLoading(force ? 'Refreshing directory...' : 'Scanning directory...');

    try {
      const endpoint = `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/directories/${encodeURIComponent(currentDirectory.id)}/files`;
      const response = await fetch(endpoint);
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Failed to load directory contents');
      }

      const payload = await response.json();
      if (loadToken !== this.loadToken) return;

      const normalizedFiles = (Array.isArray(payload.files) ? payload.files : [])
        .map(item => {
          const normalizedPath = this.normalizeRelativePath(
            item?.relative_path || item?.relativePath || ''
          );
          if (!normalizedPath) return null;

          return {
            name: item?.name || normalizedPath.split('/').pop() || normalizedPath,
            path: normalizedPath,
            isDir: Boolean(item?.is_dir),
            size: Number(item?.size) || 0,
            modTime: item?.mod_time || ''
          };
        })
        .filter(Boolean);

      this.files = normalizedFiles;
      this.fileCache.set(cacheKey, { files: normalizedFiles, loadedAt: Date.now() });

      const { root, nodeIndex } = this.buildTree(normalizedFiles);
      this.treeRoot = root;
      this.nodeIndex = nodeIndex;

      if (this.selectedPath && !nodeIndex.has(this.selectedPath)) {
        this.selectedPath = '';
        this.selectedType = '';
      }

      this.selectDefault();
      this.render();
    } catch (error) {
      if (loadToken !== this.loadToken) return;
      console.error('Failed to load directory explorer files:', error);
      const elements = this.host.elements;
      if (elements.directoryExplorerTree) {
        elements.directoryExplorerTree.innerHTML =
          '<div class="workspace-directory-tree-empty">Failed to load directory. Check the folder path and try again.</div>';
      }
      if (elements.directoryExplorerPreview) {
        elements.directoryExplorerPreview.innerHTML =
          '<div class="workspace-directory-preview-empty">No preview available.</div>';
      }
      if (window.Toast) window.Toast.error('Failed to load directory contents');
    }
  }

  selectDefault() {
    if (this.selectedPath && this.nodeIndex.has(this.selectedPath)) {
      if (!this.selectedType) {
        this.selectedType = this.nodeIndex.get(this.selectedPath)?.type || 'file';
      }
      this.ensureAncestorsExpanded(this.selectedPath, this.selectedType);
      return;
    }

    const firstFile = this.files.find(entry => !entry.isDir);
    if (firstFile) {
      this.selectedPath = firstFile.path;
      this.selectedType = 'file';
      this.ensureAncestorsExpanded(firstFile.path, 'file');
      this.persistState();
      return;
    }

    const firstDirectory = this.files.find(entry => entry.isDir);
    if (firstDirectory) {
      this.selectedPath = firstDirectory.path;
      this.selectedType = 'dir';
      this.expandedPaths.add(firstDirectory.path);
      this.ensureAncestorsExpanded(firstDirectory.path, 'dir');
      this.persistState();
    }
  }

  buildTree(files) {
    const rootName =
      this.directory?.name || this.directory?.path || 'Directory';
    const root = {
      type: 'dir',
      name: rootName,
      path: '',
      size: 0,
      modTime: '',
      children: []
    };
    const nodeIndex = new Map();
    nodeIndex.set('', root);

    const ensureDirectoryNode = path => {
      if (nodeIndex.has(path)) {
        return nodeIndex.get(path);
      }

      const normalizedPath = this.normalizeRelativePath(path);
      if (!normalizedPath) return root;

      const slashIndex = normalizedPath.lastIndexOf('/');
      const parentPath = slashIndex >= 0 ? normalizedPath.slice(0, slashIndex) : '';
      const parentNode = ensureDirectoryNode(parentPath);
      const name = normalizedPath.split('/').pop() || normalizedPath;

      const node = {
        type: 'dir',
        name,
        path: normalizedPath,
        size: 0,
        modTime: '',
        children: []
      };
      parentNode.children.push(node);
      nodeIndex.set(normalizedPath, node);
      return node;
    };

    files.forEach(entry => {
      const normalizedPath = this.normalizeRelativePath(entry.path);
      if (!normalizedPath) return;

      if (entry.isDir) {
        const dirNode = ensureDirectoryNode(normalizedPath);
        dirNode.modTime = entry.modTime || dirNode.modTime;
        return;
      }

      const slashIndex = normalizedPath.lastIndexOf('/');
      const parentPath = slashIndex >= 0 ? normalizedPath.slice(0, slashIndex) : '';
      const parentNode = ensureDirectoryNode(parentPath);

      if (nodeIndex.has(normalizedPath)) {
        return;
      }

      const fileNode = {
        type: 'file',
        name: entry.name || normalizedPath.split('/').pop() || normalizedPath,
        path: normalizedPath,
        size: entry.size || 0,
        modTime: entry.modTime || '',
        children: null
      };
      parentNode.children.push(fileNode);
      nodeIndex.set(normalizedPath, fileNode);
    });

    return { root, nodeIndex };
  }

  render() {
    const directory = this.directory;
    if (!directory) return;

    const elements = this.host.elements;

    // Capture which tree button (if any) currently has focus. innerHTML rewrite
    // below will destroy it; we restore focus to the equivalent new button so
    // arrow-key navigation keeps working across re-renders.
    const treeEl = elements.directoryExplorerTree;
    const focusedNode = treeEl && treeEl.contains(document.activeElement)
      ? document.activeElement.closest('[data-path]')
      : null;
    const focusedPath = focusedNode
      ? this.decodeDataPath(focusedNode.dataset.path || '')
      : null;

    if (elements.directoryExplorerTitle) {
      elements.directoryExplorerTitle.textContent =
        directory.name || directory.path || 'Directory Explorer';
    }
    if (elements.directoryExplorerSubtitle) {
      elements.directoryExplorerSubtitle.textContent = directory.path || '';
    }
    if (elements.directoryExplorerSortBtn) {
      const direction = this.sortDirection === 'desc' ? 'Z-A' : 'A-Z';
      elements.directoryExplorerSortBtn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <path d="M7,3H9V17H12L8,21L4,17H7V3M14,7V5H20V7H14M14,11V9H18V11H14M14,15V13H20V15H14M14,19V17H18V19H14Z"/>
        </svg>
        ${direction}
      `;
    }

    this.renderSummary();
    this.renderBreadcrumb();

    const query = this.searchQuery.toLowerCase();
    const hasSearch = query.length > 0;
    const sourceRoot = this.treeRoot;

    if (!sourceRoot || !Array.isArray(sourceRoot.children) || sourceRoot.children.length === 0) {
      if (elements.directoryExplorerTree) {
        elements.directoryExplorerTree.innerHTML =
          '<div class="workspace-directory-tree-empty">This directory is empty.</div>';
      }
      if (elements.directoryExplorerPreview) {
        elements.directoryExplorerPreview.innerHTML =
          '<div class="workspace-directory-preview-empty">No files to preview yet.</div>';
      }
      return;
    }

    const renderRoot = hasSearch ? this.filterTree(sourceRoot, query) : sourceRoot;
    const treeChildren = renderRoot?.children || [];

    if (elements.directoryExplorerTree) {
      if (treeChildren.length === 0) {
        elements.directoryExplorerTree.innerHTML =
          '<div class="workspace-directory-tree-empty">No matches for this search.</div>';
      } else {
        elements.directoryExplorerTree.innerHTML = `
          <div class="workspace-directory-tree-scroll">
            ${this.renderTreeChildren(treeChildren, 0, hasSearch)}
          </div>
        `;
      }

      // Restore focus to the equivalent button if the user was navigating the tree.
      // Falls through silently when the previously focused path no longer exists
      // (e.g. user collapsed an ancestor folder).
      if (focusedPath) {
        const restoreTarget = elements.directoryExplorerTree.querySelector(
          `[data-action="select-node"][data-path="${this.encodeDataPath(focusedPath)}"]`
        );
        if (restoreTarget) restoreTarget.focus({ preventScroll: true });
      }
    }

    this.renderPreview();
  }

  renderSummary() {
    const elements = this.host.elements;
    if (!elements.directoryExplorerSummary) return;

    const files = this.files || [];
    const folderCount = files.filter(entry => entry.isDir).length;
    const fileCount = files.length - folderCount;
    const selectedNode = this.nodeIndex.get(this.selectedPath);
    const selectedLabel = selectedNode
      ? `${selectedNode.type === 'dir' ? 'Folder' : 'File'} selected`
      : 'No selection';

    elements.directoryExplorerSummary.innerHTML = `
      <span class="workspace-directory-pill">${fileCount} file${fileCount === 1 ? '' : 's'}</span>
      <span class="workspace-directory-pill">${folderCount} folder${folderCount === 1 ? '' : 's'}</span>
      <span class="workspace-directory-pill is-muted">${this.host.escapeHtml(selectedLabel)}</span>
    `;
  }

  renderBreadcrumb() {
    const elements = this.host.elements;
    if (!elements.directoryExplorerBreadcrumb) return;

    const directory = this.directory;
    if (!directory) {
      elements.directoryExplorerBreadcrumb.innerHTML = '';
      return;
    }

    const selectedPath = this.selectedPath || '';
    const selectedType = this.selectedType || '';
    const segments = selectedPath ? selectedPath.split('/') : [];
    const crumbs = [
      { label: directory.name || 'Root', path: '', clickable: true, active: !selectedPath }
    ];

    let cursor = '';
    segments.forEach((segment, index) => {
      cursor = cursor ? `${cursor}/${segment}` : segment;
      const isLast = index === segments.length - 1;
      const canClick = !isLast || selectedType === 'file';
      crumbs.push({
        label: segment,
        path: cursor,
        clickable: canClick,
        active: isLast
      });
    });

    elements.directoryExplorerBreadcrumb.innerHTML = crumbs
      .map((crumb, index) => {
        const escapedLabel = this.host.escapeHtml(crumb.label || 'Root');
        const separator =
          index === 0 ? '' : '<span class="workspace-directory-crumb-separator">/</span>';
        if (!crumb.clickable || crumb.active) {
          return `${separator}<span class="workspace-directory-crumb is-active">${escapedLabel}</span>`;
        }

        return `
        ${separator}
        <button type="button"
                class="workspace-directory-crumb"
                data-action="breadcrumb"
                data-path="${this.encodeDataPath(crumb.path)}">
          ${escapedLabel}
        </button>
      `;
      })
      .join('');
  }

  filterTree(node, query) {
    const isMatch =
      String(node.name || '')
        .toLowerCase()
        .includes(query) ||
      String(node.path || '')
        .toLowerCase()
        .includes(query);

    if (node.type === 'file') {
      return isMatch ? { ...node } : null;
    }

    const filteredChildren = (node.children || [])
      .map(child => this.filterTree(child, query))
      .filter(Boolean);

    if (node.path === '' || isMatch || filteredChildren.length > 0) {
      return {
        ...node,
        children: filteredChildren
      };
    }

    return null;
  }

  renderTreeChildren(children, depth, forceExpanded) {
    const sortedChildren = this.getSortedChildren(children);
    return sortedChildren
      .map(node => this.renderTreeNode(node, depth, forceExpanded))
      .join('');
  }

  renderTreeNode(node, depth, forceExpanded) {
    const encodedPath = this.encodeDataPath(node.path);
    const isDirectory = node.type === 'dir';
    const isExpanded =
      isDirectory && (forceExpanded || this.expandedPaths.has(node.path));
    const isSelected = this.selectedPath === node.path;
    const sizeText =
      !isDirectory && Number.isFinite(node.size) ? this.host.formatFileSize(node.size) : '';
    const modifiedText = node.modTime ? formatDate(node.modTime) : '';
    const metaText = isDirectory
      ? `${(node.children || []).length} item${(node.children || []).length === 1 ? '' : 's'}`
      : [sizeText, modifiedText].filter(Boolean).join(' · ');

    const icon = isDirectory
      ? '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/></svg>'
      : '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13Z"/></svg>';

    const toggleButton = isDirectory
      ? `
        <button type="button"
                class="workspace-directory-tree-toggle ${isExpanded ? 'is-expanded' : ''}"
                data-action="toggle-folder"
                data-path="${encodedPath}"
                aria-label="${isExpanded ? 'Collapse folder' : 'Expand folder'}">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M8.59,16.59L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.59Z"/>
          </svg>
        </button>
      `
      : '<span class="workspace-directory-tree-spacer"></span>';

    const childrenHtml =
      isDirectory && isExpanded && Array.isArray(node.children) && node.children.length > 0
        ? `<div class="workspace-directory-tree-children">${this.renderTreeChildren(node.children, depth + 1, forceExpanded)}</div>`
        : '';

    return `
      <div class="workspace-directory-tree-node ${isSelected ? 'is-selected' : ''}">
        <div class="workspace-directory-tree-row" style="--tree-depth:${depth};">
          ${toggleButton}
          <button type="button"
                  class="workspace-directory-tree-main"
                  data-action="select-node"
                  data-path="${encodedPath}"
                  data-type="${isDirectory ? 'dir' : 'file'}">
            <span class="workspace-directory-tree-icon">${icon}</span>
            <span class="workspace-directory-tree-label">${this.host.escapeHtml(node.name || node.path || 'Untitled')}</span>
            <span class="workspace-directory-tree-meta">${this.host.escapeHtml(metaText)}</span>
          </button>
        </div>
        ${childrenHtml}
      </div>
    `;
  }

  getSortedChildren(children) {
    const direction = this.sortDirection === 'desc' ? -1 : 1;
    return [...children].sort((a, b) => {
      if (a.type !== b.type) {
        return a.type === 'dir' ? -1 : 1;
      }
      return (
        a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true }) * direction
      );
    });
  }

  toggleNode(path) {
    if (!path) return;
    if (!this.nodeIndex.has(path)) return;
    if (this.expandedPaths.has(path)) {
      this.expandedPaths.delete(path);
    } else {
      this.expandedPaths.add(path);
    }
    this.persistState();
    this.render();
  }

  expandNode(path) {
    if (!path) return;
    if (!this.nodeIndex.has(path)) return;
    this.expandedPaths.add(path);
    this.persistState();
    this.render();
  }

  collapseNode(path) {
    if (!path) return;
    this.expandedPaths.delete(path);
    this.persistState();
    this.render();
  }

  selectNode(path, type, { autoExpand = false } = {}) {
    const normalizedPath = this.normalizeRelativePath(path);
    const node = this.nodeIndex.get(normalizedPath);
    if (!node) return;

    this.selectedPath = normalizedPath;
    this.selectedType = type === 'dir' ? 'dir' : node.type;

    if (autoExpand && this.selectedType === 'dir') {
      this.expandedPaths.add(normalizedPath);
    }

    this.ensureAncestorsExpanded(normalizedPath, this.selectedType);
    this.persistState();
    this.render();
  }

  ensureAncestorsExpanded(path, type) {
    const normalizedPath = this.normalizeRelativePath(path);
    if (!normalizedPath) return;

    const parts = normalizedPath.split('/');
    const limit = type === 'dir' ? parts.length : parts.length - 1;
    let current = '';

    for (let i = 0; i < limit; i += 1) {
      current = current ? `${current}/${parts[i]}` : parts[i];
      this.expandedPaths.add(current);
    }
  }

  renderPreview() {
    const previewEl = this.host.elements.directoryExplorerPreview;
    if (!previewEl) return;

    const selectedPath = this.selectedPath;
    if (!selectedPath) {
      previewEl.innerHTML =
        '<div class="workspace-directory-preview-empty">Select a file or folder to inspect.</div>';
      return;
    }

    const node = this.nodeIndex.get(selectedPath);
    if (!node) {
      previewEl.innerHTML =
        '<div class="workspace-directory-preview-empty">Select a valid entry to preview.</div>';
      return;
    }

    if (node.type === 'dir') {
      this.renderFolderPreview(node);
      return;
    }

    void this.renderFilePreview(node);
  }

  renderFolderPreview(node) {
    const previewEl = this.host.elements.directoryExplorerPreview;
    if (!previewEl) return;

    const stats = this.collectStats(node);
    const childItems = this.getSortedChildren(node.children || []).slice(0, 16);

    previewEl.innerHTML = `
      <div class="workspace-directory-preview-header">
        <div class="workspace-directory-preview-title">${this.host.escapeHtml(node.name || 'Folder')}</div>
        <div class="workspace-directory-preview-subtitle">${this.host.escapeHtml(node.path || '/')}</div>
      </div>
      <div class="workspace-directory-preview-stats">
        <span class="workspace-directory-pill">${stats.files} file${stats.files === 1 ? '' : 's'}</span>
        <span class="workspace-directory-pill">${stats.folders} folder${stats.folders === 1 ? '' : 's'}</span>
      </div>
      <div class="workspace-directory-preview-directory-list">
        ${
          childItems.length === 0
            ? '<div class="workspace-directory-preview-empty-inline">Folder is empty.</div>'
            : childItems
                .map(
                  child => `
            <button type="button"
                    class="workspace-directory-preview-entry"
                    data-action="select-node"
                    data-path="${this.encodeDataPath(child.path)}"
                    data-type="${child.type}">
              <span>${this.host.escapeHtml(child.name)}</span>
              <span>${this.host.escapeHtml(child.type === 'dir' ? 'Folder' : this.host.formatFileSize(child.size || 0))}</span>
            </button>
          `
                )
                .join('')
        }
      </div>
    `;
  }

  async renderFilePreview(node) {
    const previewEl = this.host.elements.directoryExplorerPreview;
    if (!previewEl) return;

    const directoryId = this.directory?.id;
    if (!directoryId) return;

    const endpoint = this.getFileEndpoint(directoryId, node.path);
    previewEl.innerHTML = `
      <div class="workspace-directory-preview-header">
        <div class="workspace-directory-preview-title">${this.host.escapeHtml(node.name || 'File')}</div>
        <div class="workspace-directory-preview-subtitle">${this.host.escapeHtml(node.path || '')}</div>
      </div>
      <div class="workspace-directory-preview-loading">Loading preview...</div>
    `;

    try {
      const preview = await this.loadFilePreview(node.path);
      if (this.selectedPath !== node.path) return;

      const metadata = [
        preview.contentType || 'Unknown type',
        this.host.formatFileSize(preview.size || node.size || 0),
        node.modTime ? formatDate(node.modTime) : ''
      ]
        .filter(Boolean)
        .join(' · ');

      if (!preview.text) {
        previewEl.innerHTML = `
          <div class="workspace-directory-preview-header">
            <div class="workspace-directory-preview-title">${this.host.escapeHtml(node.name || 'File')}</div>
            <div class="workspace-directory-preview-subtitle">${this.host.escapeHtml(node.path || '')}</div>
          </div>
          <div class="workspace-directory-preview-stats">
            <span class="workspace-directory-pill">${this.host.escapeHtml(metadata)}</span>
            <a href="${endpoint}" target="_blank" rel="noopener noreferrer" class="workspace-directory-preview-open-link">Open raw</a>
          </div>
          <div class="workspace-directory-preview-empty-inline">
            ${preview.tooLarge ? 'File is too large for inline preview.' : 'Binary file preview is unavailable.'}
          </div>
        `;
        return;
      }

      previewEl.innerHTML = `
        <div class="workspace-directory-preview-header">
          <div class="workspace-directory-preview-title">${this.host.escapeHtml(node.name || 'File')}</div>
          <div class="workspace-directory-preview-subtitle">${this.host.escapeHtml(node.path || '')}</div>
        </div>
        <div class="workspace-directory-preview-stats">
          <span class="workspace-directory-pill">${this.host.escapeHtml(metadata)}</span>
          <a href="${endpoint}" target="_blank" rel="noopener noreferrer" class="workspace-directory-preview-open-link">Open raw</a>
        </div>
        <pre class="workspace-directory-preview-code">${this.host.escapeHtml(preview.text)}</pre>
        ${preview.truncated ? '<div class="workspace-directory-preview-note">Preview truncated for readability.</div>' : ''}
      `;
    } catch (error) {
      if (error?.name === 'AbortError') return;
      console.error('Failed to render directory file preview:', error);
      previewEl.innerHTML = `
        <div class="workspace-directory-preview-empty-inline">
          Failed to load file preview.
        </div>
      `;
    }
  }

  collectStats(node) {
    if (!node || node.type !== 'dir') {
      return { files: 0, folders: 0 };
    }

    const stats = { files: 0, folders: 0 };
    const walk = current => {
      (current.children || []).forEach(child => {
        if (child.type === 'dir') {
          stats.folders += 1;
          walk(child);
        } else {
          stats.files += 1;
        }
      });
    };
    walk(node);
    return stats;
  }

  async loadFilePreview(relativePath) {
    const directoryId = this.directory?.id;
    if (!directoryId) {
      throw new Error('No directory selected');
    }

    const normalizedPath = this.normalizeRelativePath(relativePath);
    const cacheKey = `${directoryId}:${normalizedPath}`;
    const cached = this.previewCache.get(cacheKey);
    if (cached) {
      return cached;
    }

    const endpoint = this.getFileEndpoint(directoryId, normalizedPath);
    this.abortPreviewRequest();
    const controller = new AbortController();
    this.previewAbortController = controller;

    const response = await fetch(endpoint, { signal: controller.signal });
    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to fetch file content');
    }

    const contentType = (response.headers.get('content-type') || '').split(';')[0].trim();
    const blob = await response.blob();
    const preview = {
      text: '',
      contentType,
      size: blob.size,
      truncated: false,
      tooLarge: false
    };

    const MAX_PREVIEW_BYTES = 1_500_000;
    const MAX_PREVIEW_CHARS = 30_000;

    if (this.isTextPreviewable(normalizedPath, contentType)) {
      if (blob.size <= MAX_PREVIEW_BYTES) {
        let text = await blob.text();
        if (text.length > MAX_PREVIEW_CHARS) {
          text = text.slice(0, MAX_PREVIEW_CHARS);
          preview.truncated = true;
        }
        preview.text = text;
      } else {
        preview.tooLarge = true;
      }
    }

    this.previewCache.set(cacheKey, preview);
    return preview;
  }

  isTextPreviewable(relativePath, contentType) {
    const lowerPath = relativePath.toLowerCase();
    const textExtensions = [
      '.txt',
      '.md',
      '.markdown',
      '.json',
      '.yaml',
      '.yml',
      '.xml',
      '.csv',
      '.ts',
      '.tsx',
      '.js',
      '.jsx',
      '.cjs',
      '.mjs',
      '.go',
      '.py',
      '.java',
      '.rb',
      '.php',
      '.sh',
      '.zsh',
      '.bash',
      '.html',
      '.htm',
      '.css',
      '.scss',
      '.sass',
      '.less',
      '.sql',
      '.toml',
      '.ini',
      '.env',
      '.gitignore',
      '.dockerfile',
      '.makefile',
      '.conf',
      '.log'
    ];

    if (textExtensions.some(extension => lowerPath.endsWith(extension))) {
      return true;
    }

    return (
      contentType.startsWith('text/') ||
      contentType.includes('json') ||
      contentType.includes('xml') ||
      contentType.includes('javascript') ||
      contentType.includes('yaml')
    );
  }

  getFileEndpoint(directoryId, relativePath) {
    const normalizedPath = this.normalizeRelativePath(relativePath);
    const encodedPath = normalizedPath
      .split('/')
      .filter(Boolean)
      .map(part => encodeURIComponent(part))
      .join('/');

    return `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/directories/${encodeURIComponent(directoryId)}/files/${encodedPath}`;
  }

  normalizeRelativePath(path) {
    if (!path) return '';
    return String(path)
      .trim()
      .replace(/\\/g, '/')
      .replace(/^\.\/+/, '')
      .replace(/^\/+/, '')
      .replace(/\/+/g, '/');
  }

  abortPreviewRequest() {
    if (this.previewAbortController) {
      this.previewAbortController.abort();
      this.previewAbortController = null;
    }
  }

  getStateStorageKey(directoryId) {
    return `workspace-directory-explorer:${this.host.workspaceId}:${directoryId}`;
  }

  loadPersistedState(directoryId) {
    this.expandedPaths = new Set();
    this.selectedPath = '';

    if (!directoryId || typeof localStorage === 'undefined') return;

    try {
      const raw = localStorage.getItem(this.getStateStorageKey(directoryId));
      if (!raw) return;

      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed.expanded_paths)) {
        this.expandedPaths = new Set(
          parsed.expanded_paths.map(path => this.normalizeRelativePath(path)).filter(Boolean)
        );
      }
      if (typeof parsed.selected_path === 'string') {
        this.selectedPath = this.normalizeRelativePath(parsed.selected_path);
      }
      if (parsed.selected_type === 'dir' || parsed.selected_type === 'file') {
        this.selectedType = parsed.selected_type;
      }
    } catch (error) {
      console.warn('Failed to restore directory explorer state:', error);
    }
  }

  persistState() {
    const directoryId = this.directory?.id;
    if (!directoryId || typeof localStorage === 'undefined') return;

    try {
      const payload = {
        expanded_paths: Array.from(this.expandedPaths),
        selected_path: this.selectedPath || '',
        selected_type: this.selectedType || ''
      };
      localStorage.setItem(
        this.getStateStorageKey(directoryId),
        JSON.stringify(payload)
      );
    } catch (error) {
      console.warn('Failed to persist directory explorer state:', error);
    }
  }

  encodeDataPath(path) {
    return encodeURIComponent(path || '');
  }

  decodeDataPath(path) {
    try {
      return decodeURIComponent(path || '');
    } catch {
      return path || '';
    }
  }
}
