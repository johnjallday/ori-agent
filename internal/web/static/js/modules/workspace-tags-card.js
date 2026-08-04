/**
 * Tags field for the create-workspace modal (create-workspace-modal.tmpl).
 *
 * Like ProjectTemplateCard, the markup ships on several pages driven by
 * different host modules (sessions.js on the live hub/home,
 * workspace-create.js on the legacy hub), so the card binds its own
 * behavior here and host modules only merge getPayloadFields() into their
 * create payload and call reset() when the modal form resets.
 *
 * The actual editor is the shared tag input widget (tag-input.js); this
 * module additionally shows which tags the selected project template will
 * contribute automatically.
 */

let wtcWidget = null;

function wtcElements() {
  return {
    mount: document.getElementById('folderTagsMount'),
    templateHint: document.getElementById('folderTemplateTagsHint'),
    modal: document.getElementById('addFolderModal')
  };
}

function wtcEnsureWidget() {
  if (wtcWidget) return wtcWidget;
  const els = wtcElements();
  if (!els.mount || !window.OriTagInput?.createTagInput) return null;
  wtcWidget = window.OriTagInput.createTagInput({
    container: els.mount,
    placeholder: 'Add tag…'
  });
  return wtcWidget;
}

export function wtcGetPayloadFields() {
  const tags = wtcWidget?.getTags() || [];
  return tags.length > 0 ? { tags } : {};
}

export function wtcReset() {
  wtcWidget?.setTags([]);
  wtcRenderTemplateTagsHint([]);
}

function wtcRenderTemplateTagsHint(tags) {
  const els = wtcElements();
  if (!els.templateHint) return;
  if (!Array.isArray(tags) || tags.length === 0) {
    els.templateHint.hidden = true;
    els.templateHint.textContent = '';
    return;
  }
  els.templateHint.textContent = `From template: ${tags.join(', ')} (added automatically)`;
  els.templateHint.hidden = false;
}

function wtcInit() {
  const els = wtcElements();
  if (!els.mount) return;
  wtcEnsureWidget();
  // The unified Template picker emits its selection (with the template's tags);
  // show which tags the selected template will contribute automatically.
  els.modal?.addEventListener('workspace-template-selected', event => {
    const template = event?.detail?.template || null;
    wtcRenderTemplateTagsHint(Array.isArray(template?.tags) ? template.tags : []);
  });
  // Re-create the widget lazily after late module load and refresh the
  // suggestion pool every time the modal opens.
  els.modal?.addEventListener('show.bs.modal', () => {
    const widget = wtcEnsureWidget();
    void widget?.refreshPool?.();
    wtcRenderTemplateTagsHint([]);
  });
}

if (typeof window !== 'undefined') {
  window.WorkspaceTagsCard = {
    getPayloadFields: wtcGetPayloadFields,
    reset: wtcReset
  };
  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', wtcInit);
    } else {
      wtcInit();
    }
  }
}
