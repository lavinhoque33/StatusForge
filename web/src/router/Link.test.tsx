import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { Link } from './Link';
import { usePathname } from './history';
import { matchRoute } from './routes';

function Harness() {
  const pathname = usePathname();
  const route = matchRoute(pathname);
  return (
    <>
      <Link to="/monitors/new">New monitor</Link>
      <Link to="/monitors/new" target="_blank">
        New monitor in a tab
      </Link>
      <p>Route: {route.name}</p>
    </>
  );
}

afterEach(() => {
  window.history.pushState(null, '', '/');
});

describe('Link', () => {
  it('exposes a real href so the address can be copied or opened normally', () => {
    render(<Harness />);
    expect(screen.getByRole('link', { name: 'New monitor' })).toHaveAttribute(
      'href',
      '/monitors/new',
    );
  });

  it('navigates client-side on a plain click', () => {
    render(<Harness />);
    expect(screen.getByText('Route: monitors')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('link', { name: 'New monitor' }));

    expect(window.location.pathname).toBe('/monitors/new');
    expect(screen.getByText('Route: create-monitor')).toBeInTheDocument();
  });

  it('re-renders when the browser reports a history change', () => {
    window.history.pushState(null, '', '/monitors/new');
    render(<Harness />);
    expect(screen.getByText('Route: create-monitor')).toBeInTheDocument();

    act(() => {
      window.history.pushState(null, '', '/');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });

    expect(screen.getByText('Route: monitors')).toBeInTheDocument();
  });

  it('leaves activations it does not own to the browser', () => {
    render(<Harness />);

    // `_blank` belongs to a browsing context this suite does not own.
    fireEvent.click(screen.getByRole('link', { name: 'New monitor in a tab' }));
    expect(window.location.pathname).toBe('/');

    // A modifier click opens a new tab; a same-document href keeps jsdom from
    // attempting a full navigation while the handler still runs.
    const link = screen.getByRole('link', { name: 'New monitor' });
    link.setAttribute('href', '#');
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true });
    link.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(false);
    expect(window.location.pathname).toBe('/');
  });
});
