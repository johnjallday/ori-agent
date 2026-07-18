#!/usr/bin/env node

import net from 'node:net';

const server = net.createServer();
server.unref();
server.on('error', (error) => {
  process.stderr.write(`Could not choose a free local port: ${error.message}\n`);
  process.exitCode = 2;
});
server.listen({ host: '127.0.0.1', port: 0 }, () => {
  const address = server.address();
  if (!address || typeof address === 'string') {
    process.stderr.write('Could not determine the selected local port.\n');
    process.exitCode = 2;
    server.close();
    return;
  }
  process.stdout.write(`${address.port}\n`);
  server.close();
});
