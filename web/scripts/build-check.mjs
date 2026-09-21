/**
 * Runs a production build into `.next-check` instead of `.next`.
 *
 * Why this exists: `next build` and `next dev` share `.next` by default, and
 * the build rewrites manifests the dev server is still holding open. The dev
 * server does not notice — it simply answers every page with a 500 until
 * `.next` is deleted and it is restarted. Verifying that the app still builds
 * should not knock over the app you are looking at.
 *
 * next.config.mjs reads NEXT_DIST_DIR, so setting it here is the whole trick.
 * `cross-env` would do the same job as a dependency; a six-line script does not
 * need one.
 */
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

const child = spawn(
  process.execPath,
  [join(webRoot, 'node_modules', 'next', 'dist', 'bin', 'next'), 'build'],
  {
    cwd: webRoot,
    stdio: 'inherit',
    env: { ...process.env, NEXT_DIST_DIR: '.next-check' },
  },
);

child.on('exit', (code) => process.exit(code ?? 1));
