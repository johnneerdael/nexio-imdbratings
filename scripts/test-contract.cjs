const { spawn } = require('child_process');
const path = require('path');
const { setTimeout: delay } = require('timers/promises');
const Dredd = require('dredd');

const apiEndpoint = 'http://127.0.0.1:4010';
const apiKey = 'contract-test-key';

async function waitForHealthy(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${url}/healthz`);
      if (response.ok) {
        return;
      }
    } catch (_) {
      // The stub may still be starting up.
    }

    await delay(250);
  }

  throw new Error(`contract stub did not become healthy within ${timeoutMs}ms`);
}

function runDredd(cwd) {
  return new Promise((resolve, reject) => {
    new Dredd({
      path: [path.join(cwd, 'docs/api.apib')],
      endpoint: apiEndpoint,
      hookfiles: [],
      language: 'nodejs',
      reporter: ['cli'],
      output: [],
      header: [`X-API-Key: ${apiKey}`],
      method: [],
      only: [],
      color: true,
      loglevel: 'warning',
      sorted: false,
      names: false,
      custom: { cwd, apiUrl: apiEndpoint },
      apiDescriptions: [],
      http: {},
    }).run((err, stats) => {
      if (err) {
        reject(err);
        return;
      }
      resolve(stats);
    });
  });
}

async function assertEpisodesEndpoints() {
  const singleResponse = await fetch(`${apiEndpoint}/v1/ratings/tt0944947?episodes=true`, {
    headers: { 'X-API-Key': apiKey },
  });
  if (!singleResponse.ok) {
    throw new Error(`single episodes lookup failed with ${singleResponse.status}`);
  }

  const singlePayload = await singleResponse.json();
  if (
    singlePayload.requestTconst !== 'tt0944947' ||
    singlePayload.episodesParentTconst !== 'tt0944947' ||
    !Array.isArray(singlePayload.episodes) ||
    singlePayload.episodes.length !== 1 ||
    singlePayload.episodes[0].seasonNumber !== 1 ||
    singlePayload.episodes[0].episodeNumber !== 1
  ) {
    throw new Error(`single episodes lookup returned unexpected payload: ${JSON.stringify(singlePayload)}`);
  }

  const bulkResponse = await fetch(`${apiEndpoint}/v1/ratings/bulk?episodes=true`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': apiKey,
    },
    body: JSON.stringify({ identifiers: ['tt0944947', 'tt1480055', 'tt-missing'] }),
  });
  if (!bulkResponse.ok) {
    throw new Error(`bulk episodes lookup failed with ${bulkResponse.status}`);
  }

  const bulkPayload = await bulkResponse.json();
  if (
    !Array.isArray(bulkPayload.results) ||
    bulkPayload.results.length !== 2 ||
    bulkPayload.results[1].episodesParentTconst !== 'tt0944947' ||
    !Array.isArray(bulkPayload.missing) ||
    bulkPayload.missing.length !== 1 ||
    bulkPayload.missing[0] !== 'tt-missing'
  ) {
    throw new Error(`bulk episodes lookup returned unexpected payload: ${JSON.stringify(bulkPayload)}`);
  }
}

async function stopServer(server) {
  if (server.exitCode !== null || server.signalCode !== null) {
    return;
  }

  server.kill('SIGTERM');
  await Promise.race([
    new Promise((resolve) => server.once('exit', resolve)),
    delay(5000).then(() => {
      server.kill('SIGKILL');
    }),
  ]);
}

async function main() {
  const repoRoot = path.resolve(__dirname, '..');
  const server = spawn('go', ['run', './cmd/contractstub'], {
    cwd: path.join(repoRoot, 'apps/api'),
    stdio: 'inherit',
  });

  try {
    await waitForHealthy(apiEndpoint, 10000);

    const stats = await runDredd(repoRoot);
    if (stats.errors > 0 || stats.failures > 0) {
      throw new Error(`dredd failed: ${stats.failures} failures, ${stats.errors} errors`);
    }

    await assertEpisodesEndpoints();
    console.log(`contract checks passed: ${stats.passes} dredd transactions plus explicit episodes=true coverage`);
  } finally {
    await stopServer(server);
  }
}

main().catch((error) => {
  console.error(error.message);
  process.exit(1);
});
