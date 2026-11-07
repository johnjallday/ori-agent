// Test-process controller only. Never registered on Ori, included in its env,
// or offered to a model. All actions are fixed callbacks over owned fixtures.
import http from 'node:http';
import { randomBytes } from 'node:crypto';

export async function startLearningDemoControl({ provider, restart }) {
  const token = randomBytes(32).toString('hex');
  let restarting = false;
  const server = http.createServer(async (request, response) => {
    response.setHeader('Content-Type', 'application/json');
    response.setHeader('Cache-Control', 'no-store');
    if (request.headers['x-ori-demo-control'] !== token) {
      response.writeHead(403).end('{}');
      return;
    }
    try {
      let bytes = 0;
      for await (const chunk of request) bytes += chunk.length;
      if (bytes) throw new Error('No control body accepted');
      if (request.method === 'GET' && request.url === '/state') {
        response.end(JSON.stringify({ held: provider.held(), calls: provider.calls() }));
        return;
      }
      if (request.method !== 'POST' || !['/hold', '/release', '/restart'].includes(request.url)) {
        response.writeHead(404).end('{}');
        return;
      }
      if (restarting) {
        response.writeHead(409).end('{}');
        return;
      }
      if (request.url === '/hold') provider.holdNext();
      if (request.url === '/release') provider.releaseHeld();
      if (request.url === '/restart') {
        if (provider.held()) throw new Error('Release the held reply before restart');
        restarting = true;
        try {
          await restart();
        } finally {
          restarting = false;
        }
      }
      response.end('{}');
    } catch {
      response.writeHead(409).end('{}');
    }
  });
  await new Promise((done, fail) => {
    server.once('error', fail);
    server.listen(0, '127.0.0.1', done);
  });
  return {
    base: `http://127.0.0.1:${server.address().port}`,
    token,
    close: async () => {
      server.closeAllConnections();
      await new Promise(done => server.close(done));
    }
  };
}
