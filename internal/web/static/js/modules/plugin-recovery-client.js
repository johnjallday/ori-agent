/**
 * Plugin recovery client — the one caller of a blueprint's plugin-recovery
 * endpoint.
 *
 * The Create Workspace card and the setup quest's group screen both repair a
 * blueprint's plugin dependency the same way: a preview that returns the trust
 * disclosure, then a confirmation that echoes what the preview disclosed. Both
 * send only an action name and a plugin name; the server resolves the source
 * from the trusted blueprint. Keeping the request shape here means the two
 * surfaces cannot drift into different bodies for the same consent.
 *
 * Each call returns PluginLifecycle.request's structured result rather than
 * throwing.
 */

function prcEndpoint(templateID) {
  return `/api/project-templates/${encodeURIComponent(String(templateID || ''))}/plugin-recovery`;
}

// previewRecovery asks for the disclosure. Nothing is changed by it.
function prcPreviewRecovery(templateID, action, plugin, generation) {
  return window.PluginLifecycle.request('POST', prcEndpoint(templateID), {
    action,
    plugin,
    confirm: false,
    generation: Number(generation) || 0
  });
}

// confirmRecovery applies the reviewed action. The generation and release are
// the ones the disclosure was derived from; the server refuses either if the
// plugin or its newest reviewed release changed in between.
function prcConfirmRecovery(templateID, action, plugin, generation, release) {
  return window.PluginLifecycle.request('POST', prcEndpoint(templateID), {
    action,
    plugin,
    confirm: true,
    generation: Number(generation) || 0,
    release: String(release || '') || undefined
  });
}

window.PluginRecoveryClient = {
  endpoint: prcEndpoint,
  previewRecovery: prcPreviewRecovery,
  confirmRecovery: prcConfirmRecovery
};
