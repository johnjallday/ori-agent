// One in-memory return slot. No browser storage, conversation creation or Send.
export function createReviewVisit({ capture, navigate, fresh, restore, focus, changed, notify }) {
  let origin = null;
  let busy = false;
  let epoch = 0;
  const same = (a, b) =>
    a &&
    b &&
    a.id === b.id &&
    a.text === b.text &&
    a.folder?.generation === b.folder?.generation &&
    a.link?.generation === b.link?.generation;
  return {
    current: () => origin,
    clear() {
      epoch++;
      origin = null;
      changed(null);
    },
    async open(id) {
      if (busy || !id) return false;
      const saved = capture();
      if (!saved || saved.text.length > 32768) {
        notify('Keep this draft here until it can be safely left.');
        return false;
      }
      if (saved.id === id) {
        focus();
        return true;
      }
      if (origin) {
        notify('Return to the original folder before opening another review.');
        return false;
      }
      busy = true;
      const ticket = ++epoch;
      try {
        const ok = await navigate(id, () => ticket === epoch && same(saved, capture()));
        if (!ok || ticket !== epoch) return false;
        origin = saved;
        changed(origin);
        focus();
        return true;
      } finally {
        busy = false;
      }
    },
    async back() {
      if (busy || !origin) return false;
      const saved = origin;
      const leaving = capture();
      if (!leaving || leaving.text || leaving.folder?.preview || leaving.link?.active) {
        notify(
          'Keep or clear the draft in this review before returning; both drafts are still here.'
        );
        return false;
      }
      busy = true;
      const ticket = ++epoch;
      const guard = () => ticket === epoch && same(leaving, capture());
      try {
        const ok = saved.id ? await navigate(saved.id, guard) : guard() && fresh();
        if (!ok || ticket !== epoch) return false;
        await restore(saved);
        origin = null;
        changed(null);
        return true;
      } finally {
        busy = false;
      }
    }
  };
}
