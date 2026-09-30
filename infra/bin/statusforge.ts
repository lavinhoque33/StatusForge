// App entry for `cdk synth` (see cdk.json). Run only through `make infra-synth`.
import { buildApp, LAMBDA_BUILD_DIR } from '../lib/statusforge-stack.ts';

buildApp({ lambdaAssetRoot: LAMBDA_BUILD_DIR }).app.synth();
