// Run by `make infra-synth` after the CLI synthesizes infra/cdk.out: checks the
// CLI-produced template (§10.2–§10.3, closed defaults) and that no lookup
// context was written.
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

import { readDynamoDBActions, STACK_NAME } from '../lib/statusforge-stack.ts';
import { checkTemplate, obj } from '../lib/template-rules.ts';

const infra = join(import.meta.dirname, '..');
const assembly = join(infra, 'cdk.out');
const problems: string[] = [];

if (existsSync(join(infra, 'cdk.context.json'))) {
  problems.push('infra/cdk.context.json exists: synthesis must never look anything up');
}
const manifest = obj(JSON.parse(readFileSync(join(assembly, 'manifest.json'), 'utf8')));
if (Array.isArray(manifest['missing']) && manifest['missing'].length > 0) {
  problems.push('the cloud assembly requests context lookups');
}
const stacks = Object.entries(obj(manifest['artifacts'])).filter(
  ([, a]) => obj(a)['type'] === 'aws:cloudformation:stack',
);
if (stacks.length !== 1 || stacks[0][0] !== STACK_NAME) {
  problems.push(`expected exactly one stack, ${STACK_NAME}; found ${stacks.map(([id]) => id)}`);
} else {
  const templateFile = String(obj(obj(stacks[0][1])['properties'])['templateFile']);
  const template: unknown = JSON.parse(readFileSync(join(assembly, templateFile), 'utf8'));
  problems.push(
    ...checkTemplate(template, { actions: readDynamoDBActions(), closedDefaults: true }),
  );
}

if (problems.length > 0) {
  console.error(`template check: FAIL\n  ${problems.join('\n  ')}`);
  process.exit(1);
}
console.log(`template check: PASS (${STACK_NAME}: §10.2 resources, §10.3 IAM, closed defaults)`);
