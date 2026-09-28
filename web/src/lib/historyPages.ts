import type { HistoryPage } from '../api/monitors';

/** Head refresh replaces only the head slice; older pages remain anchored by their cursor. */
export function mergeHistory<T extends { id: string }>(
  head: HistoryPage<T>,
  older: T[],
  previousCursor: string | null,
  olderLoaded: boolean,
): { items: T[]; nextCursor: string | null } {
  const seen = new Set<string>();
  const items = [...head.items, ...older].filter((item) => {
    if (seen.has(item.id)) return false;
    seen.add(item.id);
    return true;
  });
  return { items, nextCursor: olderLoaded ? previousCursor : head.nextCursor };
}
