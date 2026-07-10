#!/usr/bin/env node
/**
 * Drives MCP browser repro via stdout instructions - used manually by agent loop.
 * Each line printed is JSON result for one attempt.
 */
const PROMPT = 'Show me all the LG ACs you have';

export const attemptScript = `async () => {
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const prompt = ${JSON.stringify(PROMPT)};

  const accept = [...document.querySelectorAll('button')].find((b) => /Accept/i.test(b.textContent));
  for (let i = 0; i < 40; i++) {
    if (accept && !accept.disabled) { accept.click(); break; }
    await sleep(500);
  }

  let greeted = false;
  for (let i = 0; i < 60; i++) {
    const t = document.body.innerText || '';
    if (/How can I assist|How may I assist|Welcome to the Apple Store/i.test(t)) { greeted = true; break; }
    await sleep(500);
  }
  if (!greeted) return { error: 'no greeting', greeted: false };

  const input = document.querySelector('input[placeholder*="Ask"], textarea[placeholder*="Ask"]');
  if (!input) return { error: 'no input', greeted: true };

  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    || Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')?.set;
  if (setter) setter.call(input, prompt); else input.value = prompt;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  input.dispatchEvent(new Event('change', { bubbles: true }));

  for (let i = 0; i < 30; i++) {
    const send = [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Send');
    if (send && !send.disabled) { send.click(); break; }
    await sleep(300);
  }

  let userSent = false;
  for (let i = 0; i < 40; i++) {
    if ((document.body.innerText || '').includes(prompt)) { userSent = true; break; }
    await sleep(300);
  }

  await sleep(8000);

  const text = document.body.innerText || '';
  const productImgs = [...document.querySelectorAll('img')].filter((i) => {
    const src = i.src || '';
    return src.includes('jiostore') || src.includes('amazon.com/images') || src.includes('poorvika') || src.includes('imimg.com') || src.includes('spareeye-prototype-image');
  });
  const hasLGACHeading = /\\bLG AC\\b/.test(text);
  const hasProductName = /LG 1\\.5 Ton|Dual Inverter Split AC|RS-Q19YNZE/i.test(text);
  const clarifying = /what type|specific brand|particular brand|specific features|capacity do you recommend|looking for a particular/i.test(text) && productImgs.length === 0 && !hasLGACHeading;
  const hasImages = productImgs.length > 0 || (hasLGACHeading && hasProductName);

  return {
    greeted: true,
    userSent,
    hasImages,
    clarifying,
    productImgCount: productImgs.length,
    hasLGACHeading,
    hasProductName,
    bugLikely: userSent && !hasImages,
    assistantTail: text.split('\\n').map((l) => l.trim()).filter(Boolean).slice(-8),
  };
}`;
