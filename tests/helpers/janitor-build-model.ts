import { createServer } from 'node:http';

// The only synthetic integration in reviewed-creation acceptance: an actual
// local OpenAI-compatible model endpoint. Build sessions, held selections,
// normal creator gates, grants, monitoring and scans all use the real server.
// Launch the isolated app with LM_STUDIO_BASE_URL=http://127.0.0.1:8940/v1
// and LM_STUDIO_MODEL=ori-synthetic-model, then enable ORI_TEST_BUILD_CREATION=1.
export async function startJanitorBuildModel() {
  let sequence = 0;
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
    // Drain the request without logging prompts/metadata or consulting a real model.
    for await (const _ of request) {
      /* intentional drain */
    }
    sequence++;
    const suffix = `${Date.now()}-${sequence}`;
    const reply = {
      say: 'Ready to create. Use Review & create to check the draft and permissions.',
      ask: { question: '', choices: [], allow_free_text: true },
      patch: {
        set: ['blueprint_id', 'name', 'description', 'goal', 'team'],
        blueprint_id: 'file-janitor',
        name: `Synthetic Janitor ${suffix}`,
        description: 'Synthetic folder review fixture',
        goal: 'Review synthetic inbox files',
        inputs: [],
        parent_id: '',
        ask_folder: false,
        tags: [],
        color: '',
        team: {
          mode: 'staffed',
          roles: [],
          saved_agents: [],
          agents: [
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
      ready: true,
      create_now: false
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
            message: { role: 'assistant', content: JSON.stringify(reply) },
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
  return async () =>
    new Promise<void>((resolve, reject) =>
      server.close(error => (error ? reject(error) : resolve()))
    );
}
