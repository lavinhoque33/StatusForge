import { useCallback, useEffect, useRef, useState } from 'react';
import {
  getGapPage,
  getObservationPage,
  OBSERVATION_LIMIT,
  type Gap,
  type Observation,
  type ObservationFilters,
  type MonitorKind,
} from '../api/monitors';
import type { Window } from '../api/maintenance';
import { isAbortError } from '../api/http';
import { describeApiError } from '../lib/errors';
import { mergeHistory } from '../lib/historyPages';
import { formatLocalWithOffset } from '../lib/time';
import { usePolling } from '../lib/usePolling';
import { TimelineTable } from './TimelineTable';

type Collection<T> = {
  items: T[];
  nextCursor: string | null;
  searchedThrough: string | null;
  loaded: boolean;
  olderLoaded: boolean;
};
const empty = <T,>(): Collection<T> => ({
  items: [],
  nextCursor: null,
  searchedThrough: null,
  loaded: false,
  olderLoaded: false,
});

export function MonitorHistory({
  monitorId,
  monitorKind,
  windows,
  configVersion,
  refreshToken = 0,
}: {
  monitorId: string;
  monitorKind: MonitorKind;
  windows: Window[];
  configVersion: number;
  refreshToken?: number;
}) {
  const [filters, setFilters] = useState<ObservationFilters>({});
  const [range, setRange] = useState({ from: '', to: '' });
  const [observations, setObservations] = useState<Collection<Observation>>(empty);
  const [gaps, setGaps] = useState<Collection<Gap>>(empty);
  const [error, setError] = useState('');
  const [pending, setPending] = useState<'observations' | 'gaps' | null>(null);
  const generation = useRef(0);
  const observationEnd = useRef<HTMLParagraphElement>(null);
  const gapEnd = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    if (observations.olderLoaded && observations.nextCursor === null)
      observationEnd.current?.focus();
  }, [observations.olderLoaded, observations.nextCursor]);
  useEffect(() => {
    if (gaps.olderLoaded && gaps.nextCursor === null) gapEnd.current?.focus();
  }, [gaps.olderLoaded, gaps.nextCursor]);

  const head = useCallback(
    async (signal?: AbortSignal, reset = false) => {
      const token = generation.current;
      try {
        const [obs, gap] = await Promise.all([
          getObservationPage(monitorId, OBSERVATION_LIMIT, filters, undefined, signal),
          getGapPage(monitorId, OBSERVATION_LIMIT, undefined, signal),
        ]);
        if (signal?.aborted || token !== generation.current) return;
        setObservations((previous) => {
          const merged =
            reset || !previous.loaded
              ? { items: obs.items, nextCursor: obs.nextCursor }
              : mergeHistory(obs, previous.items, previous.nextCursor, previous.olderLoaded);
          return {
            ...merged,
            searchedThrough:
              obs.items.length < OBSERVATION_LIMIT ? obs.searchedThrough : previous.searchedThrough,
            loaded: true,
            olderLoaded: reset ? false : previous.olderLoaded,
          };
        });
        setGaps((previous) => {
          const merged = previous.loaded
            ? mergeHistory(gap, previous.items, previous.nextCursor, previous.olderLoaded)
            : { items: gap.items, nextCursor: gap.nextCursor };
          return {
            ...merged,
            searchedThrough: gap.searchedThrough,
            loaded: true,
            olderLoaded: previous.olderLoaded,
          };
        });
        setError('');
      } catch (cause) {
        if (!isAbortError(cause) && token === generation.current) setError(describeApiError(cause));
      }
    },
    [monitorId, filters],
  );
  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(() => {
      if (!controller.signal.aborted) return head(controller.signal, true);
    });
    return () => controller.abort();
  }, [head]);
  const lastRefresh = useRef(refreshToken);
  useEffect(() => {
    if (lastRefresh.current === refreshToken) return;
    lastRefresh.current = refreshToken;
    const controller = new AbortController();
    void Promise.resolve().then(() => {
      if (!controller.signal.aborted) return head(controller.signal);
    });
    return () => controller.abort();
  }, [head, refreshToken]);
  usePolling({ refresh: () => head() });

  const older = async (which: 'observations' | 'gaps') => {
    const current = which === 'observations' ? observations : gaps;
    if (pending || current.nextCursor === null) return;
    const token = generation.current;
    setPending(which);
    try {
      if (which === 'observations') {
        const page = await getObservationPage(
          monitorId,
          OBSERVATION_LIMIT,
          filters,
          current.nextCursor,
        );
        if (token === generation.current)
          setObservations((previous) => ({
            ...mergeHistory({ ...page, items: previous.items }, page.items, page.nextCursor, true),
            searchedThrough:
              page.items.length < OBSERVATION_LIMIT
                ? page.searchedThrough
                : previous.searchedThrough,
            loaded: true,
            olderLoaded: true,
          }));
      } else {
        const page = await getGapPage(monitorId, OBSERVATION_LIMIT, current.nextCursor);
        if (token === generation.current)
          setGaps((previous) => ({
            ...mergeHistory({ ...page, items: previous.items }, page.items, page.nextCursor, true),
            searchedThrough: page.searchedThrough,
            loaded: true,
            olderLoaded: true,
          }));
      }
      if (token === generation.current) setError('');
    } catch (cause) {
      if (token === generation.current) setError(describeApiError(cause));
    } finally {
      if (token === generation.current) setPending(null);
    }
  };
  const from = range.from ? new Date(range.from).toISOString() : undefined;
  const to = range.to ? new Date(range.to).toISOString() : undefined;
  const hasFilters = Object.values(filters).some((value) => value !== undefined);
  const newestObservation = observations.items[0];
  const driftNote =
    !hasFilters && newestObservation && configVersion > newestObservation.configVersion
      ? `Configuration changed since the last check (v${newestObservation.configVersion} → v${configVersion}).`
      : null;
  return (
    <section className="panel" aria-labelledby="monitor-timeline-heading">
      <h3 id="monitor-timeline-heading">
        {monitorKind === 'heartbeat' ? 'Reports and gaps' : 'Checks and gaps'}
      </h3>
      {driftNote ? <p className="drift-note">{driftNote}</p> : null}
      <form
        className="history-filters"
        onSubmit={(event) => {
          event.preventDefault();
          if (from && to && from > to) {
            setError('From must be before or equal to to.');
            return;
          }
          generation.current += 1;
          setObservations(empty());
          setFilters((previous) => ({ ...previous, from, to }));
        }}
      >
        <label>
          Outcome{' '}
          <select
            value={filters.outcome ?? ''}
            onChange={(event) => {
              generation.current += 1;
              setObservations(empty());
              setFilters((previous) => ({ ...previous, outcome: event.target.value || undefined }));
            }}
          >
            <option value="">All outcomes</option>
            <option value="healthy">Healthy</option>
            <option value="failing">Failing</option>
            <option value="checker_problem">Checker problem</option>
          </select>
        </label>
        <label>
          Counted{' '}
          <select
            value={filters.counted === undefined ? '' : String(filters.counted)}
            onChange={(event) => {
              generation.current += 1;
              setObservations(empty());
              setFilters((previous) => ({
                ...previous,
                counted: event.target.value === '' ? undefined : event.target.value === 'true',
              }));
            }}
          >
            <option value="">All</option>
            <option value="true">Counted</option>
            <option value="false">Not counted</option>
          </select>
        </label>
        <label>
          Maintenance{' '}
          <select
            value={filters.maintenance === undefined ? '' : String(filters.maintenance)}
            onChange={(event) => {
              generation.current += 1;
              setObservations(empty());
              setFilters((previous) => ({
                ...previous,
                maintenance: event.target.value === '' ? undefined : event.target.value === 'true',
              }));
            }}
          >
            <option value="">All</option>
            <option value="true">In maintenance</option>
            <option value="false">Outside maintenance</option>
          </select>
        </label>
        <label>
          From{' '}
          <input
            type="datetime-local"
            value={range.from}
            onChange={(event) => setRange((current) => ({ ...current, from: event.target.value }))}
          />
        </label>
        <label>
          To{' '}
          <input
            type="datetime-local"
            value={range.to}
            onChange={(event) => setRange((current) => ({ ...current, to: event.target.value }))}
          />
        </label>
        <button className="button" type="submit">
          Apply time range
        </button>
      </form>
      {error ? <p role="alert">{error}</p> : null}
      {!observations.loaded || !gaps.loaded ? (
        <p role="status">Loading history…</p>
      ) : (
        <>
          {observations.items.length === 0 && hasFilters ? (
            <p>No observations match these filters in the searched range.</p>
          ) : null}
          {observations.items.length > 0 ||
          gaps.items.length > 0 ||
          windows.length > 0 ||
          !hasFilters ? (
            <TimelineTable
              observations={observations.items}
              gaps={gaps.items}
              windows={windows}
              monitorKind={monitorKind}
            />
          ) : null}
          {observations.searchedThrough ? (
            <p>
              Searched back to{' '}
              <time dateTime={observations.searchedThrough}>
                {formatLocalWithOffset(new Date(observations.searchedThrough))}
              </time>
              .
            </p>
          ) : null}
          {observations.nextCursor ? (
            <button
              className="button"
              type="button"
              aria-disabled={pending !== null}
              onClick={() => void older('observations')}
            >
              Load older observations
            </button>
          ) : null}
          {observations.olderLoaded && observations.nextCursor === null ? (
            <p ref={observationEnd} tabIndex={-1}>
              No older checks. Checks and gap records are kept for 90 days.
            </p>
          ) : null}
          {gaps.nextCursor ? (
            <button
              className="button"
              type="button"
              aria-disabled={pending !== null}
              onClick={() => void older('gaps')}
            >
              Load older gaps
            </button>
          ) : null}
          {gaps.olderLoaded && gaps.nextCursor === null ? (
            <p ref={gapEnd} tabIndex={-1}>
              No older gaps. Checks and gap records are kept for 90 days.
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}
