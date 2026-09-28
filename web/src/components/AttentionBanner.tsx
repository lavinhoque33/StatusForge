import { useCallback, useEffect, useState } from 'react';
import { ATTENTION_LIMIT, listAttention } from '../api/incidents';
import { isAbortError } from '../api/http';
import { usePolling } from '../lib/usePolling';
import { Link } from '../router/Link';

export function AttentionBanner() {
  const [count, setCount] = useState(0);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const notifications = await listAttention(ATTENTION_LIMIT, signal);
      if (!signal.aborted) setCount(notifications.length);
    } catch (error) {
      if (signal.aborted || isAbortError(error)) return;
      // Keep the last known warning visible if attention cannot be refreshed.
    }
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(() => {
      if (!controller.signal.aborted) return load(controller.signal);
    });
    return () => controller.abort();
  }, [load]);
  usePolling({ refresh: () => load(new AbortController().signal) });
  if (count === 0) return null;
  return (
    <aside className="attention-banner" aria-label="Notifications need attention" role="status">
      <Link to="/incidents#notifications-attention">
        {count === ATTENTION_LIMIT ? `${ATTENTION_LIMIT}+` : count} notification
        {count === 1 ? '' : 's'} could not be delivered
      </Link>
    </aside>
  );
}
