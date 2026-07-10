/**
 * Local repro: "Show me all the LG ACs you have" — attempts START..END.
 * Uses fill + Send click (same as verified browser_type + browser_click flow).
 */
import { chromium } from 'playwright';
import { appendFileSync } from 'fs';

const URL = 'http://app.localhost:8080/amit-electronics-test-2';
const PROMPT = 'Show me all the LG ACs you have';
const FOLLOWUP = 'Split AC please, show me all the options you have.';
const START = Number(process.env.START_ATTEMPT || 7);
const END = Number(process.env.END_ATTEMPT || 30);
const OUT = '/tmp/repro-30-local-results.jsonl';

function checkState() {
  const body = document.body.innerText;
  const userSent = body.includes(PROMPT);
  const followupSent = body.includes(FOLLOWUP);
  const acHeadings = [...document.querySelectorAll('h4')]
    .map((h) => h.textContent)
    .filter((t) => /LG|Dual Inverter|Split AC/i.test(t || ''));
  const imgs = [...document.querySelectorAll('img')]
    .map((i) => i.src)
    .filter((s) => /jiostore|amazon|poorvika|imimg/i.test(s));
  const hasACImages =
    acHeadings.some((t) => /LG.*AC|Dual Inverter|Split/i.test(t || '')) || imgs.length > 0;
  const afterPrompt = userSent ? body.slice(body.indexOf(PROMPT)) : '';
  const clarifying = userSent && /\?/.test(afterPrompt) && !hasACImages;
  return { userSent, followupSent, hasACImages, clarifying, bugLikely: userSent && !hasACImages };
}

async function runAttempt(page, n) {
  await page.goto(URL, { waitUntil: 'domcontentloaded', timeout: 60000 });
  await page.waitForTimeout(5000);

  const accept = page.getByRole('button', { name: /Accept & Start/i });
  await accept.waitFor({ state: 'visible', timeout: 30000 });
  for (let i = 0; i < 40; i++) {
    if (await accept.isEnabled()) break;
    await page.waitForTimeout(500);
  }
  if (await accept.isEnabled()) await accept.click();

  const mute = page.getByRole('button', { name: /Mute microphone/i });
  if (await mute.isVisible().catch(() => false)) await mute.click().catch(() => {});
  await page.waitForTimeout(7000);

  const input = page.getByPlaceholder(/Ask anything/i);
  await input.click();
  await input.fill(PROMPT);

  const send = page.getByRole('button', { name: /^Send$/i });
  await page.waitForFunction(() => {
    const btn = [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Send');
    return btn && !btn.disabled;
  }, { timeout: 20000 });
  await send.click();
  await page.waitForTimeout(7000);

  let r = await page.evaluate(checkState);
  let followupSent = false;
  if (r.clarifying && !r.hasACImages) {
    await input.click();
    await input.fill(FOLLOWUP);
    await page.waitForFunction(() => {
      const btn = [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Send');
      return btn && !btn.disabled;
    }, { timeout: 20000 });
    await send.click();
    followupSent = true;
    await page.waitForTimeout(8000);
    r = await page.evaluate(checkState);
  }

  const line = { attempt: n, ...r, followupSent: followupSent || r.followupSent };
  appendFileSync(OUT, JSON.stringify(line) + '\n');
  process.stderr.write(`attempt ${n}: ${JSON.stringify(line)}\n`);
  return line;
}

const browser = await chromium.launch({ channel: 'chrome', headless: false });
const context = await browser.newContext();
await context.grantPermissions(['microphone'], { origin: 'http://app.localhost:8080' });
const page = await context.newPage();

for (let n = START; n <= END; n++) {
  try {
    await runAttempt(page, n);
  } catch (e) {
    const line = {
      attempt: n,
      userSent: false,
      followupSent: false,
      hasACImages: false,
      clarifying: false,
      bugLikely: true,
      error: String(e.message || e),
    };
    appendFileSync(OUT, JSON.stringify(line) + '\n');
    process.stderr.write(`attempt ${n} ERROR: ${e.message}\n`);
  }
}
await browser.close();
