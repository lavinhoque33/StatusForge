import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useNow } from './useNow';

afterEach(() => {
  vi.useRealTimers();
});

describe('useNow', () => {
  it('schedules the deadline timer exactly, with no earlier firing', () => {
    vi.useFakeTimers();
    const deadline = Date.now() + 31_000;
    renderHook(() => useNow(30_000, deadline));
    expect(vi.getTimerCount()).toBe(2); // interval + deadline

    // The only timer before the deadline is none: a 30 s age tick must not
    // fire before the deadline switch… it does (30 s < 31 s), but the
    // deadline timer itself is separate and still pending.
    vi.advanceTimersByTime(29_999);
    expect(vi.getTimerCount()).toBe(2);

    vi.advanceTimersByTime(1_002);
    expect(vi.getTimerCount()).toBe(1); // deadline fired; interval remains
  });

  it('reschedules when the deadline changes and drops stale timers', () => {
    vi.useFakeTimers();
    const first = Date.now() + 10_000;
    const { rerender } = renderHook(({ deadline }) => useNow(30_000, deadline), {
      initialProps: { deadline: first as number | null },
    });
    expect(vi.getTimerCount()).toBe(2);

    rerender({ deadline: first + 20_000 });
    expect(vi.getTimerCount()).toBe(2);

    vi.advanceTimersByTime(10_000);
    // The old deadline passed but its timer was cleared on the re-render.
    expect(vi.getTimerCount()).toBe(2);

    rerender({ deadline: null });
    expect(vi.getTimerCount()).toBe(1); // only the interval remains
  });

  it('schedules nothing extra for past deadlines and null', () => {
    vi.useFakeTimers();
    const { rerender } = renderHook(({ deadline }) => useNow(30_000, deadline), {
      initialProps: { deadline: null as number | null },
    });
    expect(vi.getTimerCount()).toBe(1);

    rerender({ deadline: Date.now() - 1 });
    expect(vi.getTimerCount()).toBe(1);

    rerender({ deadline: Date.now() + 5_000 });
    expect(vi.getTimerCount()).toBe(2);
  });

  it('advances `now` past the deadline so the caller sees the switch', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-09-27T12:00:00.000Z'));
    const deadline = Date.parse('2026-09-27T12:00:31.000Z');
    const { result } = renderHook(() => useNow(30_000, deadline));

    await act(async () => {
      vi.advanceTimersByTime(31_200);
    });
    expect(result.current).toBeGreaterThan(deadline);
  });
});
