import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import { BackendStatus } from './BackendStatus';

const READY_BODY = {
  status: 'ready',
  checkedAt: '2026-09-27T12:00:00.000Z',
  dependencies: { dynamodb: { status: 'ready' } },
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubFetch(): Mock<typeof fetch> {
  const fetchMock = vi.fn<typeof fetch>();
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('BackendStatus', () => {
  it('starts by checking', async () => {
    const fetchMock = stubFetch();
    let release: (response: Response) => void = () => {};
    fetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          release = resolve;
        }),
    );

    render(<BackendStatus />);

    expect(screen.getByRole('status')).toHaveTextContent('Checking...');
    expect(screen.getByRole('heading', { name: 'Backend status' })).toBeInTheDocument();

    await act(async () => {
      release(jsonResponse(READY_BODY));
    });
  });

  it('reports a ready backend with its dependencies and check time', async () => {
    stubFetch().mockImplementation(async () => jsonResponse(READY_BODY));

    render(<BackendStatus />);

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Ready'));
    expect(screen.getByRole('listitem')).toHaveTextContent('dynamodb: Ready');

    const expectedTime = new Date(READY_BODY.checkedAt).toLocaleTimeString([], { hour12: false });
    expect(screen.getByText(/Checked at/)).toHaveTextContent(
      `Checked at ${expectedTime} (local time)`,
    );
    expect(screen.getByText(expectedTime)).toHaveAttribute('datetime', READY_BODY.checkedAt);
  });

  it('shows the observation instant the backend reported, not the client clock', async () => {
    const observedAt = '2001-02-03T04:05:06.000Z';
    stubFetch().mockImplementation(async () =>
      jsonResponse({ ...READY_BODY, checkedAt: observedAt }),
    );

    render(<BackendStatus />);

    const expectedTime = new Date(observedAt).toLocaleTimeString([], { hour12: false });
    await waitFor(() =>
      expect(screen.getByText(/Checked at/)).toHaveTextContent(
        `Checked at ${expectedTime} (local time)`,
      ),
    );
    expect(screen.getByText(expectedTime)).toHaveAttribute('datetime', observedAt);
  });

  it('reports a degraded backend with the dependency failure reason', async () => {
    stubFetch().mockImplementation(async () =>
      jsonResponse(
        {
          status: 'degraded',
          checkedAt: '2026-09-27T12:00:00.000Z',
          dependencies: { dynamodb: { status: 'unavailable', reason: 'timeout' } },
        },
        503,
      ),
    );

    render(<BackendStatus />);

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Degraded'));
    expect(screen.getByRole('listitem')).toHaveTextContent('dynamodb: Unavailable (timed out)');
  });

  it('reports an unreachable backend when the request fails', async () => {
    stubFetch().mockRejectedValue(new TypeError('Failed to fetch'));

    render(<BackendStatus />);

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Unreachable'));
    expect(screen.queryByRole('listitem')).not.toBeInTheDocument();
    expect(screen.queryByText(/Checked at/)).not.toBeInTheDocument();
  });

  it('reports an unexpected response for an invalid payload', async () => {
    stubFetch().mockImplementation(async () => jsonResponse({ status: 'nonsense' }));

    render(<BackendStatus />);

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent('Unexpected response'),
    );
  });

  it('reports a timeout when the backend does not answer within 5 s', async () => {
    vi.useFakeTimers();
    stubFetch().mockImplementation(() => new Promise<Response>(() => {}));

    render(<BackendStatus />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4999);
    });
    expect(screen.getByRole('status')).toHaveTextContent('Checking...');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(screen.getByRole('status')).toHaveTextContent('Timed out after 5 s');

    // No polling: nothing else is scheduled after the deadline.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60000);
    });
    expect(screen.getByRole('status')).toHaveTextContent('Timed out after 5 s');
    expect(vi.mocked(fetch)).toHaveBeenCalledTimes(1);
  });

  it('aborts the in-flight request when checking again', async () => {
    const signals: AbortSignal[] = [];
    stubFetch().mockImplementation((_input, init) => {
      const { signal } = init ?? {};
      if (signal) signals.push(signal);
      return signals.length === 1
        ? new Promise<Response>(() => {})
        : Promise.resolve(jsonResponse(READY_BODY));
    });

    render(<BackendStatus />);
    await waitFor(() => expect(signals).toHaveLength(1));
    expect(signals[0].aborted).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: 'Check again' }));

    expect(signals[0].aborted).toBe(true);
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Ready'));
    expect(signals).toHaveLength(2);
    expect(signals[1].aborted).toBe(false);
  });

  it('aborts the in-flight request on unmount', async () => {
    const signals: AbortSignal[] = [];
    stubFetch().mockImplementation((_input, init) => {
      const { signal } = init ?? {};
      if (signal) signals.push(signal);
      return new Promise<Response>(() => {});
    });

    const { unmount } = render(<BackendStatus />);
    await waitFor(() => expect(signals).toHaveLength(1));
    expect(signals[0].aborted).toBe(false);

    unmount();

    expect(signals[0].aborted).toBe(true);
  });
});
