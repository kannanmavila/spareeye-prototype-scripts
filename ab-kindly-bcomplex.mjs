/**
 * A/B: local (thinking_budget=0) vs production for product images.
 * On failure: saves WebM + GIF and a JSON log with ISO timestamps for log correlation.
 *
 * Usage: node ab-kindly-bcomplex.mjs [surfaceId] [prompt]
 * Example: node ab-kindly-bcomplex.mjs amit-electronics-test "show me your top gaming laptops"
 * Output: Random-Scripts/ab-captures/<surfaceId>/
 */
import { chromium } from 'playwright';
import { execSync } from 'child_process';
import { mkdirSync, writeFileSync, existsSync, appendFileSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));

const SURFACE_ID = process.argv[2] || 'kindly-test-1';
const PROMPT = process.argv[3] || 'show me all your b-complex options';

const CAPTURE_DIR = join(__dirname, 'ab-captures', SURFACE_ID);
const LOG_JSON = join(CAPTURE_DIR, 'run-log.json');
const LOG_TXT = join(CAPTURE_DIR, 'run-log.txt');

const IMAGE_PASS_MS = 5000;
const IMAGE_OBSERVE_MS = 45000;
const MAX_RUNS = 30;

const ENVIRONMENTS = [
  { label: 'local', url: `https://localhost:5173/${SURFACE_ID}` },
  { label: 'production', url: `https://www.shelftalker.ai/${SURFACE_ID}` },
];

function isoNow() {
  return new Date().toISOString();
}

function logEvent(events, name, extra = {}) {
  const entry = { at: isoNow(), event: name, ...extra };
  events.push(entry);
  return entry;
}

function appendHumanLog(line) {
  appendFileSync(LOG_TXT, `${line}\n`);
}

function webmToGif(webmPath, gifPath) {
  execSync(
    `ffmpeg -y -i "${webmPath}" -vf "fps=8,scale=720:-1:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse" -loop 0 "${gifPath}"`,
    { stdio: 'pipe' },
  );
}

async function finalizeFailureCapture(ctx, page, envLabel, runNum, result) {
  const base = `${envLabel}-run${runNum}-fail`;
  const gifOut = join(CAPTURE_DIR, `${base}.gif`);

  const video = page.video();
  await page.close();
  await ctx.close();

  let webmPath = null;
  if (video) {
    webmPath = await video.path();
  }

  if (webmPath && existsSync(webmPath)) {
    try {
      webmToGif(webmPath, gifOut);
      result.gifPath = gifOut;
      result.webmPath = webmPath;
      const metaPath = join(CAPTURE_DIR, `${base}-timestamps.json`);
      writeFileSync(metaPath, JSON.stringify(result, null, 2));
      result.timestampsPath = metaPath;
      console.log(`  → GIF: ${gifOut}`);
      console.log(`  → WebM: ${webmPath}`);
      console.log(`  → Timestamps: ${metaPath}`);
    } catch (e) {
      result.gifError = e.message;
      result.webmPath = webmPath;
      console.log(`  → WebM saved (GIF conversion failed): ${webmPath}`);
      console.log(`  → ffmpeg error: ${e.message}`);
    }
  } else {
    result.captureError = 'no video recording available';
  }
}

async function runOnce(page, env, runNum, events) {
  const runStartedAt = isoNow();
  const t0 = Date.now();
  logEvent(events, 'run_start', { env: env.label, run: runNum, url: env.url });

  await page.goto(env.url, { waitUntil: 'domcontentloaded', timeout: 60000 });
  logEvent(events, 'page_loaded');

  const startBtn = page.getByRole('button', { name: /Accept & Start/i });
  await startBtn.waitFor({ state: 'visible', timeout: 30000 });
  await startBtn.click();
  logEvent(events, 'accept_and_start_clicked');

  const input = page.locator('.typed-input-field');
  try {
    await input.waitFor({ state: 'visible', timeout: 90000 });
    await page.locator('.welcome-overlay.hidden').waitFor({ timeout: 90000 });
    logEvent(events, 'session_ready', { note: 'typed input visible, welcome overlay hidden' });
  } catch (e) {
    logEvent(events, 'session_ready_failed', { error: e.message });
    return {
      env: env.label,
      run: runNum,
      runStartedAt,
      runEndedAt: isoNow(),
      imagesShown: false,
      detail: 'typed input never appeared after Accept & Start',
      elapsedMs: Date.now() - t0,
      events,
      failed: true,
    };
  }

  await input.fill(PROMPT);
  logEvent(events, 'prompt_filled', { prompt: PROMPT });

  const sendBtn = page.locator('.typed-input-send');
  await sendBtn.waitFor({ state: 'visible', timeout: 60000 });
  await page.waitForFunction(
    () => {
      const btn = document.querySelector('.typed-input-send');
      const field = document.querySelector('.typed-input-field');
      return btn && !btn.disabled && field?.value?.trim().length > 0;
    },
    { timeout: 90000 },
  );
  logEvent(events, 'websocket_ready', { note: 'send button enabled' });

  const sendAtIso = isoNow();
  const sendAt = Date.now();
  await sendBtn.click();
  logEvent(events, 'prompt_sent', { sendAt: sendAtIso });

  let imagesShown = false;
  let timeToImagesMs = null;
  let detail = '';
  const deadline = sendAt + IMAGE_OBSERVE_MS;

  while (Date.now() < deadline) {
    const count = await page.locator('.product-images-container img').count();
    if (count > 0) {
      timeToImagesMs = Date.now() - sendAt;
      imagesShown = timeToImagesMs <= IMAGE_PASS_MS;
      detail = `${count} image(s) at ${timeToImagesMs}ms after send`;
      logEvent(events, 'images_detected', {
        count,
        msAfterSend: timeToImagesMs,
        passWithin5s: imagesShown,
      });
      break;
    }
    await page.waitForTimeout(250);
  }

  const observeEndedAt = isoNow();
  if (timeToImagesMs === null) {
    detail = `no product images within ${IMAGE_OBSERVE_MS / 1000}s`;
    logEvent(events, 'observe_window_end', {
      at: observeEndedAt,
      outcome: 'no_images',
      observeMs: IMAGE_OBSERVE_MS,
    });
  } else if (!imagesShown) {
    detail += ` (FAIL: >${IMAGE_PASS_MS}ms)`;
    logEvent(events, 'observe_window_end', {
      at: observeEndedAt,
      outcome: 'images_too_late',
      msAfterSend: timeToImagesMs,
    });
  } else {
    logEvent(events, 'observe_window_end', {
      at: observeEndedAt,
      outcome: 'pass',
      msAfterSend: timeToImagesMs,
    });
  }

  const failed = !imagesShown;
  if (failed) {
    logEvent(events, 'failure', {
      at: isoNow(),
      detail,
      sendAt: sendAtIso,
      observeEndedAt,
    });
  }

  return {
    env: env.label,
    run: runNum,
    runStartedAt,
    runEndedAt: isoNow(),
    sendAt: sendAtIso,
    observeEndedAt,
    imagesShown,
    timeToImagesMs,
    detail,
    elapsedMs: Date.now() - t0,
    events,
    failed,
  };
}

async function main() {
  mkdirSync(CAPTURE_DIR, { recursive: true });
  console.log(`Surface: ${SURFACE_ID}`);
  console.log(`Prompt:  ${PROMPT}`);
  console.log(`Output:  ${CAPTURE_DIR}\n`);
  writeFileSync(LOG_TXT, `A/B capture log started ${isoNow()} | surface=${SURFACE_ID} | prompt=${PROMPT}\n`, 'utf8');

  try {
    execSync('ffmpeg -version', { stdio: 'pipe' });
  } catch {
    console.error('ffmpeg is required. Install with: brew install ffmpeg');
    process.exit(1);
  }

  const browser = await chromium.launch({
    headless: true,
    args: [
      '--use-fake-ui-for-media-stream',
      '--use-fake-device-for-media-stream',
    ],
  });

  const allResults = [];

  let stopped = false;
  for (let run = 1; run <= MAX_RUNS && !stopped; run++) {
    console.log(`\n=== Run ${run} (parallel) ===`);
    appendHumanLog(`\n=== Run ${run} @ ${isoNow()} ===`);

    // Fresh context per run with video (needed for clean capture on failure)
    const runContexts = await Promise.all(
      ENVIRONMENTS.map(async (env) => {
        const videoDir = join(CAPTURE_DIR, env.label);
        mkdirSync(videoDir, { recursive: true });
        const ctx = await browser.newContext({
          ignoreHTTPSErrors: true,
          permissions: ['microphone'],
          recordVideo: { dir: videoDir, size: { width: 1280, height: 720 } },
        });
        const page = await ctx.newPage();
        page.setDefaultTimeout(90000);
        return { env, ctx, page };
      }),
    );

    const runResults = await Promise.all(
      runContexts.map(async ({ env, page }) => {
        const events = [];
        try {
          return await runOnce(page, env, run, events);
        } catch (e) {
          logEvent(events, 'uncaught_error', { error: e.message });
          return {
            env: env.label,
            run,
            runStartedAt: isoNow(),
            runEndedAt: isoNow(),
            imagesShown: false,
            detail: `error: ${e.message}`,
            elapsedMs: 0,
            events,
            failed: true,
          };
        }
      }),
    );

    const anyFail = runResults.some((r) => r.failed);
    const failedIndices = new Set(
      runResults.map((r, i) => (r.failed ? i : -1)).filter((i) => i >= 0),
    );

    for (let i = 0; i < runResults.length; i++) {
      const r = runResults[i];
      const { ctx, page } = runContexts[i];

      if (failedIndices.has(i)) {
        console.log(`[${r.env}] run ${r.run}: FAIL — ${r.detail}`);
        console.log(`  Timestamps:`);
        console.log(`    run_started:  ${r.runStartedAt}`);
        if (r.sendAt) console.log(`    prompt_sent:  ${r.sendAt}`);
        if (r.observeEndedAt) console.log(`    observe_end:  ${r.observeEndedAt}`);
        console.log(`    run_ended:    ${r.runEndedAt}`);
        appendHumanLog(
          `[${r.env}] run ${r.run} FAIL @ ${r.runEndedAt} | send=${r.sendAt ?? 'n/a'} | ${r.detail}`,
        );
        for (const ev of r.events) {
          appendHumanLog(`  ${ev.at}  ${ev.event}  ${JSON.stringify({ ...ev, at: undefined, event: undefined })}`);
        }
        await finalizeFailureCapture(ctx, page, r.env, r.run, r);
      } else {
        await page.close();
        await ctx.close();
        const status = r.imagesShown ? 'PASS' : 'FAIL';
        console.log(`[${r.env}] run ${r.run}: ${status} — ${r.detail} (${r.elapsedMs}ms)`);
        appendHumanLog(`[${r.env}] run ${r.run} ${status} @ ${r.runEndedAt} — ${r.detail}`);
      }

      allResults.push(r);
    }

    if (anyFail) {
      console.log('\nStopping: at least one environment failed.');
      stopped = true;
      break;
    }

    await new Promise((r) => setTimeout(r, 1500));
  }

  writeFileSync(LOG_JSON, JSON.stringify({ completedAt: isoNow(), results: allResults }, null, 2));

  console.log('\n=== Summary ===');
  for (const env of ENVIRONMENTS) {
    const envRuns = allResults.filter((r) => r.env === env.label);
    const passes = envRuns.filter((r) => r.imagesShown).length;
    console.log(`${env.label}: ${passes}/${envRuns.length} passes`);
  }
  console.log(`\nLogs: ${LOG_JSON}`);
  console.log(`      ${LOG_TXT}`);

  await browser.close();
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
