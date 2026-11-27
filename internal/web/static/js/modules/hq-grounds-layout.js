// Three avenues. The middle two lots in front of the tower are the HQ and
// its lead. Coordinates in storage remain fractions of the *whole* field.
const LOTS = [
  'memory',
  'watchtower',
  'second-brain',
  'daily-brief',
  null,
  'file-janitor',
  'email',
  null,
  'calendar-ops'
];
const avenues = [0.17, 0.5, 0.83];

export function groundsLayout(stations, saved = {}) {
  const keys = [...new Set(stations.map(station => station.key))];
  const clamp = n => Math.min(1, Math.max(0, n));
  const arranged = new Map();
  for (const key of keys) {
    const point = saved?.[key];
    if (Number.isFinite(point?.x) && Number.isFinite(point?.y)) {
      arranged.set(key, { x: clamp(point.x), y: clamp(point.y) });
    }
  }
  const occupied = [...arranged.values()];
  // Lay out untouched stations without measuring the DOM or consulting their
  // state. Pin known landmarks first; a displaced or new station takes a free
  // lot, growing the grounds by a row if saved positions crowd the field.
  for (let rows = Math.max(3, Math.ceil((keys.length + 2) / 3)); ; rows++) {
    const positions = new Map(arranged);
    const lots = Array.from({ length: rows * 3 }, (_, i) => ({
      x: avenues[i % 3],
      y: (Math.floor(i / 3) + 0.5) / rows,
      i
    })).filter(
      lot =>
        lot.i !== 4 &&
        lot.i !== 7 &&
        !occupied.some(
          point => Math.abs(point.x - lot.x) < 0.33 && Math.abs(point.y - lot.y) < 0.78 / rows
        )
    );
    const occupy = (key, index) => {
      const [{ x, y }] = lots.splice(index, 1);
      positions.set(key, { x, y });
    };
    for (const key of keys) {
      const index = lots.findIndex(lot => LOTS[lot.i] === key);
      if (!positions.has(key) && index >= 0) occupy(key, index);
    }
    for (const key of keys) if (!positions.has(key) && lots.length) occupy(key, 0);
    if (positions.size === keys.length) return { rows, positions };
  }
}
