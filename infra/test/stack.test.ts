import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { Template as Assertions } from 'aws-cdk-lib/assertions';
import { CURRENTLY_RECOMMENDED_FLAGS } from 'aws-cdk-lib/cx-api';
import { afterAll, describe, expect, it } from 'vitest';

import {
  buildApp,
  CDK_JSON,
  CONTEXT_MONITORS,
  CONTEXT_SCHEDULE_STATE,
  CONTEXT_TARGETS,
  readDynamoDBActions,
} from '../lib/statusforge-stack.ts';
import { checkTemplate, obj } from '../lib/template-rules.ts';

// Placeholder bootstraps: the tests never need `make lambda-build`.
const stubAssets = mkdtempSync(join(tmpdir(), 'statusforge-infra-'));
for (const name of ['planner', 'worker']) {
  mkdirSync(join(stubAssets, name));
  writeFileSync(join(stubAssets, name, 'bootstrap'), '#!/bin/sh\nexit 1\n');
}
afterAll(() => rmSync(stubAssets, { recursive: true, force: true }));

function synth(context?: Record<string, unknown>): Record<string, unknown> {
  const { stack } = buildApp({ lambdaAssetRoot: stubAssets, context });
  return obj(Assertions.fromStack(stack).toJSON());
}

/** Reads a nested field of untrusted template JSON. */
function at(value: unknown, ...path: string[]): unknown {
  return path.reduce((v, key) => obj(v)[key], value);
}

function resourcesOf(template: unknown, type: string): [string, unknown][] {
  return Object.entries(obj(at(template, 'Resources'))).filter(([, r]) => at(r, 'Type') === type);
}

const actions = readDynamoDBActions();

describe('cdk.json', () => {
  const cdkJson: unknown = JSON.parse(readFileSync(CDK_JSON, 'utf8'));

  it('runs the app with Node type stripping and turns off notices, telemetry and version reporting', () => {
    expect(at(cdkJson, 'app')).toBe('node bin/statusforge.ts');
    expect(at(cdkJson, 'notices')).toBe(false);
    expect(at(cdkJson, 'versionReporting')).toBe(false);
    expect(at(cdkJson, 'context', 'cli-telemetry')).toBe(false);
  });

  it('sets every feature flag the pinned aws-cdk-lib recommends', () => {
    for (const [flag, value] of Object.entries(CURRENTLY_RECOMMENDED_FLAGS)) {
      expect(at(cdkJson, 'context', flag), flag).toStrictEqual(value);
    }
  });
});

describe('StatusForge stack', () => {
  it('matches §10.2 and §10.3 with closed defaults', () => {
    expect(checkTemplate(synth(), { actions, closedDefaults: true })).toStrictEqual([]);
  });

  it('grants each Lambda role exactly the DynamoDB actions in iam/dynamodb-actions.json, on the table', () => {
    const template = synth();
    const [[tableId]] = resourcesOf(template, 'AWS::DynamoDB::Table');
    const functions = resourcesOf(template, 'AWS::Lambda::Function');
    expect(functions).toHaveLength(2);
    for (const [id, f] of functions) {
      const variables = obj(at(f, 'Properties', 'Environment', 'Variables'));
      const expected = 'STATUSFORGE_WORK_QUEUE_URL' in variables ? actions.planner : actions.worker;
      const [roleId] = [at(f, 'Properties', 'Role', 'Fn::GetAtt')].flat();
      const statements = [at(template, 'Resources', String(roleId), 'Properties', 'Policies')]
        .flat()
        .flatMap((p) => [at(p, 'PolicyDocument', 'Statement')].flat());
      const granted = statements.flatMap((s) =>
        [at(s, 'Action')]
          .flat()
          .filter((a) => String(a).startsWith('dynamodb:'))
          .map((action) => ({ action, resource: at(s, 'Resource') })),
      );
      expect(granted.map(({ action }) => action).sort(), id).toStrictEqual([...expected].sort());
      for (const { resource } of granted) {
        expect(resource, id).toStrictEqual({ 'Fn::GetAtt': [tableId, 'Arn'] });
      }
    }
  });

  it('passes deploy-time context unchanged into the functions and the schedule', () => {
    const monitors =
      '[{"key":"a","name":"A","url":"https://a.test/","intervalSeconds":300,"expectedStatus":200,"deadlineMs":5000}]';
    const template = synth({
      [CONTEXT_MONITORS]: monitors,
      [CONTEXT_TARGETS]: 'https://a.test',
      [CONTEXT_SCHEDULE_STATE]: 'ENABLED',
    });
    const environments = resourcesOf(template, 'AWS::Lambda::Function').map(([, f]) =>
      at(f, 'Properties', 'Environment', 'Variables'),
    );
    expect(environments).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          STATUSFORGE_CLOUD_MONITORS: monitors,
          STATUSFORGE_CLOUD_TARGETS: 'https://a.test',
        }),
        expect.not.objectContaining({ STATUSFORGE_CLOUD_MONITORS: expect.anything() }),
      ]),
    );
    for (const variables of environments) {
      expect(at(variables, 'STATUSFORGE_CLOUD_TARGETS')).toBe('https://a.test');
    }
    const [[, schedule]] = resourcesOf(template, 'AWS::Scheduler::Schedule');
    expect(at(schedule, 'Properties', 'State')).toBe('ENABLED');
    // Opened configuration keeps the §10.2 shape; only the committed-default check treats the
    // operator's target URLs as forbidden literals.
    expect(checkTemplate(template, { actions, closedDefaults: false })).toStrictEqual([]);
    expect(checkTemplate(template, { actions, closedDefaults: true })).toEqual(
      expect.arrayContaining([expect.stringMatching(/STATUSFORGE_CLOUD_TARGETS: URL$/)]),
    );
  });

  it('reports each opened default when the committed configuration is checked', () => {
    const check = (context: Record<string, unknown>) =>
      checkTemplate(synth(context), { actions, closedDefaults: true });
    expect(check({ [CONTEXT_SCHEDULE_STATE]: 'ENABLED' })).toStrictEqual([
      'defaults: the schedule must be DISABLED',
    ]);
    expect(check({ [CONTEXT_TARGETS]: 'x', [CONTEXT_MONITORS]: '[{}]' })).toStrictEqual([
      'defaults: STATUSFORGE_CLOUD_TARGETS must be empty',
      'defaults: STATUSFORGE_CLOUD_MONITORS must be []',
    ]);
  });

  it('stringifies monitors given as a JSON value in cdk.json', () => {
    const template = synth({ [CONTEXT_MONITORS]: [{ key: 'a' }] });
    const values = resourcesOf(template, 'AWS::Lambda::Function').map(([, f]) =>
      at(f, 'Properties', 'Environment', 'Variables', 'STATUSFORGE_CLOUD_MONITORS'),
    );
    expect(values).toContain('[{"key":"a"}]');
  });

  it('rejects a schedule state other than ENABLED or DISABLED', () => {
    expect(() => synth({ [CONTEXT_SCHEDULE_STATE]: 'enabled' })).toThrow(CONTEXT_SCHEDULE_STATE);
  });
});

describe('template rules', () => {
  const base = synth();
  const check = (template: unknown) => checkTemplate(template, { actions, closedDefaults: true });
  const mutate = (change: (resources: Record<string, unknown>) => void): string[] => {
    const copy = structuredClone(base);
    change(obj(copy['Resources']));
    return check(copy);
  };

  it('rejects resources outside §10.2', () => {
    expect(
      mutate((resources) => {
        resources['LogRetention'] = { Type: 'Custom::LogRetention', Properties: {} };
        resources['ExtraQueue'] = { Type: 'AWS::SQS::Queue', Properties: {} };
      }),
    ).toStrictEqual([
      'AWS::SQS::Queue: expected 3, found 4',
      'forbidden resource type Custom::LogRetention',
    ]);
  });

  it('rejects wildcard grants and table-level writes', () => {
    const problems = mutate((resources) => {
      const [[, role]] = resourcesOf({ Resources: resources }, 'AWS::IAM::Role');
      const [policy] = [at(role, 'Properties', 'Policies')].flat();
      const statements = at(policy, 'PolicyDocument', 'Statement');
      if (!Array.isArray(statements)) throw new Error('expected a statement list');
      statements.push(
        { Action: 'dynamodb:*', Effect: 'Allow', Resource: { Ref: 'X' } },
        { Action: 'dynamodb:UpdateTimeToLive', Effect: 'Allow', Resource: '*' },
      );
    });
    expect(problems).toEqual(
      expect.arrayContaining([
        expect.stringContaining('wildcard or non-literal action "dynamodb:*"'),
        expect.stringContaining('table-level write dynamodb:UpdateTimeToLive'),
        expect.stringContaining('wildcard resource *'),
      ]),
    );
  });

  it('rejects literal accounts and regions, Fn::GetAZs, URLs and host names', () => {
    const problems = mutate((resources) => {
      const [[, f]] = resourcesOf({ Resources: resources }, 'AWS::Lambda::Function');
      Object.assign(obj(at(f, 'Properties', 'Environment', 'Variables')), {
        A: 'arn:aws:sqs:us-east-1:123456789012:q',
        B: { 'Fn::GetAZs': '' },
        C: 'https://example.com/x',
        D: 'sqs.us-east-1.amazonaws.com',
      });
    });
    expect(problems).toEqual(
      expect.arrayContaining([
        expect.stringContaining('literal account ID'),
        expect.stringContaining('literal region'),
        expect.stringContaining('Fn::GetAZs is not allowed'),
        expect.stringContaining(': URL'),
        expect.stringContaining('host name sqs.us-east-1.amazonaws.com'),
      ]),
    );
  });

  it('rejects a role that lacks an action from iam/dynamodb-actions.json', () => {
    const problems = checkTemplate(base, {
      actions: { ...actions, worker: [...actions.worker, 'dynamodb:BatchWriteItem'] },
      closedDefaults: true,
    });
    expect(problems).toStrictEqual([
      expect.stringMatching(/^worker role: missing grants .*dynamodb:BatchWriteItem/),
    ]);
  });
});
