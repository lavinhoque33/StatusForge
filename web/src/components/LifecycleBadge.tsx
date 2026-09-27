import type { Lifecycle } from '../api/monitors';

const LIFECYCLE_LABELS: Record<Lifecycle, string> = {
  active: 'Active',
  paused: 'Paused',
  archived: 'Archived',
};

/** Lifecycle as a word, never as colour alone. */
export function LifecycleBadge({ lifecycle }: { lifecycle: Lifecycle }) {
  return <span className={`lifecycle lifecycle--${lifecycle}`}>{LIFECYCLE_LABELS[lifecycle]}</span>;
}

export default LifecycleBadge;
