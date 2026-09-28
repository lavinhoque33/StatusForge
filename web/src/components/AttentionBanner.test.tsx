import { act, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { jsonResponse, stubApi } from '../test/fixtures';
import { AttentionBanner } from './AttentionBanner';
import { IncidentsPage } from '../pages/IncidentsPage';

const received = {
  id: 'i:opened',
  kind: 'opened',
  reminderSeq: null,
  state: 'failed',
  createdAt: '2026-09-27T10:00:00.000Z',
  nextAttemptAt: null,
  deliveredAt: null,
  failedAt: '2026-09-27T10:00:00.000Z',
  cancelledReason: null,
  attempts: [],
  monitorId: 'm',
  monitorName: 'M',
  incidentId: 'i',
};
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  window.history.replaceState(null, '', '/');
});

it('appears and disappears with visible polls, without hidden-tab requests', async () => {
  vi.useFakeTimers();
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  let reads = 0;
  stubApi({
    'GET /api/notifications/attention?limit=200': () =>
      jsonResponse({ notifications: reads++ === 1 ? [received] : [] }),
  });
  render(<AttentionBanner />);
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
  expect(screen.queryByText(/notification could not be delivered/)).not.toBeInTheDocument();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15_000);
  });
  expect(
    screen.getByRole('link', { name: '1 notification could not be delivered' }),
  ).toHaveAttribute('href', '/incidents#notifications-attention');
  expect(within(screen.getByRole('status')).getByRole('link')).toHaveTextContent('1 notification');
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15_000);
  });
  expect(reads).toBe(2);
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  await act(async () => {
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(20);
  });
  expect(screen.queryByText(/notification could not be delivered/)).not.toBeInTheDocument();
  expect(reads).toBe(3);
});
it('marks a full attention page as 200+ in both banner and list', async () => {
  const notifications = Array.from({ length: 200 }, (_, index) => ({
    ...received,
    id: `i${index}:opened`,
    incidentId: `i${index}`,
  }));
  stubApi({
    'GET /api/notifications/attention?limit=200': () => jsonResponse({ notifications }),
    'GET /api/incidents?state=all&limit=50': () => jsonResponse({ incidents: [] }),
  });
  render(
    <>
      <AttentionBanner />
      <IncidentsPage />
    </>,
  );
  expect(
    await screen.findByRole('link', { name: '200+ notifications could not be delivered' }),
  ).toBeInTheDocument();
  expect(
    await screen.findByRole('heading', { name: 'Failed notifications needing attention (200+)' }),
  ).toBeInTheDocument();
});

it('focuses and scrolls to the attention heading after initial hash navigation loads data', async () => {
  window.history.replaceState(null, '', '/incidents#notifications-attention');
  const scroll = vi.fn();
  const originalScroll = Element.prototype.scrollIntoView;
  Object.defineProperty(Element.prototype, 'scrollIntoView', { value: scroll, configurable: true });
  try {
    stubApi({
      'GET /api/notifications/attention?limit=200': () =>
        jsonResponse({ notifications: [received] }),
      'GET /api/incidents?state=all&limit=50': () => jsonResponse({ incidents: [] }),
    });
    render(<IncidentsPage />);
    const heading = await screen.findByRole('heading', {
      name: 'Failed notifications needing attention',
    });
    await waitFor(() => expect(heading).toHaveFocus());
    expect(scroll).toHaveBeenCalledTimes(1);
    expect(scroll).toHaveBeenCalledWith();
  } finally {
    Object.defineProperty(Element.prototype, 'scrollIntoView', {
      value: originalScroll,
      configurable: true,
    });
  }
});
