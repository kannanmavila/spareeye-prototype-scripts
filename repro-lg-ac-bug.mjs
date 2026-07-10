/**
 * Retry typed input "Show me all the LG ACs you have" up to N times.
 * Bug = user message sent but no product image card after optional follow-up.
 */
import { chromium } from 'playwright';

const URL = 'https://app.shelftalker.ai/amit-electronics-test-2';
const PROMPT = 'Show me all the LG ACs you have';
const FOLLOWUP = 'Split AC please, show me all the options you have.';
const MAX_ATTEMPTS = 30;
const WAIT_AFTER_SEND_MS = 8000;

function evaluateState(prompt, followup) {
  const text = document.body.innerText || '';
  const userSent = text.includes(prompt);
  const followupSent = text.includes(followup);
  const hasLGACHeading = /\bLG AC/i.test(text);
  const hasProductName = /LG 1\.5 Ton|Dual Inverter Split AC|RS-Q19YNZE/i.test(text);
  const productImgs = [...document.querySelectorAll('img')].filter((i) => {
    const src = i.src || '';
    return (
      src.includes('jiostore') ||
      src.includes('amazon.com/images') ||
      src.includes('poorvika') ||
      src.includes('imimg.com') ||
      src.includes('spareeye-prototype-image')
    );
  });
  const hasImages = productImgs.length > 0 || (hasLGACHeading && hasProductName);
  const afterPrompt = userSent ? text.slice(text.indexOf(prompt)) : '';
  const clarifying = userSent && /\?/.test(afterPrompt) && !hasImages;
  const assistantLines = text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .filter((l) => !l.includes('Accept & Start') && !l.includes('Privacy Notice'));
  return {
    userSent,
    followupSent,
    hasImages,
    hasLGACHeading,
    hasProductName,
    productImgCount: productImgs.length,
    clarifying,
    assistantTail: assistantLines.slice(-6),
  };
}

async function attemptOnce(page, n) {
  const startedAt = new Date().toISOString();
  await page.goto(URL, { waitUntil: 'domcontentloaded', timeout: 90000 });

  const accept = page.getByRole('button', { name: /Accept & Start/i });
  await accept.waitFor({ state: 'visible', timeout: 30000 });
  for (let i = 0; i < 40; i++) {
    if (await accept.isEnabled()) break;
    await page.waitForTimeout(500);
  }
  if (await accept.isEnabled()) {
    await accept.click();
  }

  await page.getByText(/Welcome to the Apple Store|How can I assist|How may I assist/i).waitFor({
    timeout: 45000,
  });

  const muteMic = page.getByRole('button', { name: /Mute microphone/i });
  if (await muteMic.isVisible().catch(() => false)) {
    await muteMic.click({ timeout: 3000 }).catch(() => {});
  }

  const input = page.getByPlaceholder(/Ask anything/i);
  await input.waitFor({ state: 'visible', timeout: 10000 });
  await input.click();
  await input.fill(PROMPT);

  const send = page.getByRole('button', { name: /^Send$/i });
  await page.waitForFunction(() => {
    const btn = [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Send');
    return btn && !btn.disabled;
  }, { timeout: 15000 });
  await send.click();

  await page.getByText(PROMPT, { exact: true }).waitFor({ timeout: 15000 });
  await page.waitForTimeout(WAIT_AFTER_SEND_MS);

  let result = await page.evaluate(evaluateState, PROMPT, FOLLOWUP);
  let followupSent = false;

  if (result.clarifying && !result.hasImages) {
    await input.click();
    await input.fill(FOLLOWUP);
    await page.waitForFunction(() => {
      const btn = [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Send');
      return btn && !btn.disabled;
    }, { timeout: 15000 });
    await send.click();
    await page.getByText(FOLLOWUP, { exact: true }).waitFor({ timeout: 15000 });
    followupSent = true;
    await page.waitForTimeout(WAIT_AFTER_SEND_MS);
    result = await page.evaluate(evaluateState, PROMPT, FOLLOWUP);
    result.followupSent = followupSent;
  }

  const bugLikely = result.userSent && !result.hasImages;
  return { attempt: n, startedAt, bugLikely, ...result };
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext();
await context.grantPermissions(['microphone'], { origin: 'https://app.shelftalker.ai' });
const page = await context.newPage();

const results = [];
for (let n = 1; n <= MAX_ATTEMPTS; n++) {
  process.stderr.write(`Attempt ${n}/${MAX_ATTEMPTS}...\n`);
  try {
    const r = await attemptOnce(page, n);
    results.push(r);
    if (r.bugLikely) {
      process.stderr.write(`  -> BUG CANDIDATE (no images)\n`);
    } else if (!r.userSent) {
      process.stderr.write(`  -> FAIL: user message not sent\n`);
    } else {
      process.stderr.write(`  -> OK: images=${r.hasImages} clarifying=${r.clarifying} followup=${r.followupSent}\n`);
    }
  } catch (err) {
    results.push({
      attempt: n,
      startedAt: new Date().toISOString(),
      error: String(err?.message || err),
      bugLikely: true,
      hasImages: false,
      userSent: false,
    });
    process.stderr.write(`  -> ERROR: ${err.message}\n`);
  }
}

await browser.close();

const summary = {
  prompt: PROMPT,
  followup: FOLLOWUP,
  maxAttempts: MAX_ATTEMPTS,
  waitAfterSendMs: WAIT_AFTER_SEND_MS,
  okWithImages: results.filter((r) => r.hasImages).length,
  bugCandidates: results.filter((r) => r.bugLikely).length,
  errors: results.filter((r) => r.error).length,
  clarifyingOnly: results.filter((r) => r.clarifying && !r.hasImages && !r.followupSent).length,
  parallelBatchFound: null,
  results,
};

console.log(JSON.stringify(summary, null, 2));
