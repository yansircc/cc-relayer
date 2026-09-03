#!/usr/bin/env node
// Browser smoke test — catches JS runtime errors that curl can't detect.
// Usage: SITE=https://example.com API_TOKEN=xxx node web/smoke.mjs

import { chromium } from 'playwright-core';

if (!process.env.SITE) { console.error('SITE is required'); process.exit(1); }
const SITE = process.env.SITE.replace(/\/$/, '');
const TOKEN = process.env.API_TOKEN;
if (!TOKEN) { console.error('API_TOKEN is required'); process.exit(1); }

const domain = new URL(SITE).hostname;

// Pages to visit — detail pages extracted dynamically from dashboard links
const staticPages = ['/'];

async function run() {
  const browser = await chromium.launch();
  const context = await browser.newContext({ ignoreHTTPSErrors: true });

  const results = []; // { path, errors[] }

  async function visit(path) {
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (err) => errors.push(err.message));
    page.on('console', (msg) => {
      if (msg.type() !== 'error') return;
      const text = msg.text();
      // Browser-generated network errors — already covered by curl smoke tests
      if (text.includes('Failed to load resource')) return;
      errors.push(`console.error: ${text}`);
    });

    try {
      await page.goto(`${SITE}${path}`, { waitUntil: 'domcontentloaded', timeout: 15000 });
      // Some pages keep background requests alive; don't let that block smoke navigation.
      await page.waitForLoadState('load', { timeout: 5000 }).catch(() => {});
      // extra settle time for async API calls + reactive rendering
      await page.waitForTimeout(1000);
    } catch (e) {
      errors.push(`navigation: ${e.message}`);
    }

    results.push({ path, errors });
    return page;
  }

  // The public status page must work before an admin cookie is installed.
  const publicStatus = await visit('/status');
  if (new URL(publicStatus.url()).pathname === '/login') {
    results.at(-1).errors.push('public status redirected to login');
  }
  await publicStatus.close();

  await context.addCookies([
    { name: 'cc_session', value: TOKEN, domain, path: '/' },
  ]);

  // 1. Visit dashboard and extract detail page links
  const dashboard = await visit('/dashboard');
  let detailPages = [];
  try {
    const links = await dashboard.$$eval('a[href]', (els) =>
      els.map((a) => a.getAttribute('href')).filter(Boolean)
    );
    const addAccountLink = links.find((h) => h.match(/^\/add-account\/[^/]+$/));
    const accountLink = links.find((h) => h.match(/^\/accounts\/[^/]+$/));
    const userLink = links.find((h) => h.match(/^\/users\/[^/]+$/));
    if (addAccountLink) detailPages.push(addAccountLink);
    if (accountLink) detailPages.push(accountLink);
    if (userLink) detailPages.push(userLink);
  } catch { /* no links found — skip detail pages */ }
  await dashboard.close();

  // 2. Visit remaining static pages + extracted detail pages
  const remaining = [...staticPages.slice(1), ...detailPages];
  for (const path of remaining) {
    const p = await visit(path);
    await p.close();
  }

  await browser.close();

  // 3. Report
  let hasErrors = false;
  for (const { path, errors } of results) {
    if (errors.length === 0) {
      console.log(`    \u2713 ${path} (0 errors)`);
    } else {
      hasErrors = true;
      console.log(`    \u2717 ${path} (${errors.length} error${errors.length > 1 ? 's' : ''})`);
      for (const e of errors) console.log(`      ${e}`);
    }
  }

  process.exit(hasErrors ? 1 : 0);
}

run().catch((e) => { console.error(e); process.exit(1); });
