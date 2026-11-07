import { createServer } from 'node:http';

type Evidence = { id: string; kind: string; value: string };
type Candidate = { handle: string; evidence: Evidence[] };
type Input = {
  roster: { candidates: Candidate[] };
  roles: { id: string; target_id: string; occupied?: boolean; required_skills?: string[] }[];
};

// Synthetic provider boundary only. Ori's roster, builder, persistence and HTTP
// routes remain real. Prompts are parsed in memory and never logged/retained.
export async function startRosterStaffingModel(port = 8944) {
  let mode: 'recommended' | 'needs_choice' | 'no_suitable_match' | 'unavailable' = 'recommended';
  let workspaceName = 'Synthetic Staffing Review';
  let purpose = 'Builds and reviews software';
  let calls = 0;
  const observedEvidence = new Set<string>();
  let held: Promise<void> | null = null;
  let releaseHeld: (() => void) | null = null;
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
    let body = '';
    for await (const chunk of request) {
      body += chunk.toString();
      if (body.length > 256 * 1024) {
        response.writeHead(413);
        response.end('{}');
        return;
      }
    }
    const payload = JSON.parse(body);
    const system = (payload.messages || [])
      .filter((m: { role: string }) => m.role === 'system')
      .map((m: { content: string }) => m.content)
      .join('\n');
    const isBuild = system.includes('Right now you are setting up a new workspace');
    const isHome = system.startsWith('Recommend staffing, never assign or create anything.');
    if (isBuild || isHome) calls++;
    if (isHome && held) {
      const wait = held;
      held = null;
      await wait;
    }
    if ((isBuild || isHome) && ((payload.tools || []).length || mode === 'unavailable')) {
      response.writeHead(503);
      response.end(
        JSON.stringify({ error: { message: 'Synthetic unavailable', type: 'fixture' } })
      );
      return;
    }
    const data = system.match(/<staffing_data>([\s\S]*?)<\/staffing_data>/)?.[1];
    const input: Input | undefined = data ? JSON.parse(data) : undefined;
    for (const profile of input?.roster.candidates || []) {
      for (const fact of profile.evidence || []) observedEvidence.add(fact.value);
    }
    const candidate = input?.roster.candidates.find(c =>
      c.evidence.some(e => e.value.includes(purpose))
    );
    const roles = input?.roles.filter(r => r.target_id === 'code-project') || [];
    const reply = {
      say:
        mode === 'no_suitable_match'
          ? 'No suitable saved match was found. Choose manually or deliberately create a new agent.'
          : 'Review the saved software specialist suggestion; nothing has been assigned.',
      ask: { question: 'Would you like to review the team?', choices: [], allow_free_text: true },
      patch: {
        set: ['blueprint_id', 'name', 'description'],
        blueprint_id: 'code-project',
        name: workspaceName,
        description: 'Build and review software',
        goal: '',
        inputs: [],
        parent_id: '',
        ask_folder: false,
        team: { mode: '', agents: [], roles: [], saved_agents: [] },
        tags: [],
        color: ''
      },
      why: [],
      alternatives: [],
      ready: false,
      create_now: false,
      staffing: {
        roles:
          candidate || mode === 'no_suitable_match'
            ? roles.map(r => ({
                role_id: r.id,
                state: mode,
                choices:
                  mode === 'no_suitable_match'
                    ? []
                    : [
                        {
                          candidate: candidate!.handle,
                          evidence: [
                            candidate!.evidence.find(e => e.kind === 'declared_purpose')!.id
                          ]
                        }
                      ]
              }))
            : []
      }
    };
    const homeReply = {
      roles: (input?.roles || [])
        .filter(role => !role.occupied)
        .map(role => {
          const selected =
            candidate && role.id === 'portfolio_manager' && mode !== 'no_suitable_match';
          const fact =
            candidate?.evidence.find(e => e.kind === 'declared_purpose') ||
            candidate?.evidence.find(
              e => e.kind === 'enabled_skill' && role.required_skills?.includes(e.value)
            );
          return {
            role_id: role.id,
            state: selected && fact ? mode : mode === 'no_suitable_match' ? mode : 'needs_choice',
            choices: selected && fact ? [{ candidate: candidate.handle, evidence: [fact.id] }] : []
          };
        })
    };
    response.end(
      JSON.stringify({
        id: 'staffing-fixture',
        object: 'chat.completion',
        model: 'ori-synthetic-model',
        choices: [
          {
            index: 0,
            message: {
              role: 'assistant',
              content: JSON.stringify(
                isHome
                  ? homeReply
                  : isBuild
                    ? reply
                    : { text: 'Synthetic background response; no work requested.' }
              )
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
    server.listen(port, '127.0.0.1', resolve);
  });
  return {
    mode(value: typeof mode) {
      mode = value;
    },
    workspaceName(value: string) {
      workspaceName = value;
    },
    candidatePurpose(value: string) {
      purpose = value;
    },
    calls: () => calls,
    sawEvidence: (value: string) => observedEvidence.has(value),
    holdNextRecommendation: () => {
      held = new Promise<void>(resolve => {
        releaseHeld = resolve;
      });
      return () => {
        releaseHeld?.();
        releaseHeld = null;
      };
    },
    stop: () =>
      new Promise<void>((resolve, reject) => {
        releaseHeld?.();
        releaseHeld = null;
        server.close(error => (error ? reject(error) : resolve()));
      })
  };
}
