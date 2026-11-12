import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { test } from 'node:test';

const script = readFileSync('scripts/ci/push-image.sh', 'utf8');

/** Every workflow and composite action, with its comment lines dropped. */
function steps() {
  const files = readdirSync('.github/workflows').map((name) => `.github/workflows/${name}`);
  for (const name of readdirSync('.github/actions')) files.push(`.github/actions/${name}/action.yml`);
  return files.map((file) => ({
    file,
    code: readFileSync(file, 'utf8')
      .split('\n')
      .filter((line) => !line.trim().startsWith('#'))
      .join('\n'),
  }));
}

// A push from the runner's own store mounts layers it knows from another
// ghcr.io repository, and the registry refused the manifest that named them
// (the first deploy to the VPS, 2026-11-12). The script's header has it all.
test('no workflow pushes an image from the runner\'s own store', () => {
  for (const { file, code } of steps()) {
    assert.doesNotMatch(code, /docker push/, `${file} runs docker push: use scripts/ci/push-image.sh`);
  }
});

test('the two images that are published go through push-image.sh', () => {
  const published = steps().filter(({ code }) => code.includes('scripts/ci/push-image.sh'));
  assert.deepEqual(published.map(({ file }) => file).sort(), [
    '.github/workflows/deploy.yml',
    '.github/workflows/minio-image.yml',
  ]);
});

test('push-image.sh builds in a builder of its own and pushes from it', () => {
  assert.match(script, /docker buildx create --driver docker-container /);
  assert.match(script, /docker buildx build --builder "\$builder" --push --tag "\$image" "\$context"/);
  // --use would make it the default builder for the rest of the job.
  assert.doesNotMatch(script, /--use\b/);
});

test('the builder takes Docker Hub images from the mirror', () => {
  assert.match(script, /\[registry\."docker\.io"\]\n\s+mirrors = \["mirror\.gcr\.io"\]/);
  assert.match(script, /mirror\.gcr\.io\/moby\/buildkit:/);
});

test('a change to the script publishes MinIO again', () => {
  const workflow = readFileSync('.github/workflows/minio-image.yml', 'utf8');
  assert.equal(workflow.split('- scripts/ci/push-image.sh').length - 1, 2);
});
