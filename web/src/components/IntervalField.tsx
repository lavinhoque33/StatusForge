import { useState } from 'react';
import { type Intervals } from '../api/monitors';
import { intervalLabel } from '../lib/intervals';

type IntervalFieldProps = {
  id: string;
  /** The intervals the backend offers; null while loading. */
  intervals: Intervals | null;
  /** The monitor's stored interval; offered as `(no longer offered)` when the backend no longer lists it. */
  storedSeconds?: number;
  /** Reported on every change so the page knows what a submit would send. */
  onChange: (seconds: number) => void;
  disabled?: boolean;
};

/**
 * Which option the selector starts on: the monitor's stored interval whenever
 * one exists (offered or legacy), else the backend default (create), else the
 * first offered value.
 */
function computeInitial(
  listed: number[],
  intervals: Intervals | null,
  stored: number | undefined,
): number | null {
  if (stored !== undefined) return stored;
  const fallback = intervals?.defaultIntervalSeconds;
  if (fallback !== undefined && listed.includes(fallback)) return fallback;
  return listed[0] ?? null;
}

/**
 * Interval selector: the backend's intervals in words
 * (`1 min`, `5 min`, …). A stored interval the backend no longer offers — the
 * dev minimum was raised — appears as an extra, pre-selected
 * `(no longer offered)` option instead of silently disappearing.
 */
export function IntervalField({
  id,
  intervals,
  storedSeconds,
  onChange,
  disabled = false,
}: IntervalFieldProps) {
  const listed = intervals?.intervalSeconds ?? [];
  const legacy =
    storedSeconds !== undefined && !listed.includes(storedSeconds) ? storedSeconds : null;
  // `picked === undefined` until the user chooses; the initial selection is
  // then derived from the offer list on every render, so no effect is needed
  // to sync it when the list arrives.
  const [picked, setPicked] = useState<number | undefined>(undefined);

  const value = picked ?? computeInitial(listed, intervals, storedSeconds);

  return (
    <div className="form-field">
      <label htmlFor={id}>Check every</label>
      {intervals === null ? (
        <p className="form-hint" id={`${id}-hint`}>
          Loading the available intervals…
        </p>
      ) : null}
      <select
        id={id}
        name="intervalSeconds"
        disabled={disabled || intervals === null || value === null}
        aria-describedby={intervals === null ? `${id}-hint` : undefined}
        value={value === null ? '' : String(value)}
        onChange={(event) => {
          const seconds = Number(event.target.value);
          setPicked(seconds);
          onChange(seconds);
        }}
      >
        {legacy === null ? null : (
          <option value={legacy}>{intervalLabel(legacy)} (no longer offered)</option>
        )}
        {listed.map((seconds) => (
          <option key={seconds} value={seconds}>
            {intervalLabel(seconds)}
          </option>
        ))}
      </select>
    </div>
  );
}

export default IntervalField;
