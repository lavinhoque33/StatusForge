// Checks a synthesized StatusForge template against the resource and IAM
// rules in docs/architecture/cloud-path.md. Used on the in-process template by
// the tests and on the CLI's cdk.out by scripts/check-template.ts, so both
// apply the same rules. The template is untrusted JSON: every read narrows at
// runtime.
import { isDeepStrictEqual } from 'node:util';

import type { DynamoDBActions } from './statusforge-stack.ts';

type Fields = Record<string, unknown>;

export interface CheckOptions {
  readonly actions: DynamoDBActions;
  /** The committed configuration: schedule DISABLED, no targets, no monitors. */
  readonly closedDefaults: boolean;
}

/** Exactly these types and counts. AWS::IAM::Policy is allowed but, like roles, checked. */
export const EXPECTED_TYPES: Readonly<Record<string, number>> = {
  'AWS::DynamoDB::Table': 1,
  'AWS::SQS::Queue': 3,
  'AWS::SQS::QueuePolicy': 3,
  'AWS::Lambda::Function': 2,
  'AWS::Lambda::EventInvokeConfig': 1,
  'AWS::Lambda::EventSourceMapping': 1,
  'AWS::Logs::LogGroup': 2,
  'AWS::Scheduler::Schedule': 1,
  'AWS::IAM::Role': 3,
};

export const TABLE_LEVEL_WRITES = [
  'dynamodb:CreateTable',
  'dynamodb:UpdateTable',
  'dynamodb:UpdateTimeToLive',
  'dynamodb:DeleteTable',
];

const SERVICE_PRINCIPALS: Record<string, true> = {
  'lambda.amazonaws.com': true,
  'scheduler.amazonaws.com': true,
};
// The only places operator-supplied targets and monitors (which hold URLs) may appear.
const DEPLOY_TIME_PATH =
  /^\$\.Resources\.[A-Za-z0-9]+\.Properties\.Environment\.Variables\.STATUSFORGE_CLOUD_(?:TARGETS|MONITORS)$/;
const LAMBDA_LOG_ACTIONS = ['logs:CreateLogStream', 'logs:PutLogEvents'];
const WORKER_QUEUE_ACTIONS = [
  'sqs:ReceiveMessage',
  'sqs:DeleteMessage',
  'sqs:ChangeMessageVisibility',
  'sqs:GetQueueAttributes',
];
const HOSTNAME =
  /(?:^|[^a-z0-9-])[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:com|net|org|io|dev|app|aws|cn|cloud|co)(?![a-z0-9-])/i;
const REGION =
  /(?:^|[^a-z0-9])[a-z]{2}(?:-gov|-iso[a-z]?)?-(?:north|south|east|west|central|northeast|northwest|southeast|southwest)-\d(?![0-9])/i;

const ref = (id: string) => ({ Ref: id });
const arn = (id: string) => ({ 'Fn::GetAtt': [id, 'Arn'] });

/** The value if it is a plain JSON object, else an empty one. */
export function obj(value: unknown): Fields {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Fields) // Checked above: a non-array object parsed from JSON.
    : {};
}

/** IAM's "one or many" form as a list. */
function list(value: unknown): unknown[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? value : [value];
}

function refId(value: unknown): string | undefined {
  const id = obj(value)['Ref'];
  return typeof id === 'string' ? id : undefined;
}

function getAttId(value: unknown): string | undefined {
  const g = obj(value)['Fn::GetAtt'];
  return Array.isArray(g) && g.length === 2 && g[1] === 'Arn' && typeof g[0] === 'string'
    ? g[0]
    : undefined;
}

function variables(resource: Fields): Fields {
  return obj(obj(obj(resource['Properties'])['Environment'])['Variables']);
}

export function checkTemplate(template: unknown, options: CheckOptions): string[] {
  const problems: string[] = [];
  const root = obj(template);
  const resources: Record<string, Fields> = {};
  for (const [id, r] of Object.entries(obj(root['Resources']))) resources[id] = obj(r);
  const props = (id: string) => obj(resources[id]?.['Properties']);
  const idsOf = (type: string) =>
    Object.keys(resources).filter((id) => resources[id]['Type'] === type);
  const expectEqual = (what: string, actual: unknown, expected: unknown) => {
    if (!isDeepStrictEqual(actual, expected)) {
      problems.push(
        `${what}: expected ${JSON.stringify(expected)}, found ${JSON.stringify(actual)}`,
      );
    }
  };
  const expectDestroy = (id: string) => {
    const r = resources[id];
    if (r['DeletionPolicy'] !== 'Delete' || r['UpdateReplacePolicy'] !== 'Delete') {
      problems.push(`${id}: removal policy must be destroy`);
    }
  };

  // Types and counts.
  const counts: Record<string, number> = {};
  for (const r of Object.values(resources)) {
    const type = String(r['Type']);
    counts[type] = (counts[type] ?? 0) + 1;
  }
  for (const [type, count] of Object.entries(counts)) {
    if (type === 'AWS::IAM::Policy') continue;
    if (EXPECTED_TYPES[type] === undefined) problems.push(`forbidden resource type ${type}`);
    else if (EXPECTED_TYPES[type] !== count) {
      problems.push(`${type}: expected ${EXPECTED_TYPES[type]}, found ${count}`);
    }
  }
  for (const [type, count] of Object.entries(EXPECTED_TYPES)) {
    if (counts[type] === undefined) problems.push(`${type}: expected ${count}, found 0`);
  }
  for (const [id, r] of Object.entries(resources)) {
    if (r['Condition'] !== undefined) problems.push(`${id}: conditional resources are not allowed`);
  }
  if (!isDeepStrictEqual(Object.keys(obj(root['Parameters'])), ['BootstrapVersion'])) {
    problems.push('parameters: only the synthesizer BootstrapVersion parameter is allowed');
  }
  if (!isDeepStrictEqual(Object.keys(obj(root['Rules'])), ['CheckBootstrapVersion'])) {
    problems.push('rules: only the synthesizer CheckBootstrapVersion rule is allowed');
  }
  if (root['Conditions'] !== undefined) problems.push('conditions are not allowed');

  problems.push(...scanLiterals(template, !options.closedDefaults));
  problems.push(...checkIdentityStatements(resources));
  if (problems.length > 0) return problems; // The structure below assumes the right shape.

  const [tableId] = idsOf('AWS::DynamoDB::Table');
  const functions = idsOf('AWS::Lambda::Function');
  const planner = functions.find((id) => 'STATUSFORGE_WORK_QUEUE_URL' in variables(resources[id]));
  const worker = functions.find(
    (id) => !('STATUSFORGE_WORK_QUEUE_URL' in variables(resources[id])),
  );
  if (!planner || !worker) return ['functions: expected one planner and one worker'];
  const plannerEnv = variables(resources[planner]);
  const workerEnv = variables(resources[worker]);
  const workQueue = refId(plannerEnv['STATUSFORGE_WORK_QUEUE_URL']);
  const [scheduleId] = idsOf('AWS::Scheduler::Schedule');
  const target = obj(props(scheduleId)['Target']);
  const workDlq =
    workQueue && getAttId(obj(props(workQueue)['RedrivePolicy'])['deadLetterTargetArn']);
  const scheduleDlq = getAttId(obj(target['DeadLetterConfig'])['Arn']);
  const scheduleRole = getAttId(target['RoleArn']);
  if (!workQueue || !workDlq || !scheduleDlq || !scheduleRole) {
    return [
      'queues/schedule: the work queue, dead-letter queues and schedule role must be referenced',
    ];
  }

  // Table.
  expectEqual(`table ${tableId}`, props(tableId), {
    AttributeDefinitions: [
      { AttributeName: 'PK', AttributeType: 'S' },
      { AttributeName: 'SK', AttributeType: 'S' },
    ],
    DeletionProtectionEnabled: false,
    KeySchema: [
      { AttributeName: 'PK', KeyType: 'HASH' },
      { AttributeName: 'SK', KeyType: 'RANGE' },
    ],
    PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: false },
    ProvisionedThroughput: { ReadCapacityUnits: 10, WriteCapacityUnits: 10 },
    TimeToLiveSpecification: { AttributeName: 'expiresAt', Enabled: true },
  });
  expectDestroy(tableId);

  // Queues, each with exactly one SSL-only policy.
  const fourteenDays = { MessageRetentionPeriod: 1_209_600, SqsManagedSseEnabled: true };
  const queues: Record<string, unknown> = {
    [workQueue]: {
      MessageRetentionPeriod: 86_400,
      RedrivePolicy: { deadLetterTargetArn: arn(workDlq), maxReceiveCount: 3 },
      SqsManagedSseEnabled: true,
      VisibilityTimeout: 360,
    },
    [workDlq]: fourteenDays,
    [scheduleDlq]: fourteenDays,
  };
  if (Object.keys(queues).length !== 3) return ['queues: expected three distinct queues'];
  for (const [id, expected] of Object.entries(queues)) {
    if (resources[id]?.['Type'] !== 'AWS::SQS::Queue') return [`${id}: not a queue`];
    expectEqual(`queue ${id}`, props(id), expected);
    expectDestroy(id);
    const sslOnly = {
      PolicyDocument: {
        Statement: [
          {
            Action: 'sqs:*',
            Condition: { Bool: { 'aws:SecureTransport': 'false' } },
            Effect: 'Deny',
            Principal: { AWS: '*' },
            Resource: arn(id),
          },
        ],
        Version: '2012-10-17',
      },
      Queues: [ref(id)],
    };
    if (
      idsOf('AWS::SQS::QueuePolicy').filter((p) => isDeepStrictEqual(props(p), sslOnly)).length !==
      1
    ) {
      problems.push(`queue ${id}: expected one enforceSSL-only policy`);
    }
  }

  // Functions, their log groups and roles.
  const logGroups = new Set<string>();
  const checkFunction = (name: 'planner' | 'worker', id: string, grants: Grant[]) => {
    const { Code: code, ...rest } = props(id);
    const logGroup = refId(obj(rest['LoggingConfig'])['LogGroup']);
    const role = getAttId(rest['Role']);
    if (!logGroup || !role || resources[logGroup]?.['Type'] !== 'AWS::Logs::LogGroup') {
      problems.push(`${name} ${id}: needs an explicit log group and role`);
      return;
    }
    logGroups.add(logGroup);
    const env = variables(resources[id]);
    const expectedEnv: Fields = {
      STATUSFORGE_TABLE: ref(tableId),
      STATUSFORGE_CLOUD_TARGETS: env['STATUSFORGE_CLOUD_TARGETS'],
      STATUSFORGE_LOG_LEVEL: 'info',
    };
    if (name === 'planner') {
      expectedEnv['STATUSFORGE_WORK_QUEUE_URL'] = ref(workQueue);
      expectedEnv['STATUSFORGE_CLOUD_MONITORS'] = env['STATUSFORGE_CLOUD_MONITORS'];
    }
    for (const key of ['STATUSFORGE_CLOUD_TARGETS', 'STATUSFORGE_CLOUD_MONITORS']) {
      if (key in expectedEnv && typeof expectedEnv[key] !== 'string') {
        problems.push(`${name}: ${key} must be a string`);
      }
    }
    expectEqual(`${name} ${id}`, rest, {
      Architectures: ['arm64'],
      Environment: { Variables: expectedEnv },
      Handler: 'bootstrap',
      LoggingConfig: { LogGroup: ref(logGroup) },
      MemorySize: 128,
      Role: arn(role),
      Runtime: 'provided.al2023',
      Timeout: name === 'planner' ? 50 : 60,
    });
    if (typeof obj(code)['S3Key'] !== 'string' || obj(code)['S3Bucket'] === undefined) {
      problems.push(`${name}: code must be a synthesizer asset`);
    }
    expectEqual(`log group ${logGroup}`, props(logGroup), { RetentionInDays: 3 });
    expectDestroy(logGroup);
    problems.push(
      ...checkRole(resources, name, role, { Service: 'lambda.amazonaws.com' }, undefined, [
        ...grants,
        { actions: LAMBDA_LOG_ACTIONS, resource: arn(logGroup) },
      ]),
    );
  };
  checkFunction('planner', planner, [
    { actions: options.actions.planner, resource: arn(tableId) },
    { actions: ['sqs:SendMessage'], resource: arn(workQueue) },
  ]);
  checkFunction('worker', worker, [
    { actions: options.actions.worker, resource: arn(tableId) },
    { actions: WORKER_QUEUE_ACTIONS, resource: arn(workQueue) },
  ]);
  if (logGroups.size !== 2) problems.push('log groups: each function needs its own');

  const [invokeConfig] = idsOf('AWS::Lambda::EventInvokeConfig');
  expectEqual(`event invoke config ${invokeConfig}`, props(invokeConfig), {
    FunctionName: ref(planner),
    MaximumEventAgeInSeconds: 60,
    MaximumRetryAttempts: 0,
    Qualifier: '$LATEST',
  });
  const [mapping] = idsOf('AWS::Lambda::EventSourceMapping');
  expectEqual(`event source mapping ${mapping}`, props(mapping), {
    BatchSize: 1,
    EventSourceArn: arn(workQueue),
    FunctionName: ref(worker),
    FunctionResponseTypes: ['ReportBatchItemFailures'],
    ScalingConfig: { MaximumConcurrency: 2 },
  });

  const state = props(scheduleId)['State'];
  expectEqual(`schedule ${scheduleId}`, props(scheduleId), {
    FlexibleTimeWindow: { Mode: 'OFF' },
    ScheduleExpression: 'rate(1 minute)',
    State: state === 'ENABLED' ? 'ENABLED' : 'DISABLED',
    Target: {
      Arn: arn(planner),
      DeadLetterConfig: { Arn: arn(scheduleDlq) },
      RetryPolicy: { MaximumEventAgeInSeconds: 60, MaximumRetryAttempts: 2 },
      RoleArn: arn(scheduleRole),
    },
  });
  problems.push(
    ...checkRole(
      resources,
      'schedule',
      scheduleRole,
      { Service: 'scheduler.amazonaws.com' },
      { StringEquals: { 'aws:SourceAccount': ref('AWS::AccountId') } },
      [
        { actions: ['lambda:InvokeFunction'], resource: arn(planner) },
        { actions: ['sqs:SendMessage'], resource: arn(scheduleDlq) },
      ],
    ),
  );

  if (options.closedDefaults) {
    if (state !== 'DISABLED') problems.push('defaults: the schedule must be DISABLED');
    if (
      plannerEnv['STATUSFORGE_CLOUD_TARGETS'] !== '' ||
      workerEnv['STATUSFORGE_CLOUD_TARGETS'] !== ''
    ) {
      problems.push('defaults: STATUSFORGE_CLOUD_TARGETS must be empty');
    }
    if (plannerEnv['STATUSFORGE_CLOUD_MONITORS'] !== '[]') {
      problems.push('defaults: STATUSFORGE_CLOUD_MONITORS must be []');
    }
  }
  return problems;
}

interface Grant {
  actions: readonly string[];
  resource: unknown;
}

/** A role's trust policy and its exact (action, resource) grants, from every attached policy. */
function checkRole(
  resources: Record<string, Fields>,
  name: string,
  roleId: string,
  principal: Fields,
  condition: Fields | undefined,
  grants: Grant[],
): string[] {
  const problems: string[] = [];
  if (resources[roleId]?.['Type'] !== 'AWS::IAM::Role') return [`${name}: role ${roleId} missing`];
  const {
    AssumeRolePolicyDocument: trust,
    Policies: policies,
    ...rest
  } = obj(resources[roleId]['Properties']);
  if (Object.keys(rest).length > 0) {
    problems.push(`${name} role: unexpected properties ${Object.keys(rest).join(', ')}`);
  }
  const expectedTrust = {
    Statement: [
      {
        Action: 'sts:AssumeRole',
        ...(condition === undefined ? {} : { Condition: condition }),
        Effect: 'Allow',
        Principal: principal,
      },
    ],
    Version: '2012-10-17',
  };
  if (!isDeepStrictEqual(trust, expectedTrust)) {
    problems.push(
      `${name} role trust: expected ${JSON.stringify(expectedTrust)}, found ${JSON.stringify(trust)}`,
    );
  }
  const documents = list(policies).map((p) => obj(p)['PolicyDocument']);
  for (const r of Object.values(resources)) {
    const policy = obj(r['Properties']);
    if (
      r['Type'] === 'AWS::IAM::Policy' &&
      list(policy['Roles']).some((a) => refId(a) === roleId)
    ) {
      documents.push(policy['PolicyDocument']);
    }
  }
  const actual = new Set<string>();
  for (const document of documents) {
    for (const s of list(obj(document)['Statement']).map(obj)) {
      for (const action of list(s['Action'])) {
        for (const resource of list(s['Resource'])) {
          actual.add(JSON.stringify([s['Effect'], action, resource]));
        }
      }
    }
  }
  const expected = new Set<string>();
  for (const g of grants) {
    for (const action of g.actions) expected.add(JSON.stringify(['Allow', action, g.resource]));
  }
  const missing = [...expected].filter((e) => !actual.has(e));
  const extra = [...actual].filter((a) => !expected.has(a));
  if (missing.length > 0) problems.push(`${name} role: missing grants ${missing.join(' ')}`);
  if (extra.length > 0) problems.push(`${name} role: unexpected grants ${extra.join(' ')}`);
  return problems;
}

/** Identity policies: no wildcard action, no "*" resource, no Not* forms, no table-level writes. */
function checkIdentityStatements(resources: Record<string, Fields>): string[] {
  const problems: string[] = [];
  for (const [id, r] of Object.entries(resources)) {
    const properties = obj(r['Properties']);
    let documents: unknown[];
    if (r['Type'] === 'AWS::IAM::Role') {
      if (properties['ManagedPolicyArns'] !== undefined) {
        problems.push(`${id}: managed policies are not allowed`);
      }
      documents = list(properties['Policies']).map((p) => obj(p)['PolicyDocument']);
    } else if (r['Type'] === 'AWS::IAM::Policy') {
      documents = [properties['PolicyDocument']];
    } else {
      continue;
    }
    for (const s of documents.flatMap((d) => list(obj(d)['Statement']).map(obj))) {
      for (const key of ['NotAction', 'NotResource', 'NotPrincipal']) {
        if (key in s) problems.push(`${id}: ${key} is not allowed`);
      }
      if (s['Effect'] !== 'Allow') problems.push(`${id}: only Allow statements are expected`);
      for (const action of list(s['Action'])) {
        if (typeof action !== 'string' || action.includes('*')) {
          problems.push(`${id}: wildcard or non-literal action ${JSON.stringify(action)}`);
        } else if (TABLE_LEVEL_WRITES.includes(action)) {
          problems.push(`${id}: table-level write ${action}`);
        }
      }
      const resourceList = list(s['Resource']);
      if (resourceList.length === 0) problems.push(`${id}: statement without Resource`);
      for (const resource of resourceList) {
        if (typeof resource === 'string' && resource.includes('*')) {
          problems.push(`${id}: wildcard resource ${resource}`);
        }
      }
    }
  }
  return problems;
}

/**
 * No literal account or region, no Fn::GetAZs, no URL or host except the two service principals.
 * With `deployTimeValues`, the operator-supplied targets and monitors (which hold URLs) are skipped.
 */
function scanLiterals(template: unknown, deployTimeValues: boolean): string[] {
  const problems = new Set<string>();
  const visit = (value: unknown, path: string, key: string): void => {
    if (Array.isArray(value)) {
      value.forEach((v, i) => visit(v, `${path}[${i}]`, key));
    } else if (value !== null && typeof value === 'object') {
      for (const [k, v] of Object.entries(value)) {
        if (k === 'Fn::GetAZs') problems.add(`${path}: Fn::GetAZs is not allowed`);
        visit(v, `${path}.${k}`, k);
      }
    } else if (typeof value === 'string') {
      if (deployTimeValues && DEPLOY_TIME_PATH.test(path)) return;
      // Asset hashes (64 hex digits) can contain digit runs; an account ID never sits inside one.
      const unhashed = value.replace(/[0-9a-f]{64}/gi, '');
      if (/(?<![0-9])[0-9]{12}(?![0-9])/.test(unhashed)) {
        problems.add(`${path}: literal account ID`);
      }
      if (REGION.test(value)) problems.add(`${path}: literal region`);
      if (value.includes('://')) problems.add(`${path}: URL`);
      else if (HOSTNAME.test(value) && !(key === 'Service' && SERVICE_PRINCIPALS[value])) {
        problems.add(`${path}: host name ${value}`);
      }
    }
  };
  visit(template, '$', '');
  return [...problems];
}
