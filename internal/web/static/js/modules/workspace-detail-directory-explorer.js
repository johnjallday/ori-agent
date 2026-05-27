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
    // Multi-select state for owned workspace files. selectedPaths holds every
    // selected file path; selectionAnchor is the pivot for Shift range selects.
    this.selectedPaths = new Set();
    this.selectionAnchor = '';
    this.searchQuery = '';
    this.sortDirection = 'asc';
    this.fileCache = new Map();
    this.previewCache = new Map();
    this.previewAbortController = null;
    this.loadToken = 0;
    this.draggedFile = null;
    this.dropTargetEl = null;
  }

  bindEvents() {
    const elements = this.host.elements;
    elements.directoryExplorerRefreshBtn?.addEventListener('click', () =>
      this.loadFiles({ force: true })
    );

    elements.directoryExplorerCreateFolderBtn?.addEventListener('click', () => {
      void this.promptCreateFolder();
    });

    elements.directoryExplorerRenameFolderBtn?.addEventListener('click', () => {
      void this.promptRenameSelectedFolder();
    });

    elements.directoryExplorerDeleteFolderBtn?.addEventListener('click', () => {
      void this.promptDeleteSelectedFolder();
    });

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
        this.selectNode(path, type, {
          autoExpand: true,
          range: event.shiftKey,
          toggle: event.metaKey || event.ctrlKey
        });
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
        this.selectNode(targetPath, targetType, { range: event.shiftKey });
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
      const bulkMoveButton = event.target.closest('[data-action="bulk-move"]');
      if (bulkMoveButton) {
        event.preventDefault();
        void this.bulkMoveSelectedFiles();
        return;
      }

      const bulkTrashButton = event.target.closest('[data-action="bulk-trash"]');
      if (bulkTrashButton) {
        event.preventDefault();
        void this.bulkTrashSelectedFiles();
        return;
      }

      const bulkClearButton = event.target.closest('[data-action="bulk-clear"]');
      if (bulkClearButton) {
        event.preventDefault();
        this.clearMultiSelection();
        this.render();
        return;
      }

      const moveButton = event.target.closest('[data-action="move-workspace-file"]');
      if (moveButton) {
        event.preventDefault();
        void this.promptMoveSelectedFile();
        return;
      }

      const locateButton = event.target.closest('[data-action="locate-orphan"]');
      if (locateButton) {
        event.preventDefault();
        const selectedNode = this.getSelectedNode();
        const targetPath = this.decodeDataPath(locateButton.dataset.path);
        if (selectedNode?.attachmentId && targetPath) {
          void this.locateMissingFile(selectedNode.attachmentId, targetPath);
        }
        return;
      }

      const openButton = event.target.closest('[data-action="open-workspace-file"]');
      if (openButton) {
        event.preventDefault();
        void this.openFileInOS(this.decodeDataPath(openButton.dataset.path));
        return;
      }

      const revealButton = event.target.closest('[data-action="reveal-workspace-file"]');
      if (revealButton) {
        event.preventDefault();
        void this.revealFileInOS(this.decodeDataPath(revealButton.dataset.path));
        return;
      }

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

    // Drag-and-drop file moves: drag a file row onto a folder (tree row or
    // breadcrumb folder, including root) to move it into that folder.
    const treeEl = elements.directoryExplorerTree;
    if (treeEl) {
      treeEl.addEventListener('dragstart', event => this.handleTreeDragStart(event));
      treeEl.addEventListener('dragend', () => this.clearDragState());
      this.bindDropZone(treeEl, event => this.resolveTreeDropTarget(event));

      // Double-click a file row to open it in the OS default app (owned files).
      treeEl.addEventListener('dblclick', event => {
        const main = event.target.closest('[data-action="select-node"][data-type="file"]');
        if (!main || this.source !== 'owned') return;
        const path = this.decodeDataPath(main.dataset.path);
        const node = this.nodeIndex.get(path);
        if (!node || node.status === 'missing') return;
        event.preventDefault();
        void this.openFileInOS(path);
      });
    }
    this.bindDropZone(elements.directoryExplorerBreadcrumb, event =>
      this.resolveBreadcrumbDropTarget(event)
    );

    elements.directoryExplorerModal?.addEventListener('hidden.bs.modal', () => {
      this.abortPreviewRequest();
      this.searchQuery = '';
      if (elements.directoryExplorerSearch) {
        elements.directoryExplorerSearch.value = '';
      }
    });
  }

  async open(directoryId, source = 'reference') {
    const normalizedSource =
      source === 'owned' ? 'owned' : source === 'attachment' ? 'attachment' : 'reference';
    if (!directoryId && normalizedSource !== 'owned') return;

    const directory = normalizedSource === 'owned'
      ? {
          id: '__workspace_files__',
          name: 'Workspace files',
          path: 'Managed files in this workspace'
        }
      : this.host.directories.find(entry => entry.id === directoryId);
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
    this.clearMultiSelection();
    this.previewCache = new Map();

    this.loadPersistedState(this.cacheKeyForDirectory(directory));

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

    this.updateFolderActionControls();
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
    this.updateFolderActionControls();
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
    const cacheKey = this.cacheKeyForDirectory(currentDirectory);

    if (this.source === 'attachment') {
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
      const endpoint = this.source === 'owned'
        ? `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/files/tree`
        : `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/directories/${encodeURIComponent(currentDirectory.id)}/files`;
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
            id: item?.id || '',
            attachmentId: item?.attachment_id || item?.attachmentId || '',
            folderId: item?.folder_id || item?.folderId || '',
            source: item?.source || '',
            url: item?.url || '',
            name: item?.name || normalizedPath.split('/').pop() || normalizedPath,
            path: normalizedPath,
            isDir: Boolean(item?.is_dir),
            size: Number(item?.size) || 0,
            modTime: item?.mod_time || '',
            status: item?.status || ''
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
      this.setDefaultFileAnchor(this.selectedPath, this.selectedType);
      this.ensureAncestorsExpanded(this.selectedPath, this.selectedType);
      return;
    }

    const firstFile = this.files.find(entry => !entry.isDir);
    if (firstFile) {
      this.selectedPath = firstFile.path;
      this.selectedType = 'file';
      this.setDefaultFileAnchor(firstFile.path, 'file');
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
      id: '',
      attachmentId: '',
      folderId: '',
      source: this.source,
      url: '',
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
        id: '',
        attachmentId: '',
        folderId: '',
        source: this.source,
        url: '',
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
        dirNode.id = entry.id || dirNode.id;
        dirNode.folderId = entry.folderId || dirNode.folderId;
        dirNode.source = entry.source || dirNode.source;
        dirNode.url = entry.url || dirNode.url;
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
        id: entry.id || '',
        attachmentId: entry.attachmentId || '',
        folderId: entry.folderId || '',
        source: entry.source || this.source,
        url: entry.url || '',
        status: entry.status || '',
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

    // Drop any selected paths that no longer exist (e.g. after a refresh that
    // removed trashed/moved files) so highlights and counts stay accurate.
    if (this.selectedPaths.size > 0) {
      this.selectedPaths = new Set(
        Array.from(this.selectedPaths).filter(
          path => this.nodeIndex.get(path)?.type === 'file'
        )
      );
      if (this.selectedPaths.size === 0) {
        this.selectionAnchor = '';
      }
    }

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

    this.updateFolderActionControls();
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
      this.updateFolderActionControls();
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
    const selectedFileCount = this.getSelectedFileNodes().length;
    const selectedNode = this.nodeIndex.get(this.selectedPath);
    const selectedLabel = selectedFileCount > 1
      ? `${selectedFileCount} files selected`
      : selectedNode
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
    const isSelected = isDirectory
      ? this.selectedPath === node.path
      : this.selectedPaths.has(node.path) || this.selectedPath === node.path;
    const isMissing = !isDirectory && node.status === 'missing';
    const sizeText =
      !isDirectory && Number.isFinite(node.size) ? this.host.formatFileSize(node.size) : '';
    const modifiedText = node.modTime ? formatDate(node.modTime) : '';
    const metaText = isDirectory
      ? `${(node.children || []).length} item${(node.children || []).length === 1 ? '' : 's'}`
      : isMissing
        ? 'Missing'
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

    const isDraggableFile =
      this.source === 'owned' && !isDirectory && !isMissing && Boolean(node.attachmentId);
    const dragAttrs = isDraggableFile
      ? ` draggable="true" data-attachment-id="${this.encodeDataPath(node.attachmentId)}"`
      : '';

    return `
      <div class="workspace-directory-tree-node ${isSelected ? 'is-selected' : ''} ${isMissing ? 'is-missing' : ''}">
        <div class="workspace-directory-tree-row" style="--tree-depth:${depth};">
          ${toggleButton}
          <button type="button"
                  class="workspace-directory-tree-main"
                  data-action="select-node"
                  data-path="${encodedPath}"
                  data-type="${isDirectory ? 'dir' : 'file'}"${dragAttrs}>
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

  selectNode(path, type, { autoExpand = false, range = false, toggle = false } = {}) {
    const normalizedPath = this.normalizeRelativePath(path);
    const node = this.nodeIndex.get(normalizedPath);
    if (!node) return;

    const resolvedType = type === 'dir' ? 'dir' : node.type;
    // Multi-select only applies to files in the owned "Workspace files" view.
    const multiEligible = this.source === 'owned' && resolvedType === 'file';
    const anchorIsFile =
      this.selectionAnchor && this.nodeIndex.get(this.selectionAnchor)?.type === 'file';

    if (multiEligible && range && anchorIsFile) {
      // Shift-click / Shift-arrow: select the contiguous run of files between
      // the anchor and this node. The anchor stays put for further extension.
      this.selectRange(this.selectionAnchor, normalizedPath);
      this.selectedPath = normalizedPath;
      this.selectedType = 'file';
    } else if (multiEligible && toggle) {
      // Cmd/Ctrl-click: add or remove this single file from the selection.
      if (this.selectedPaths.has(normalizedPath)) {
        this.selectedPaths.delete(normalizedPath);
      } else {
        this.selectedPaths.add(normalizedPath);
      }
      this.selectionAnchor = normalizedPath;
      if (this.selectedPaths.has(normalizedPath)) {
        this.selectedPath = normalizedPath;
      } else {
        const remaining = Array.from(this.selectedPaths);
        this.selectedPath = remaining.length ? remaining[remaining.length - 1] : '';
      }
      this.selectedType = this.selectedPath ? 'file' : '';
    } else {
      // Plain click (or any selection on a folder / non-owned source).
      this.selectedPaths = multiEligible ? new Set([normalizedPath]) : new Set();
      this.selectionAnchor = multiEligible ? normalizedPath : '';
      this.selectedPath = normalizedPath;
      this.selectedType = resolvedType;
    }

    if (autoExpand && this.selectedType === 'dir') {
      this.expandedPaths.add(normalizedPath);
    }

    this.ensureAncestorsExpanded(this.selectedPath || normalizedPath, this.selectedType);
    this.persistState();
    this.render();
  }

  // selectRange replaces the current selection with every file between two
  // paths, inclusive, using the rendered (visible) row order. Folders inside
  // the range are skipped since multi-select targets files only.
  selectRange(anchorPath, targetPath) {
    const treeEl = this.host.elements.directoryExplorerTree;
    if (!treeEl) {
      this.selectedPaths = new Set([anchorPath, targetPath]);
      return;
    }

    const paths = Array.from(
      treeEl.querySelectorAll('[data-action="select-node"]')
    ).map(button => this.decodeDataPath(button.dataset.path));

    const anchorIndex = paths.indexOf(anchorPath);
    const targetIndex = paths.indexOf(targetPath);
    if (anchorIndex === -1 || targetIndex === -1) {
      this.selectedPaths = new Set([targetPath]);
      this.selectionAnchor = targetPath;
      return;
    }

    const [start, end] =
      anchorIndex <= targetIndex ? [anchorIndex, targetIndex] : [targetIndex, anchorIndex];
    const next = new Set();
    for (let index = start; index <= end; index += 1) {
      const path = paths[index];
      if (this.nodeIndex.get(path)?.type === 'file') {
        next.add(path);
      }
    }
    this.selectedPaths = next;
  }

  clearMultiSelection() {
    this.selectedPaths = new Set();
    this.selectionAnchor = '';
  }

  // setDefaultFileAnchor seeds the multi-select anchor when a file is selected
  // by default/restore, so the user's first Shift-click extends a range from it.
  setDefaultFileAnchor(path, type) {
    if (this.source !== 'owned' || type !== 'file') return;
    const normalizedPath = this.normalizeRelativePath(path);
    if (!normalizedPath) return;
    this.selectionAnchor = normalizedPath;
    this.selectedPaths = new Set([normalizedPath]);
  }

  // getSelectedFileNodes returns the currently selected file nodes that still
  // exist in the tree (stale paths are ignored).
  getSelectedFileNodes() {
    const nodes = [];
    this.selectedPaths.forEach(path => {
      const node = this.nodeIndex.get(path);
      if (node && node.type === 'file') {
        nodes.push(node);
      }
    });
    return nodes;
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

  updateFolderActionControls() {
    const elements = this.host.elements;
    const isOwnedSource = this.source === 'owned';
    const selectedNode = this.getSelectedNode();
    const canManageSelectedFolder =
      isOwnedSource &&
      selectedNode?.type === 'dir' &&
      Boolean(selectedNode.path) &&
      Boolean(selectedNode.folderId);

    [
      elements.directoryExplorerCreateFolderBtn,
      elements.directoryExplorerRenameFolderBtn,
      elements.directoryExplorerDeleteFolderBtn
    ].forEach(button => {
      if (button) {
        button.hidden = !isOwnedSource;
      }
    });

    if (elements.directoryExplorerCreateFolderBtn) {
      elements.directoryExplorerCreateFolderBtn.disabled = !isOwnedSource;
    }
    if (elements.directoryExplorerRenameFolderBtn) {
      elements.directoryExplorerRenameFolderBtn.disabled = !canManageSelectedFolder;
    }
    if (elements.directoryExplorerDeleteFolderBtn) {
      elements.directoryExplorerDeleteFolderBtn.disabled = !canManageSelectedFolder;
    }
  }

  getSelectedNode() {
    return this.nodeIndex.get(this.normalizeRelativePath(this.selectedPath)) || null;
  }

  getSelectedFolderPathForCreate() {
    const selectedNode = this.getSelectedNode();
    if (!selectedNode) return '';
    if (selectedNode.type === 'dir') {
      return selectedNode.path || '';
    }
    return this.parentPathFor(selectedNode.path || '');
  }

  getSelectedManagedFolderNode() {
    const selectedNode = this.getSelectedNode();
    if (
      this.source === 'owned' &&
      selectedNode?.type === 'dir' &&
      selectedNode.path &&
      selectedNode.folderId
    ) {
      return selectedNode;
    }
    return null;
  }

  async promptCreateFolder() {
    if (this.source !== 'owned') return;

    const selectedFolder = this.getSelectedFolderPathForCreate();
    const defaultPath = selectedFolder ? `${selectedFolder}/New Folder` : 'New Folder';
    const rawPath = window.prompt('New folder path inside Workspace files', defaultPath);
    if (rawPath === null) return;

    const folderPath = this.normalizeRelativePath(rawPath);
    if (!folderPath) return;

    try {
      const payload = await this.requestWorkspaceJSON('/folders', {
        method: 'POST',
        body: { path: folderPath }
      });
      const nextPath = this.normalizeRelativePath(payload?.folder?.path || folderPath);
      this.selectedPath = nextPath;
      this.selectedType = 'dir';
      this.expandedPaths.add(nextPath);
      this.ensureAncestorsExpanded(nextPath, 'dir');
      await this.refreshOwnedTree();
      if (window.Toast) window.Toast.success('Folder created');
    } catch (error) {
      console.error('Failed to create workspace file folder:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to create folder');
    }
  }

  async promptRenameSelectedFolder() {
    const folderNode = this.getSelectedManagedFolderNode();
    if (!folderNode) return;

    const rawPath = window.prompt(
      'Rename folder path inside Workspace files',
      folderNode.path || ''
    );
    if (rawPath === null) return;

    const nextPath = this.normalizeRelativePath(rawPath);
    if (!nextPath || nextPath === folderNode.path) return;

    try {
      const payload = await this.requestWorkspaceJSON(`/folders/${encodeURIComponent(folderNode.folderId)}`, {
        method: 'PATCH',
        body: { path: nextPath }
      });
      const updatedPath = this.normalizeRelativePath(payload?.folder?.path || nextPath);
      this.selectedPath = updatedPath;
      this.selectedType = 'dir';
      this.expandedPaths.delete(folderNode.path);
      this.expandedPaths.add(updatedPath);
      this.ensureAncestorsExpanded(updatedPath, 'dir');
      await this.refreshOwnedTree();
      if (window.Toast) window.Toast.success('Folder renamed');
    } catch (error) {
      console.error('Failed to rename workspace file folder:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to rename folder');
    }
  }

  async promptDeleteSelectedFolder() {
    const folderNode = this.getSelectedManagedFolderNode();
    if (!folderNode) return;

    const confirmed = window.confirm(
      `Delete the empty folder "${folderNode.path}" from Workspace files?`
    );
    if (!confirmed) return;

    try {
      await this.requestWorkspaceJSON(`/folders/${encodeURIComponent(folderNode.folderId)}`, {
        method: 'DELETE'
      });
      const parentPath = this.parentPathFor(folderNode.path);
      this.selectedPath = parentPath;
      this.selectedType = parentPath ? 'dir' : '';
      this.expandedPaths.delete(folderNode.path);
      await this.refreshOwnedTree();
      if (window.Toast) window.Toast.success('Folder deleted');
    } catch (error) {
      console.error('Failed to delete workspace file folder:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to delete folder');
    }
  }

  async promptMoveSelectedFile() {
    const selectedNode = this.getSelectedNode();
    if (this.source !== 'owned' || selectedNode?.type !== 'file' || !selectedNode.attachmentId) {
      return;
    }

    const currentFolder = this.parentPathFor(selectedNode.path);
    const rawPath = window.prompt(
      'Move file to folder path inside Workspace files. Leave blank for root.',
      currentFolder
    );
    if (rawPath === null) return;

    const targetFolder = this.normalizeRelativePath(rawPath);
    if (targetFolder === currentFolder) return;

    await this.moveFileToFolder(selectedNode.attachmentId, targetFolder, selectedNode.name);
  }

  async moveFileToFolder(attachmentId, targetFolder, fileName) {
    if (this.source !== 'owned' || !attachmentId) return;

    const normalizedTarget = this.normalizeRelativePath(targetFolder);
    try {
      const payload = await this.requestWorkspaceJSON(
        `/attachments/${encodeURIComponent(attachmentId)}/move`,
        {
          method: 'PATCH',
          body: { target_folder: normalizedTarget }
        }
      );
      const nextPath = this.normalizeRelativePath(
        payload?.attachment?.file_meta?.relative_path ||
          payload?.attachment?.file?.relative_path ||
          (normalizedTarget ? `${normalizedTarget}/${fileName}` : fileName)
      );
      this.selectedPath = nextPath;
      this.selectedType = 'file';
      this.ensureAncestorsExpanded(nextPath, 'file');
      await this.refreshOwnedTree();
      if (window.Toast) window.Toast.success('File moved');
    } catch (error) {
      console.error('Failed to move workspace file:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to move file');
    }
  }

  renderBulkSelectionPanel() {
    const previewEl = this.host.elements.directoryExplorerPreview;
    if (!previewEl) return;

    const nodes = this.getSelectedFileNodes();
    const count = nodes.length;
    const totalSize = nodes.reduce(
      (sum, node) => sum + (Number.isFinite(node.size) ? node.size : 0),
      0
    );
    const canMove = nodes.some(node => node.attachmentId && node.status !== 'missing');
    const canTrash = nodes.some(node => node.attachmentId);
    const listItems = nodes
      .slice(0, 50)
      .map(
        node =>
          `<li class="workspace-directory-bulk-item">${this.host.escapeHtml(node.name || node.path)}</li>`
      )
      .join('');

    previewEl.innerHTML = `
      <div class="workspace-directory-preview-header">
        <div class="workspace-directory-preview-title">${count} files selected</div>
        <div class="workspace-directory-preview-subtitle">${this.host.escapeHtml(this.host.formatFileSize(totalSize))} total</div>
      </div>
      <div class="workspace-directory-preview-stats">
        ${canMove ? '<button type="button" class="workspace-directory-preview-open-link" data-action="bulk-move">Move selected</button>' : ''}
        ${canTrash ? '<button type="button" class="workspace-directory-preview-open-link is-danger" data-action="bulk-trash">Delete selected</button>' : ''}
        <button type="button" class="workspace-directory-preview-open-link" data-action="bulk-clear">Clear selection</button>
      </div>
      <ul class="workspace-directory-bulk-list">${listItems}</ul>
      ${count > 50 ? `<div class="workspace-directory-preview-note">Showing first 50 of ${count}.</div>` : ''}
    `;
  }

  async bulkMoveSelectedFiles() {
    if (this.source !== 'owned') return;
    const nodes = this.getSelectedFileNodes().filter(
      node => node.attachmentId && node.status !== 'missing'
    );
    if (nodes.length === 0) return;

    const rawPath = window.prompt(
      `Move ${nodes.length} file${nodes.length === 1 ? '' : 's'} to folder path inside Workspace files. Leave blank for root.`,
      ''
    );
    if (rawPath === null) return;

    const targetFolder = this.normalizeRelativePath(rawPath);
    let successCount = 0;
    for (const node of nodes) {
      if (this.parentPathFor(node.path) === targetFolder) {
        // Already in the destination; nothing to do but count as handled.
        successCount += 1;
        continue;
      }
      try {
        await this.requestWorkspaceJSON(
          `/attachments/${encodeURIComponent(node.attachmentId)}/move`,
          {
            method: 'PATCH',
            body: { target_folder: targetFolder }
          }
        );
        successCount += 1;
      } catch (error) {
        console.error('Failed to move workspace file:', node.path, error);
      }
    }

    if (window.Toast) {
      if (successCount > 0) {
        window.Toast.success(`Moved ${successCount} file${successCount === 1 ? '' : 's'}`);
      } else {
        window.Toast.error('Failed to move files');
      }
    }

    this.clearMultiSelection();
    this.selectedPath = '';
    this.selectedType = '';
    await this.refreshOwnedTree();
  }

  async bulkTrashSelectedFiles() {
    if (this.source !== 'owned') return;
    const nodes = this.getSelectedFileNodes().filter(node => node.attachmentId);
    if (nodes.length === 0) return;

    const confirmed = window.confirm(
      `Move ${nodes.length} file${nodes.length === 1 ? '' : 's'} to trash?`
    );
    if (!confirmed) return;

    try {
      const payload = await this.requestWorkspaceJSON('/attachments/bulk-trash', {
        method: 'POST',
        body: { attachment_ids: nodes.map(node => node.attachmentId) }
      });
      const count = Number.isFinite(payload?.success_count)
        ? payload.success_count
        : nodes.length;
      if (window.Toast) {
        window.Toast.success(`Moved ${count} file${count === 1 ? '' : 's'} to trash`);
      }
      this.clearMultiSelection();
      this.selectedPath = '';
      this.selectedType = '';
      await this.refreshOwnedTree();
    } catch (error) {
      console.error('Failed to bulk trash workspace files:', error);
      if (window.Toast) {
        window.Toast.error(error.message || 'Failed to move files to trash');
      }
    }
  }

  handleTreeDragStart(event) {
    const main = event.target.closest('[data-action="select-node"][draggable="true"]');
    if (!main) return;

    const path = this.decodeDataPath(main.dataset.path);
    const node = this.nodeIndex.get(path);
    const attachmentId =
      node?.attachmentId || this.decodeDataPath(main.dataset.attachmentId || '');

    if (this.source !== 'owned' || !node || node.type !== 'file' || !attachmentId) {
      event.preventDefault();
      return;
    }

    this.draggedFile = { path, attachmentId, name: node.name };
    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'move';
      try {
        event.dataTransfer.setData('text/plain', path);
      } catch {
        // Some browsers disallow setData here; the in-memory state is enough.
      }
    }
    main.classList.add('is-dragging');
  }

  bindDropZone(element, resolver) {
    if (!element) return;
    element.addEventListener('dragover', event => this.handleDragOver(event, resolver));
    element.addEventListener('dragleave', event => this.handleDragLeave(event, element));
    element.addEventListener('drop', event => {
      void this.handleDrop(event, resolver);
    });
  }

  handleDragOver(event, resolver) {
    if (!this.draggedFile) return;

    const drop = resolver(event);
    const currentFolder = this.parentPathFor(this.draggedFile.path);
    if (!drop || drop.path === currentFolder) {
      this.setDropTarget(null);
      return;
    }

    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
    this.setDropTarget(drop.element);
  }

  handleDragLeave(event, container) {
    if (!event.relatedTarget || !container.contains(event.relatedTarget)) {
      this.setDropTarget(null);
    }
  }

  async handleDrop(event, resolver) {
    if (!this.draggedFile) return;

    const drop = resolver(event);
    this.setDropTarget(null);
    if (!drop) return;

    const dragged = this.draggedFile;
    const currentFolder = this.parentPathFor(dragged.path);
    if (drop.path === currentFolder) {
      this.clearDragState();
      return;
    }

    event.preventDefault();
    this.clearDragState();
    await this.moveFileToFolder(dragged.attachmentId, drop.path, dragged.name);
  }

  resolveTreeDropTarget(event) {
    const row = event.target.closest('.workspace-directory-tree-row');
    if (!row) return null;

    const main = row.querySelector('[data-action="select-node"][data-type="dir"]');
    if (!main) return null;

    const path = this.decodeDataPath(main.dataset.path);
    if (!this.nodeIndex.has(path)) return null;

    return { element: row.closest('.workspace-directory-tree-node'), path };
  }

  resolveBreadcrumbDropTarget(event) {
    const crumb = event.target.closest('[data-action="breadcrumb"]');
    if (!crumb) return null;

    const path = this.decodeDataPath(crumb.dataset.path);
    // Only folders are valid targets: the directory root ('') or a dir node.
    if (path !== '' && this.nodeIndex.get(path)?.type !== 'dir') return null;

    return { element: crumb, path };
  }

  setDropTarget(element) {
    if (this.dropTargetEl === element) return;
    if (this.dropTargetEl) {
      this.dropTargetEl.classList.remove('is-drop-target');
    }
    this.dropTargetEl = element || null;
    if (this.dropTargetEl) {
      this.dropTargetEl.classList.add('is-drop-target');
    }
  }

  clearDragState() {
    this.draggedFile = null;
    this.setDropTarget(null);
    const treeEl = this.host.elements.directoryExplorerTree;
    treeEl
      ?.querySelectorAll('.is-dragging')
      .forEach(el => el.classList.remove('is-dragging'));
  }

  async refreshOwnedTree() {
    const cacheKey = this.cacheKeyForDirectory(this.directory);
    if (cacheKey) {
      this.fileCache.delete(cacheKey);
    }
    this.previewCache = new Map();
    await this.loadFiles({ force: true });
    if (typeof this.host.loadFiles === 'function') {
      await this.host.loadFiles();
    }
  }

  async requestWorkspaceJSON(path, options = {}) {
    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}${path}`,
      {
        method: options.method || 'GET',
        headers: {
          'Content-Type': 'application/json',
          ...(options.headers || {})
        },
        body:
          options.body && typeof options.body !== 'string'
            ? JSON.stringify(options.body)
            : options.body
      }
    );
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(payload.error || payload.message || 'Request failed');
    }
    return payload;
  }

  parentPathFor(path) {
    const normalizedPath = this.normalizeRelativePath(path);
    if (!normalizedPath || !normalizedPath.includes('/')) {
      return '';
    }
    return normalizedPath.slice(0, normalizedPath.lastIndexOf('/'));
  }

  renderPreview() {
    const previewEl = this.host.elements.directoryExplorerPreview;
    if (!previewEl) return;

    if (this.source === 'owned' && this.getSelectedFileNodes().length > 1) {
      this.renderBulkSelectionPanel();
      return;
    }

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

    if (node.status === 'missing') {
      this.renderMissingFilePreview(node);
      return;
    }

    void this.renderFilePreview(node);
  }

  // renderMissingFilePreview shows the DAW-style "missing → locate" state for an
  // attachment whose backing file is gone from disk and could not be matched to a
  // rename automatically. It offers to re-link the attachment to an unlinked file
  // ("orphan") that exists in this workspace.
  renderMissingFilePreview(node) {
    const previewEl = this.host.elements.directoryExplorerPreview;
    if (!previewEl) return;

    const orphans = this.getOrphanCandidates();
    const canLocate = this.source === 'owned' && Boolean(node.attachmentId);

    const orphanList = !canLocate
      ? ''
      : orphans.length === 0
        ? '<div class="workspace-directory-preview-empty-inline">No unlinked files were found in this workspace to relink to. Re-add the file to restore it.</div>'
        : `
          <div class="workspace-directory-preview-note">Link this entry to a file that appeared in the workspace:</div>
          <div class="workspace-directory-preview-directory-list">
            ${orphans
              .map(
                orphan => `
              <button type="button"
                      class="workspace-directory-preview-entry"
                      data-action="locate-orphan"
                      data-path="${this.encodeDataPath(orphan.path)}">
                <span>${this.host.escapeHtml(orphan.name)}</span>
                <span>${this.host.escapeHtml(this.host.formatFileSize(orphan.size || 0))}</span>
              </button>
            `
              )
              .join('')}
          </div>
        `;

    const revealButton =
      this.source === 'owned'
        ? `<button type="button" class="workspace-directory-preview-open-link" data-action="reveal-workspace-file" data-path="${this.encodeDataPath(node.path || '')}">Reveal folder in Finder</button>`
        : '';

    previewEl.innerHTML = `
      <div class="workspace-directory-preview-header">
        <div class="workspace-directory-preview-title">${this.host.escapeHtml(node.name || 'File')}</div>
        <div class="workspace-directory-preview-subtitle">${this.host.escapeHtml(node.path || '')}</div>
      </div>
      <div class="workspace-directory-preview-stats">
        <span class="workspace-directory-pill is-missing">Missing from disk</span>
        ${revealButton}
      </div>
      <div class="workspace-directory-preview-empty-inline">
        This file is no longer on disk. It may have been renamed, moved, or deleted outside the app.
      </div>
      ${orphanList}
    `;
  }

  // getOrphanCandidates returns workspace files present on disk that no attachment
  // owns yet — the candidates for relinking a missing attachment.
  getOrphanCandidates() {
    return (this.files || [])
      .filter(entry => !entry.isDir && !entry.attachmentId && entry.status !== 'missing')
      .sort((a, b) =>
        String(a.path || '').localeCompare(String(b.path || ''), undefined, {
          sensitivity: 'base',
          numeric: true
        })
      );
  }

  async locateMissingFile(attachmentId, relativePath) {
    if (this.source !== 'owned' || !attachmentId) return;

    const targetPath = this.normalizeRelativePath(relativePath);
    if (!targetPath) return;

    try {
      const payload = await this.requestWorkspaceJSON(
        `/attachments/${encodeURIComponent(attachmentId)}/locate`,
        {
          method: 'PATCH',
          body: { relative_path: targetPath }
        }
      );
      const nextPath = this.normalizeRelativePath(
        payload?.attachment?.file_meta?.relative_path ||
          payload?.attachment?.file?.relative_path ||
          targetPath
      );
      this.selectedPath = nextPath;
      this.selectedType = 'file';
      this.ensureAncestorsExpanded(nextPath, 'file');
      await this.refreshOwnedTree();
      if (window.Toast) window.Toast.success('File relinked');
    } catch (error) {
      console.error('Failed to locate workspace file:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to relink file');
    }
  }

  openFileInOS(relativePath) {
    return this.requestOSFileAction('open', relativePath, 'Failed to open file');
  }

  revealFileInOS(relativePath) {
    return this.requestOSFileAction('reveal', relativePath, 'Failed to reveal file');
  }

  // requestOSFileAction asks the server to open or reveal a workspace file with
  // the native OS. This only works when the server runs on the user's machine;
  // failures (e.g. a remote server) surface as a toast.
  async requestOSFileAction(action, relativePath, failMessage) {
    if (this.source !== 'owned') return;
    const targetPath = this.normalizeRelativePath(relativePath);
    if (!targetPath) return;

    try {
      await this.requestWorkspaceJSON(`/files/${action}`, {
        method: 'POST',
        body: { relative_path: targetPath }
      });
    } catch (error) {
      console.error(`Failed to ${action} workspace file:`, error);
      if (window.Toast) window.Toast.error(error.message || failMessage);
    }
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
            ${this.renderFilePreviewActions(endpoint, node)}
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
          ${this.renderFilePreviewActions(endpoint, node)}
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

  renderFilePreviewActions(endpoint, node) {
    const isOwned = this.source === 'owned';
    const encodedPath = this.encodeDataPath(node?.path || '');
    // OS open/reveal only work when the server is on the user's machine; "Open
    // raw" stays as the universal, remote-safe fallback.
    const osButtons = isOwned
      ? `
        <button type="button" class="workspace-directory-preview-open-link" data-action="open-workspace-file" data-path="${encodedPath}">Open</button>
        <button type="button" class="workspace-directory-preview-open-link" data-action="reveal-workspace-file" data-path="${encodedPath}">Reveal in Finder</button>
      `
      : '';
    const moveButton =
      isOwned && node?.attachmentId
        ? '<button type="button" class="workspace-directory-preview-open-link" data-action="move-workspace-file">Move</button>'
        : '';
    return `
      <a href="${endpoint}" target="_blank" rel="noopener noreferrer" class="workspace-directory-preview-open-link">Open raw</a>
      ${osButtons}
      ${moveButton}
    `;
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
    const cacheKey = `${this.cacheKeyForDirectory(this.directory)}:${normalizedPath}`;
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

    if (this.source === 'owned') {
      return `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/files/${encodedPath}`;
    }

    return `/api/workspaces/${encodeURIComponent(this.host.workspaceId)}/directories/${encodeURIComponent(directoryId)}/files/${encodedPath}`;
  }

  cacheKeyForDirectory(directory) {
    if (this.source === 'owned') {
      return `owned:${this.host.workspaceId}`;
    }
    return directory?.id || '';
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
    this.clearMultiSelection();

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
    const directoryId = this.cacheKeyForDirectory(this.directory);
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
