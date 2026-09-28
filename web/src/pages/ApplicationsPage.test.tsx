import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { callsTo, jsonResponse, monitorRecordFixture, stubApi } from '../test/fixtures';
import { ApplicationsPage } from './ApplicationsPage';

const at = '2026-09-27T10:00:00.000Z';
const app = {
  id: 'app-1',
  name: 'Payments',
  token: { hint: '1234', createdAt: at },
  members: [],
  createdAt: at,
  updatedAt: at,
  archivedAt: null,
};
afterEach(() => vi.unstubAllGlobals());

it('shows a new application token exactly once and lets a monitor join', async () => {
  let created = false;
  const monitor = monitorRecordFixture({ id: 'monitor-1', name: 'Checkout' });
  const fetchMock = stubApi({
    'GET /api/applications': () => jsonResponse({ applications: created ? [app] : [] }),
    'GET /api/monitors': () => jsonResponse({ monitors: [monitor] }),
    'POST /api/applications': () => {
      created = true;
      return jsonResponse({ ...app, issuedToken: 'sfd_secret' }, 201);
    },
    'GET /api/applications/app-1/deployments?limit=50': () => jsonResponse({ deployments: [] }),
    'PUT /api/monitors/monitor-1/application': () =>
      jsonResponse({ ...monitor, applicationId: app.id }),
  });
  render(<ApplicationsPage />);
  fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Payments' } });
  fireEvent.click(screen.getByRole('button', { name: 'Create application' }));
  const panel = await screen.findByRole('region', { name: 'New application token' });
  expect(within(panel).getByText('sfd_secret')).toBeInTheDocument();
  expect(within(panel).getByText(/-d '{"version":"1.2.3"}'/)).toHaveTextContent(
    '/ingest/applications/app-1/deployments',
  );
  expect(within(panel).getByRole('button', { name: 'Copy token' })).toHaveFocus();
  expect(screen.getByText('Application created. Token shown once.')).toHaveAttribute(
    'role',
    'status',
  );
  fireEvent.click(within(panel).getByRole('button', { name: 'Done' }));
  expect(screen.queryByText('sfd_secret')).not.toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole('heading', { name: 'Payments' })).toHaveFocus());
  fireEvent.change(screen.getByLabelText('Add monitor'), { target: { value: 'monitor-1' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add member' }));
  await waitFor(() =>
    expect(callsTo(fetchMock, 'PUT', '/api/monitors/monitor-1/application')).toHaveLength(1),
  );
  expect(
    JSON.parse(
      callsTo(fetchMock, 'PUT', '/api/monitors/monitor-1/application')[0][1]!.body as string,
    ),
  ).toEqual({ applicationId: 'app-1' });
});

it('retains the selected membership on an archived conflict and shows the error', async () => {
  const monitor = monitorRecordFixture({ id: 'monitor-1', name: 'Checkout' });
  stubApi({
    'GET /api/applications': () => jsonResponse({ applications: [app] }),
    'GET /api/monitors': () => jsonResponse({ monitors: [monitor] }),
    'GET /api/applications/app-1/deployments?limit=50': () => jsonResponse({ deployments: [] }),
    'PUT /api/monitors/monitor-1/application': () => jsonResponse({ error: 'archived' }, 409),
  });
  render(<ApplicationsPage />);
  fireEvent.change(await screen.findByLabelText('Add monitor'), { target: { value: 'monitor-1' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add member' }));
  expect(await screen.findByText(/archived/)).toBeInTheDocument();
  expect(screen.getByLabelText('Add monitor')).toHaveValue('monitor-1');
});
it('keeps manual marker inputs after field validation and surfaces the backend field error', async () => {
  stubApi({
    'GET /api/applications': () => jsonResponse({ applications: [app] }),
    'GET /api/monitors': () => jsonResponse({ monitors: [] }),
    'GET /api/applications/app-1/deployments?limit=50': () => jsonResponse({ deployments: [] }),
    'POST /api/applications/app-1/deployments': () =>
      jsonResponse(
        {
          error: 'validation_failed',
          fields: {
            link: { code: 'scheme_not_allowed', message: 'Only http and https links are allowed.' },
          },
        },
        400,
      ),
  });
  render(<ApplicationsPage />);
  fireEvent.change(await screen.findByLabelText('Version'), { target: { value: '1.2.3' } });
  fireEvent.change(screen.getByLabelText('Link'), {
    target: { value: 'https://example.invalid/release' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Record deployment' }));
  expect(await screen.findByText('Only http and https links are allowed.')).toBeInTheDocument();
  expect(screen.getByLabelText('Version')).toHaveValue('1.2.3');
  expect(screen.getByLabelText('Link')).toHaveAttribute('aria-invalid', 'true');
});
it('moves focus into confirmation and back to the correct trigger, then to the archived card', async () => {
  let archived = false;
  stubApi({
    'GET /api/applications': () =>
      jsonResponse({ applications: [{ ...app, archivedAt: archived ? at : null }] }),
    'GET /api/monitors': () => jsonResponse({ monitors: [] }),
    'GET /api/applications/app-1/deployments?limit=50': () => jsonResponse({ deployments: [] }),
    'POST /api/applications/app-1/archive': () => {
      archived = true;
      return jsonResponse({ ...app, archivedAt: at, token: null });
    },
  });
  render(<ApplicationsPage />);
  for (const [trigger, label] of [
    ['Rotate token', 'Confirm rotate'],
    ['Revoke token', 'Confirm revoke'],
    ['Archive application', 'Confirm archive'],
  ]) {
    const button = await screen.findByRole('button', { name: trigger });
    fireEvent.click(button);
    await waitFor(() => expect(screen.getByRole('button', { name: label })).toHaveFocus());
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(button).toHaveFocus());
  }
  fireEvent.click(screen.getByRole('button', { name: 'Archive application' }));
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Confirm archive' })).toHaveFocus(),
  );
  fireEvent.click(screen.getByRole('button', { name: 'Confirm archive' }));
  const heading = await screen.findByRole('heading', { name: 'Payments — Archived' });
  await waitFor(() => expect(heading).toHaveFocus());
  expect(screen.getByText('Application archived.')).toHaveAttribute('role', 'status');
});

it('clears prior marker success before displaying duplicate deployment error', async () => {
  let submissions = 0;
  const marker = {
    id: 'deployment-1',
    applicationId: 'app-1',
    version: '1.0',
    description: null,
    link: null,
    deployedAt: null,
    deploymentId: null,
    source: 'manual',
    reportedAt: at,
  };
  stubApi({
    'GET /api/applications': () => jsonResponse({ applications: [app] }),
    'GET /api/monitors': () => jsonResponse({ monitors: [] }),
    'GET /api/applications/app-1/deployments?limit=50': () =>
      jsonResponse({ deployments: [marker] }),
    'POST /api/applications/app-1/deployments': () => {
      submissions += 1;
      return submissions === 1
        ? jsonResponse(marker, 201)
        : jsonResponse({ error: 'duplicate_deployment' }, 409);
    },
  });
  render(<ApplicationsPage />);
  fireEvent.change(await screen.findByLabelText('Version'), { target: { value: '1.0' } });
  fireEvent.click(screen.getByRole('button', { name: 'Record deployment' }));
  await screen.findByText('Deployment marker recorded.');
  fireEvent.change(screen.getByLabelText('Version'), { target: { value: '1.0' } });
  fireEvent.click(screen.getByRole('button', { name: 'Record deployment' }));
  expect(
    await screen.findByText('This deployment ID was already recorded for this application.'),
  ).toBeInTheDocument();
  expect(screen.queryByText('Deployment marker recorded.')).not.toBeInTheDocument();
});
