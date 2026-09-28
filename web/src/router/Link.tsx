import type { AnchorHTMLAttributes, MouseEvent, ReactNode } from 'react';
import { navigate } from './history';

type LinkProps = {
  to: string;
  children: ReactNode;
} & Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'href'>;

/**
 * Anchor that navigates client-side for plain left clicks.
 *
 * Modifier clicks, middle clicks, and an explicit `target` keep the browser's
 * own behaviour so "open in new tab" and copy-link are not broken.
 */
export function Link({ to, children, onClick, target, ...rest }: LinkProps) {
  const handleClick = (event: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(event);
    if (event.defaultPrevented || event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    if (target !== undefined && target !== '_self') return;
    // Native fragment navigation scrolls and focuses the destination. A
    // pushState-only route transition cannot do that when the section mounts.
    if (to.includes('#')) return;
    event.preventDefault();
    navigate(to);
  };

  return (
    <a href={to} target={target} onClick={handleClick} {...rest}>
      {children}
    </a>
  );
}

export default Link;
