import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Gap, Observation } from '../api/monitors';
import { gapFixture, observationFixture } from '../test/fixtures';
import { TimelineTable } from './TimelineTable';
import { mergeTimeline } from '../lib/timeline';

function observationAt(startedAt: string, overrides: Partial<Observation> = {}): Observation {
  return observationFixture({
    startedAt,
    completedAt: startedAt,
    counted: true,
    ...overrides,
  });
}

function gapFrom(fromDueAt: string, overrides: Partial<Gap> = {}): Gap {
  return gapFixture({ fromDueAt, toDueAt: fromDueAt, ...overrides });
}

describe('mergeTimeline', () => {
  it('orders observations and gaps newest first by their own instants', () => {
    const timeline = mergeTimeline(
      [
        observationAt('2026-09-27T10:15:00.000Z', { id: 'o1' }),
        observationAt('2026-09-27T10:05:00.000Z', { id: 'o2' }),
      ],
      [gapFrom('2026-09-27T10:10:00.000Z', { id: 'g1' })],
    );

    expect(timeline.map((entry) => entry.key)).toEqual(['o1', 'g1', 'o2']);
  });

  it('places a gap whose range ends at an observation time by its newest slot', () => {
    // The gap's newest missed slot is 10:10; it belongs before the 10:05 row.
    const timeline = mergeTimeline(
      [observationAt('2026-09-27T10:05:00.000Z', { id: 'o1' })],
      [gapFrom('2026-09-27T10:00:00.000Z', { id: 'g1', toDueAt: '2026-09-27T10:10:00.000Z' })],
    );

    expect(timeline.map((entry) => entry.key)).toEqual(['g1', 'o1']);
  });

  it('keeps two adjacent gaps as two rows', () => {
    const timeline = mergeTimeline(
      [],
      [
        gapFrom('2026-09-27T10:10:00.000Z', { id: 'g1' }),
        gapFrom('2026-09-27T10:05:00.000Z', { id: 'g2' }),
      ],
    );

    expect(timeline.map((entry) => entry.key)).toEqual(['g1', 'g2']);
  });
});

describe('TimelineTable', () => {
  it('renders gap rows with the contract wording and the covered range', () => {
    render(
      <TimelineTable
        observations={[]}
        gaps={[
          gapFrom('2026-09-27T10:00:00.000Z', {
            id: 'g1',
            toDueAt: '2026-09-27T10:30:00.000Z',
            missedCount: 4,
            reason: 'not_scheduled',
          }),
        ]}
      />,
    );

    const row = screen.getByRole('row', { name: /Missed 4 checks/ });
    expect(row).toHaveTextContent('Missed 4 checks');
    expect(row).toHaveTextContent('StatusForge was not running');
    expect(row).toHaveTextContent('Gap');
  });

  it('renders the singular for a single missed slot', () => {
    render(<TimelineTable observations={[]} gaps={[gapFixture({ missedCount: 1 })]} />);

    expect(screen.getByRole('row', { name: /Missed 1 check —/ })).toBeInTheDocument();
  });

  it('marks not-counted observations with the reason in words', () => {
    render(
      <TimelineTable
        observations={[
          observationAt('2026-09-27T10:15:00.000Z', {
            id: 'o1',
            counted: false,
            notCountedReason: 'paused',
          }),
        ]}
        gaps={[]}
      />,
    );

    const row = screen.getByRole('row', { name: /Not counted/ });
    expect(row).toHaveTextContent('Not counted — monitor was paused');
  });

  it('shows the initiator for scheduled and manual observations', () => {
    render(
      <TimelineTable
        observations={[
          observationAt('2026-09-27T10:15:00.000Z', {
            id: 'o1',
            initiatedBy: 'scheduled',
            trigger: 'schedule',
            dueAt: '2026-09-27T10:15:00.000Z',
          }),
          observationAt('2026-09-27T10:14:00.000Z', {
            id: 'o2',
            initiatedBy: 'manual',
            outcome: 'failing',
          }),
        ]}
        gaps={[]}
      />,
    );

    expect(screen.getByRole('row', { name: /scheduled/ })).toHaveTextContent('scheduled');
    expect(screen.getByRole('row', { name: /Failing/ })).toHaveTextContent('manual');
  });

  it('labels observations and presents an ended window at both boundaries in time order', () => {
    render(
      <TimelineTable
        observations={[observationAt('2026-09-27T10:30:00.000Z', { maintenanceWindowId: 'w1' })]}
        gaps={[]}
        windows={[
          {
            id: 'w1',
            monitorId: 'monitor-1',
            startAt: '2026-09-27T10:00:00.000Z',
            endAt: '2026-09-27T11:00:00.000Z',
            note: '',
            createdAt: '2026-09-27T09:00:00.000Z',
            cancelledAt: null,
            state: 'ended',
          },
        ]}
      />,
    );
    const rows = screen.getAllByRole('row');
    expect(rows[1]).toHaveTextContent('Maintenance ended');
    expect(rows[2]).toHaveTextContent('Healthy · Maintenance');
    expect(rows[3]).toHaveTextContent('Maintenance started');
  });

  it('renders the empty state when nothing is recorded', () => {
    render(<TimelineTable observations={[]} gaps={[]} />);

    expect(screen.getByText('No checks have been recorded for this monitor.')).toBeInTheDocument();
  });

  it('keeps a mixed timeline readable with the newest entry first', () => {
    render(
      <TimelineTable
        observations={[
          observationAt('2026-09-27T10:15:00.000Z', { id: 'o1', initiatedBy: 'scheduled' }),
          observationAt('2026-09-27T09:55:00.000Z', { id: 'o3' }),
        ]}
        gaps={[gapFrom('2026-09-27T10:05:00.000Z', { id: 'g1' })]}
      />,
    );

    const rows = screen.getAllByRole('row');
    // Header row first, then newest to oldest. The rendered time is local, so
    // assert on the machine-readable `datetime` instead.
    expect(rows[1]).toContainHTML('datetime="2026-09-27T10:15:00.000Z"');
    expect(rows[2]).toHaveTextContent('Missed 4 checks');
    expect(rows[3]).toContainHTML('datetime="2026-09-27T09:55:00.000Z"');
  });

  it('labels every row with its kind for the stacked layout', () => {
    render(
      <TimelineTable
        observations={[observationAt('2026-09-27T10:15:00.000Z', { id: 'o1' })]}
        gaps={[gapFixture({ id: 'g1' })]}
      />,
    );

    const gapRow = screen.getByRole('row', { name: /Missed 4 checks/ });
    expect(gapRow).toHaveTextContent('Gap');
    expect(screen.getAllByRole('row')).toHaveLength(3);
  });

  it('renders the check request columns for observations', () => {
    render(
      <TimelineTable
        observations={[
          observationAt('2026-09-27T10:15:00.000Z', {
            id: 'o1',
            durationMs: 12,
          }),
        ]}
        gaps={[]}
      />,
    );

    const row = screen.getByRole('row', { name: /Healthy/ });
    expect(row).toHaveTextContent('200');
    expect(row).toHaveTextContent('12 ms');
    expect(row).toHaveTextContent('v1');
  });
});
