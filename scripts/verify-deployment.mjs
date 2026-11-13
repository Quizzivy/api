import assert from 'node:assert/strict';

const api = process.env.API_ORIGIN ?? 'https://api.quizzivy.com';
const webs = (process.env.WEB_ORIGINS ?? 'https://my.quizzivy.com,https://portal.quizzivy.com').split(',').map((origin) => origin.trim()).filter(Boolean);
const headers = process.argv.includes('--headers');

async function response(url) {
  const result = await fetch(url, { signal: AbortSignal.timeout(15000), redirect: 'error' });
  assert.equal(result.status, 200, `${new URL(url).pathname}: HTTP ${result.status}`);
  if (headers) {
    for (const name of ['content-security-policy', 'strict-transport-security', 'permissions-policy', 'referrer-policy']) {
      assert.ok(result.headers.get(name), `Missing ${name}`);
    }
    assert.equal(result.headers.get('x-content-type-options'), 'nosniff');
    assert.match(result.headers.get('content-security-policy'), /frame-ancestors 'none'/);
  }
  return result;
}

try {
  const [health, ...pages] = await Promise.all([response(`${api}/healthz`), ...webs.map((web) => response(`${web}/login`))]);
  const body = await health.json();
  assert.equal(body.status, 'ok', 'API reports degraded health');
  assert.equal(body.database, 'ok', 'Database probe failed');
  for (const [index, page] of pages.entries()) {
    assert.match(page.headers.get('content-type') ?? '', /text\/html/, `${webs[index]} does not serve HTML`);
    assert.match(await page.text(), /id="root"/, `${webs[index]}: SPA root is missing`);
  }
  console.log(`PASS ${new Date().toISOString()} API, database and SPA on ${webs.length} hosts${headers ? ', including security headers' : ''}`);
} catch (error) {
  console.error(`FAIL ${new Date().toISOString()} ${error.message}`);
  process.exitCode = 1;
}
