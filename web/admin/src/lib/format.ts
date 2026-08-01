/**
 * A manifest's identity is its hash. Numbering it 01/02/03 would be
 * decoration; the hash is what the gateway logs, what the diff is taken
 * against, and what an auditor would compare. Eight hex characters is enough
 * to tell two manifests apart by eye and short enough to sit in a table.
 */
export function shortHash(hash: string): string {
  return hash.slice(0, 8);
}

const absolute = new Intl.DateTimeFormat(undefined, {
  year: 'numeric',
  month: 'short',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
});

/** Full timestamps: this is a record, and a record carries when. */
export function formatTimestamp(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return absolute.format(at);
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** How long this has been waiting. Terse, for the queue. */
export function formatAge(iso: string, now: number = Date.now()): string {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return '—';
  const elapsed = Math.max(0, now - at);

  if (elapsed < MINUTE) return 'just now';
  if (elapsed < HOUR) return `${Math.floor(elapsed / MINUTE)}m`;
  if (elapsed < DAY) return `${Math.floor(elapsed / HOUR)}h`;
  return `${Math.floor(elapsed / DAY)}d`;
}

export function pluralise(count: number, one: string, many = `${one}s`): string {
  return count === 1 ? one : many;
}
