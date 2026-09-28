import { expect, it } from 'vitest';
import { ApiInvalidResponseError } from './http';
import { parseGapPage, parseObservationPage } from './monitors';
import { gapFixture, observationFixture } from '../test/fixtures';

it('rejects absent or malformed paging metadata instead of treating an incomplete response as exhausted', () => {
  const payload = {
    observations: [observationFixture()],
    nextCursor: 'opaque',
    searchedThrough: '2026-09-27T10:15:30.000Z',
  };
  expect(parseObservationPage(payload).nextCursor).toBe('opaque');
  expect(() => parseObservationPage({ ...payload, nextCursor: 42 })).toThrow(
    ApiInvalidResponseError,
  );
  expect(() => parseObservationPage({ ...payload, searchedThrough: 'yesterday' })).toThrow(
    ApiInvalidResponseError,
  );
  expect(() => parseObservationPage({ observations: [] })).toThrow(ApiInvalidResponseError);
  expect(
    parseGapPage({ gaps: [gapFixture()], nextCursor: null, searchedThrough: null }).items,
  ).toHaveLength(1);
});
