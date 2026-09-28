import { describe, expect, it } from 'vitest';
import { gapReasonWords, notCountedReasonWords } from './reasons';

describe('gapReasonWords', () => {
  it.each([
    ['not_scheduled', 'StatusForge was not running'],
    ['overdue', 'workers were busy'],
    ['lease_expired', 'a check was interrupted'],
  ] as [string, string][])('renders %s as %s', (reason, words) => {
    expect(gapReasonWords(reason)).toBe(words);
  });

  it('shows an unfamiliar reason readably instead of reclassifying it', () => {
    expect(gapReasonWords('scheduler_hiccup')).toBe('scheduler hiccup');
  });
});

describe('notCountedReasonWords', () => {
  it.each([
    ['paused', 'monitor was paused'],
    ['archived', 'monitor was archived'],
    ['config_changed', 'configuration changed during the check'],
    ['older_than_current', 'a newer result already existed'],
    ['lease_lost', 'the check outlived its lease'],
  ] as [string, string][])('renders %s as %s', (reason, words) => {
    expect(notCountedReasonWords(reason)).toBe(words);
  });

  it('shows an unfamiliar reason readably instead of reclassifying it', () => {
    expect(notCountedReasonWords('warehouse_flood')).toBe('warehouse flood');
  });
});
