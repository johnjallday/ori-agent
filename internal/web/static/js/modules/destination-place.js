// Only this opaque browsing-context handle crosses documents. Do not put a
// filename, query, result set or workspace snapshot in window.name or storage.
const prefix = 'ori-destination:';
const handlePattern =
  /^ori-destination:([0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})$/;
export function destinationPlace(surface) {
  if (surface !== 'map' && surface !== 'desk' && surface !== 'files')
    throw new Error('Unknown destination');
  if (window !== window.top) return null;
  if (!window.name && typeof crypto.randomUUID === 'function')
    window.name = prefix + crypto.randomUUID();
  const tab = window.name.match(handlePattern)?.[1];
  if (!tab) return null; // Respect caller-owned named targets.
  const send = place =>
    fetch(`/api/file-search/${surface}-place`, {
      method: 'POST',
      cache: 'no-store',
      referrerPolicy: 'no-referrer',
      keepalive: !!place,
      headers: { 'Content-Type': 'application/json', 'X-Ori-Search': '1' },
      body: JSON.stringify({ tab, place })
    });
  return {
    async read() {
      try {
        const response = await send(undefined);
        return response.ok ? await response.json() : null;
      } catch {
        return null;
      }
    },
    save(place) {
      return send(place).catch(() => {});
    }
  };
}
