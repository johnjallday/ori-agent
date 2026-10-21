import { createServer } from 'node:http';

type Scenario = {
  mode?: 'success' | 'failure' | 'ambiguous' | 'ordinary';
  createNow?: boolean;
  name?: string;
};

// The only synthetic integration: a local OpenAI-compatible model endpoint.
// Build sessions, held selections, creator gates, grants and scans remain real.
// Use only an isolated app with LM_STUDIO_BASE_URL=http://127.0.0.1:8940/v1
// and LM_STUDIO_MODEL=ori-synthetic-model. Never change production settings.
export async function startJanitorBuildModel() {
  let sequence = 0;
  let buildCalls = 0;
  let scenario: Scenario = {};
  let barrier: { started: () => void; wait: Promise<void>; release: () => void } | undefined;
  const server = createServer(async (request, response) => {
    response.setHeader('Content-Type', 'application/json');
    if (request.method === 'GET') {
      response.end(
        JSON.stringify({
          object: 'list',
          data: [{ id: 'ori-synthetic-model', object: 'model', owned_by: 'fixture' }]
        })
      );
      return;
    }
    // Classify without retaining/logging prompts. HQ background work may also
    // use this synthetic endpoint; count it separately from build inference.
    let payload = '';
    for await (const chunk of request) payload += chunk.toString();
    const isBuild =
      payload.includes('workspace_build_reply') ||
      payload.includes('Right now you are setting up a new workspace');
    sequence++;
    if (isBuild) buildCalls++;
    const selected = isBuild ? { ...scenario } : {};
    const held = isBuild ? barrier : undefined;
    held?.started();
    if (held) await held.wait;
    const suffix = `${Date.now()}-${sequence}`;
    const ambiguous = selected.mode === 'ambiguous';
    const ordinary = selected.mode === 'ordinary';
    const reply = {
      say: ambiguous
        ? 'The metadata does not establish what you want to do.'
        : ordinary
          ? 'Suggested a blank notebook workspace. Review and edit it before creating.'
          : 'Suggested a File Janitor workspace. Review and edit it before creating.',
      ask: {
        question: ambiguous ? 'What would you like to do with this folder?' : '',
        choices: ambiguous
          ? [
              { id: 'organize', label: 'Organize files' },
              { id: 'project', label: 'Work on a project' }
            ]
          : [],
        allow_free_text: true
      },
      patch: {
        set: ambiguous ? ['none'] : ['blueprint_id', 'name', 'description', 'goal', 'team'],
        blueprint_id: ordinary ? '' : 'file-janitor',
        name: selected.name || `Synthetic Janitor ${suffix}`,
        description: 'Synthetic folder review fixture',
        goal: 'Review synthetic inbox files',
        inputs: [],
        parent_id: '',
        ask_folder: false,
        tags: [],
        color: '',
        team: {
          mode: ordinary ? 'agentless' : 'staffed',
          roles: [],
          saved_agents: [],
          agents: ordinary
            ? []
            : [
                {
                  name: 'File Curator',
                  action: 'create',
                  rename: `Synthetic Curator ${suffix}`,
                  provider: 'lmstudio',
                  model: 'ori-synthetic-model',
                  system_prompt: 'Synthetic edited curator prompt.'
                }
              ]
        }
      },
      why: [],
      alternatives: [],
      ready: !ambiguous,
      create_now: selected.createNow === true
    };
    response.end(
      JSON.stringify({
        id: 'synthetic',
        object: 'chat.completion',
        created: Math.floor(Date.now() / 1000),
        model: 'ori-synthetic-model',
        choices: [
          {
            index: 0,
            message: {
              role: 'assistant',
              content: selected.mode === 'failure' ? 'not valid JSON' : JSON.stringify(reply)
            },
            finish_reason: 'stop'
          }
        ],
        usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 }
      })
    );
  });
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(8940, '127.0.0.1', resolve);
  });
  const stop = async () => {
    barrier?.release();
    await new Promise<void>((resolve, reject) =>
      server.close(error => (error ? reject(error) : resolve()))
    );
  };
  return Object.assign(stop, {
    calls: () => buildCalls,
    totalCalls: () => sequence,
    scenario(value: Scenario = {}) {
      scenario = value;
    },
    // Tests wait for admission, then release explicitly. No timing/reply-order
    // assumptions; all calls made while held share the same barrier.
    hold() {
      let announce!: () => void, release!: () => void;
      const started = new Promise<void>(resolve => {
        announce = resolve;
      });
      const wait = new Promise<void>(resolve => {
        release = resolve;
      });
      barrier = { started: announce, wait, release };
      return {
        started,
        release: () => {
          release();
          barrier = undefined;
        }
      };
    }
  });
}
