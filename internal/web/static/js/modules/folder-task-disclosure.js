// Inert, shared configuration/read-scope disclosure. No connection check or I/O.
export function folderTaskDisclosure(look) {
  const executor = look?.executor;
  const who =
    executor?.agent && executor?.provider && executor?.model
      ? `${String(executor.agent)} · ${String(executor.provider)} / ${String(executor.model)}. Configuration only; connection not verified.`
      : 'Executor/model configuration is unavailable. Refresh the task before starting.';
  const scope = String(look?.input_scope || 'Permitted inputs have not been reported.');
  return `Reads: ${scope}. Executor: ${who} Each explicit attempt uses model tokens. Read-only: the host gathers bounded data; no model tools, source-file changes or external actions are permitted.`;
}
