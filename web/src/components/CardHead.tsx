import type { ReactNode } from 'react';
import { Icon, type IconName } from './Icon';

type Tone = 'ok' | 'fail' | 'warn' | 'info' | 'neutral';

/**
 * Card title row: a decorative icon tile before the card's own heading.
 * `children` is the heading element itself, so its id, ref and focus
 * behaviour stay with the caller; the icon adds no accessible text.
 */
export function CardHead({
  icon,
  tone,
  children,
}: {
  icon: IconName;
  tone?: Tone;
  children: ReactNode;
}) {
  return (
    <div className="card-head">
      <span
        className={tone === undefined ? 'card-icon' : `card-icon tone-${tone}`}
        aria-hidden="true"
      >
        <Icon name={icon} size={16} />
      </span>
      {children}
    </div>
  );
}
