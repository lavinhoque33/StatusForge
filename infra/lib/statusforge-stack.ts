// The StatusForge cloud path: synthesized only, never
// deployed from this repository. Every resource and permission here is
// checked by test/stack.test.ts and scripts/check-template.ts.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import {
  App,
  Aws,
  DefaultStackSynthesizer,
  Duration,
  RemovalPolicy,
  Stack,
  type StackProps,
} from 'aws-cdk-lib';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as scheduler from 'aws-cdk-lib/aws-scheduler';
import * as sqs from 'aws-cdk-lib/aws-sqs';
import type { Construct } from 'constructs';

import { obj } from './template-rules.ts';

export const STACK_NAME = 'StatusForge';

const INFRA_ROOT = join(import.meta.dirname, '..');

/** The committed cdk.json: the CLI passes its context to the app, and the tests read it here. */
export const CDK_JSON = join(INFRA_ROOT, 'cdk.json');

/** The DynamoDB actions each Lambda role is granted, derived from the Go contract tests. */
export const DYNAMODB_ACTIONS_FILE = join(INFRA_ROOT, 'iam', 'dynamodb-actions.json');

/** Where `make lambda-build` writes the two `bootstrap` binaries. */
export const LAMBDA_BUILD_DIR = join(INFRA_ROOT, '..', 'backend', 'bin', 'lambda');

/** Deploy-time configuration. Each key defaults to closed. */
export const CONTEXT_MONITORS = 'statusforge:cloudMonitors';
export const CONTEXT_TARGETS = 'statusforge:cloudTargets';
export const CONTEXT_SCHEDULE_STATE = 'statusforge:scheduleState';

export interface DynamoDBActions {
  planner: string[];
  worker: string[];
}

export function readDynamoDBActions(): DynamoDBActions {
  const parsed: unknown = JSON.parse(readFileSync(DYNAMODB_ACTIONS_FILE, 'utf8'));
  const valid = (list: unknown): list is string[] =>
    Array.isArray(list) &&
    list.length > 0 &&
    list.every((a) => typeof a === 'string' && /^dynamodb:[A-Za-z]+$/.test(a));
  if (
    typeof parsed === 'object' &&
    parsed !== null &&
    Object.keys(parsed).sort().join() === 'planner,worker' &&
    'planner' in parsed &&
    'worker' in parsed &&
    valid(parsed.planner) &&
    valid(parsed.worker)
  ) {
    return { planner: parsed.planner, worker: parsed.worker };
  }
  throw new Error(`${DYNAMODB_ACTIONS_FILE}: expected {"planner": [...], "worker": [...]}`);
}

export interface StatusForgeStackProps extends StackProps {
  /** Directory holding `planner/bootstrap` and `worker/bootstrap`. */
  readonly lambdaAssetRoot: string;
  readonly dynamodbActions: DynamoDBActions;
}

export class StatusForgeStack extends Stack {
  constructor(scope: Construct, id: string, props: StatusForgeStackProps) {
    super(scope, id, props);

    const monitors = contextMonitors(this);
    const targets = contextString(this, CONTEXT_TARGETS, '');
    const scheduleState = contextString(this, CONTEXT_SCHEDULE_STATE, 'DISABLED');
    if (scheduleState !== 'ENABLED' && scheduleState !== 'DISABLED') {
      throw new Error(`${CONTEXT_SCHEDULE_STATE}: must be ENABLED or DISABLED`);
    }

    // TableV2 in provisioned mode requires autoscaled writes; Table does not.
    const table = new dynamodb.Table(this, 'Table', {
      partitionKey: { name: 'PK', type: dynamodb.AttributeType.STRING },
      sortKey: { name: 'SK', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PROVISIONED,
      readCapacity: 10,
      writeCapacity: 10,
      timeToLiveAttribute: 'expiresAt',
      pointInTimeRecoverySpecification: { pointInTimeRecoveryEnabled: false },
      deletionProtection: false,
      removalPolicy: RemovalPolicy.DESTROY,
    });

    const queue = (id: string, props: sqs.QueueProps) =>
      new sqs.Queue(this, id, {
        encryption: sqs.QueueEncryption.SQS_MANAGED,
        enforceSSL: true,
        removalPolicy: RemovalPolicy.DESTROY,
        ...props,
      });
    const workDeadLetterQueue = queue('WorkDeadLetterQueue', {
      retentionPeriod: Duration.days(14),
    });
    // Visibility is 6× the worker timeout, so a slow invocation never frees its message early.
    const workQueue = queue('WorkQueue', {
      visibilityTimeout: Duration.seconds(360),
      retentionPeriod: Duration.days(1),
      deadLetterQueue: { queue: workDeadLetterQueue, maxReceiveCount: 3 },
    });
    const scheduleDeadLetterQueue = queue('ScheduleDeadLetterQueue', {
      retentionPeriod: Duration.days(14),
    });

    const planner = this.lambda('Planner', {
      assetRoot: props.lambdaAssetRoot,
      timeout: Duration.seconds(50),
      environment: {
        STATUSFORGE_TABLE: table.tableName,
        STATUSFORGE_WORK_QUEUE_URL: workQueue.queueUrl,
        STATUSFORGE_CLOUD_TARGETS: targets,
        STATUSFORGE_CLOUD_MONITORS: monitors,
        STATUSFORGE_LOG_LEVEL: 'info',
      },
      statements: [
        new iam.PolicyStatement({
          actions: props.dynamodbActions.planner,
          resources: [table.tableArn],
        }),
        // SendMessage also authorises SendMessageBatch.
        new iam.PolicyStatement({ actions: ['sqs:SendMessage'], resources: [workQueue.queueArn] }),
      ],
    });
    // Scheduler invokes the planner asynchronously; the next minute's pass supersedes a failed one.
    planner.configureAsyncInvoke({ retryAttempts: 0, maxEventAge: Duration.seconds(60) });

    const worker = this.lambda('Worker', {
      assetRoot: props.lambdaAssetRoot,
      timeout: Duration.seconds(60),
      environment: {
        STATUSFORGE_TABLE: table.tableName,
        STATUSFORGE_CLOUD_TARGETS: targets,
        STATUSFORGE_LOG_LEVEL: 'info',
      },
      statements: [
        new iam.PolicyStatement({
          actions: props.dynamodbActions.worker,
          resources: [table.tableArn],
        }),
        new iam.PolicyStatement({
          actions: [
            'sqs:ReceiveMessage',
            'sqs:DeleteMessage',
            'sqs:ChangeMessageVisibility',
            'sqs:GetQueueAttributes',
          ],
          resources: [workQueue.queueArn],
        }),
      ],
    });
    // A plain mapping: SqsEventSource would also grant sqs:GetQueueUrl.
    new lambda.EventSourceMapping(this, 'WorkerEventSource', {
      target: worker,
      eventSourceArn: workQueue.queueArn,
      batchSize: 1,
      reportBatchItemFailures: true,
      maxConcurrency: 2,
    });

    // Scheduler uses this role, so no resource-based Lambda permission exists.
    const scheduleRole = new iam.Role(this, 'ScheduleRole', {
      assumedBy: new iam.ServicePrincipal('scheduler.amazonaws.com', {
        conditions: { StringEquals: { 'aws:SourceAccount': Aws.ACCOUNT_ID } },
      }),
      inlinePolicies: {
        Schedule: new iam.PolicyDocument({
          statements: [
            new iam.PolicyStatement({
              actions: ['lambda:InvokeFunction'],
              resources: [planner.functionArn],
            }),
            new iam.PolicyStatement({
              actions: ['sqs:SendMessage'],
              resources: [scheduleDeadLetterQueue.queueArn],
            }),
          ],
        }),
      },
    });
    // L1: the L2 Lambda target would grant invoke on every version and alias as well.
    new scheduler.CfnSchedule(this, 'PlannerSchedule', {
      scheduleExpression: 'rate(1 minute)',
      flexibleTimeWindow: { mode: 'OFF' },
      state: scheduleState,
      target: {
        arn: planner.functionArn,
        roleArn: scheduleRole.roleArn,
        retryPolicy: { maximumEventAgeInSeconds: 60, maximumRetryAttempts: 2 },
        deadLetterConfig: { arn: scheduleDeadLetterQueue.queueArn },
      },
    });
  }

  private lambda(
    name: 'Planner' | 'Worker',
    options: {
      assetRoot: string;
      timeout: Duration;
      environment: Record<string, string>;
      statements: iam.PolicyStatement[];
    },
  ): lambda.Function {
    const logGroup = new logs.LogGroup(this, `${name}LogGroup`, {
      retention: logs.RetentionDays.THREE_DAYS,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    // An explicit role: the default one attaches the AWSLambdaBasicExecutionRole managed policy.
    const role = new iam.Role(this, `${name}Role`, {
      assumedBy: new iam.ServicePrincipal('lambda.amazonaws.com'),
      inlinePolicies: {
        [name]: new iam.PolicyDocument({
          statements: [
            ...options.statements,
            new iam.PolicyStatement({
              actions: ['logs:CreateLogStream', 'logs:PutLogEvents'],
              resources: [logGroup.logGroupArn],
            }),
          ],
        }),
      },
    });
    return new lambda.Function(this, name, {
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.ARM_64,
      memorySize: 128,
      handler: 'bootstrap',
      code: lambda.Code.fromAsset(join(options.assetRoot, name.toLowerCase())),
      timeout: options.timeout,
      logGroup,
      role,
      environment: options.environment,
    });
  }
}

/** Monitors pass unchanged: a JSON string from `-c`, or a JSON value from cdk.json. */
function contextMonitors(scope: Construct): string {
  const value: unknown = scope.node.tryGetContext(CONTEXT_MONITORS) ?? [];
  return typeof value === 'string' ? value : JSON.stringify(value);
}

function contextString(scope: Construct, key: string, fallback: string): string {
  const value: unknown = scope.node.tryGetContext(key) ?? fallback;
  if (typeof value !== 'string') {
    throw new Error(`${key}: must be a string`);
  }
  return value;
}

export interface BuildOptions {
  /** Directory holding `planner/bootstrap` and `worker/bootstrap`. */
  readonly lambdaAssetRoot: string;
  /** Context applied over cdk.json and any CLI context (tests only). */
  readonly context?: Record<string, unknown>;
  /** Cloud assembly directory; the CLI sets it through the environment. */
  readonly outdir?: string;
}

/**
 * Builds the app and its one stack. The app entry and the tests both call it,
 * so the feature flags from cdk.json and the stack props always match.
 */
export function buildApp(options: BuildOptions): { app: App; stack: StatusForgeStack } {
  const cdkJson = obj(JSON.parse(readFileSync(CDK_JSON, 'utf8')));
  const app = new App({
    context: obj(cdkJson['context']),
    postCliContext: options.context,
    outdir: options.outdir,
    analyticsReporting: false,
  });
  // No env: the stack is environment-agnostic, so nothing looks up an account or region.
  const stack = new StatusForgeStack(app, STACK_NAME, {
    synthesizer: new DefaultStackSynthesizer(),
    lambdaAssetRoot: options.lambdaAssetRoot,
    dynamodbActions: readDynamoDBActions(),
  });
  return { app, stack };
}
