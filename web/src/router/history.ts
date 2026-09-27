/**
 * Minimal History-API routing.
 *
 * M1 has three routes and no loaders, nested layouts, or server rendering, so a
 * subscription over `history` keeps the dependency surface empty and small
 * enough to audit. Links stay real anchors (see `Link`), so middle-click,
 * modifier-click, and copy-link keep working.
 */
import { useSyncExternalStore } from 'react';

const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  window.addEventListener('popstate', listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener('popstate', listener);
  };
}

/** Current history pathname; re-renders on client navigation and `popstate`. */
export function usePathname(): string {
  return useSyncExternalStore(
    subscribe,
    () => window.location.pathname,
    () => '/',
  );
}

/** Push a same-origin path and notify subscribers (`pushState` is silent). */
export function navigate(to: string): void {
  if (to !== window.location.pathname) {
    window.history.pushState(null, '', to);
  }
  for (const listener of listeners) listener();
}
