import type { ReactNode } from 'react';

/** Keyboard-accessible horizontal scrolling for wide data tables. */
export function ScrollRegion({
  labelledBy,
  children,
}: {
  labelledBy: string;
  children: ReactNode;
}) {
  return (
    <div className="table-wrapper" tabIndex={0} role="region" aria-labelledby={labelledBy}>
      {children}
    </div>
  );
}
