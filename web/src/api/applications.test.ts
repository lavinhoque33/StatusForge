import { afterEach, expect, it, vi } from 'vitest';
import { parseMarker, listMarkers } from './applications';
import { ApiInvalidResponseError } from './http';
import { jsonResponse, stubApi } from '../test/fixtures';

const at = '2026-09-27T10:00:00.000Z';
const marker = {
  id: 'dep-1',
  applicationId: 'app-1',
  version: '1.2.3',
  description: null,
  link: 'https://example.invalid/release',
  deployedAt: null,
  deploymentId: null,
  source: 'manual',
  reportedAt: at,
};
afterEach(() => vi.unstubAllGlobals());
it('rejects unsafe links, unknown marker sources and malformed timestamps instead of rendering them', () => {
  expect(() => parseMarker({ ...marker, link: 'javascript:alert(1)' })).toThrow(
    ApiInvalidResponseError,
  );
  expect(() => parseMarker({ ...marker, link: 'https://user:pass@example.invalid/' })).toThrow(
    ApiInvalidResponseError,
  );
  expect(() => parseMarker({ ...marker, source: 'automatic' })).toThrow(ApiInvalidResponseError);
  expect(() => parseMarker({ ...marker, reportedAt: 'tomorrow' })).toThrow(ApiInvalidResponseError);
});
it('reads bounded deployment markers from the application endpoint', async () => {
  stubApi({
    'GET /api/applications/app-1/deployments?limit=50': () =>
      jsonResponse({ deployments: [marker] }),
  });
  await expect(listMarkers('app-1')).resolves.toEqual([marker]);
});
