export type ApplicationItem = {
  monitor?: { applicationId: string | null; applicationName: string | null };
  applicationId?: string | null;
  applicationName?: string | null;
};

export function groupByApplication<T extends ApplicationItem>(
  items: T[],
): { name: string; items: T[] }[] {
  const groups: { name: string; id: string | null; items: T[] }[] = [];
  for (const item of items) {
    const id = item.monitor?.applicationId ?? item.applicationId ?? null;
    let group = groups.find((candidate) => candidate.id === id);
    if (!group) {
      group = {
        id,
        name: item.monitor?.applicationName ?? item.applicationName ?? 'No application',
        items: [],
      };
      groups.push(group);
    }
    group.items.push(item);
  }
  return groups.sort((a, b) =>
    a.id === null ? 1 : b.id === null ? -1 : a.name.localeCompare(b.name),
  );
}
