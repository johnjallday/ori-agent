import type { APIRequestContext } from '@playwright/test';

// Ori no longer seeds a built-in assistant when a Default model is set, so a
// spec that needs a workspace led by a ready agent creates one explicitly.
// The name is deliberately ordinary; nothing routes to it by name.
export const LEAD_AGENT = 'Desk Lead';

// ensureLeadAgent saves the lead agent on the spec's Default model. Repeated
// calls are harmless: an existing agent of that name is kept as it is.
export async function ensureLeadAgent(
  request: APIRequestContext,
  options: { name?: string; provider?: string; model?: string } = {}
): Promise<string> {
  const name = options.name || LEAD_AGENT;
  const existing = await request.get(`/api/agents?name=${encodeURIComponent(name)}`);
  if (existing.ok()) return name;
  const created = await request.post('/api/agents', {
    data: {
      name,
      role: 'orchestrator',
      ...(options.model ? { model: options.model } : {}),
      ...(options.provider ? { llm_provider: options.provider } : {})
    }
  });
  if (!created.ok()) {
    throw new Error(
      `could not create lead agent ${name}: ${created.status()} ${await created.text()}`
    );
  }
  return name;
}
