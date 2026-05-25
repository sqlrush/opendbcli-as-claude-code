const { chromium } = require('playwright');

(async () => {
  let pass = 0, fail = 0;
  const report = (ok, msg) => {
    if (ok) { console.log(`  [PASS] ${msg}`); pass++; }
    else { console.log(`  [FAIL] ${msg}`); fail++; }
  };

  console.log('>>> Playwright Web UI E2E（完整集群数据验证）');
  console.log('---------------------------------------------');

  const browser = await chromium.launch({
    headless: true,
    executablePath: process.env.CHROMIUM_PATH || undefined,
  });
  const page = await browser.newPage();

  try {
    // Load dashboard
    await page.goto('http://localhost:18080/', { timeout: 10000 });
    // Wait for auto-refresh to populate data
    await page.waitForTimeout(3000);

    // === 1. Page structure ===
    const title = await page.title();
    report(title.includes('OpenDB') || title.includes('Cerebrate'), 'Page title contains OpenDB');

    const header = await page.textContent('.header h1');
    report(header && header.includes('OpenDB'), 'Header: OpenDB Autopilot');

    const subtitle = await page.textContent('.header .subtitle');
    report(subtitle && subtitle.includes('L4'), 'Subtitle: L4 Autonomous');

    // === 2. Stats — REAL DATA, not placeholders ===
    const oc = await page.textContent('#overlord-count');
    report(oc === '1', `Overlord count = "${oc}" (expect "1")`);

    const wc = await page.textContent('#worker-count');
    report(wc === '1', `Worker count = "${wc}" (expect "1")`);

    const onc = await page.textContent('#online-count');
    report(onc === '1', `Online count = "${onc}" (expect "1")`);

    // Verify NOT placeholder
    report(oc !== '--' && oc !== '-' && oc !== '', 'Overlord count is not placeholder');
    report(wc !== '--' && wc !== '-' && wc !== '', 'Worker count is not placeholder');

    // === 3. Region cards — must show real overlord data ===
    const regionsHTML = await page.textContent('#regions');
    report(regionsHTML && regionsHTML.includes('china-east'), 'Region card: china-east');
    report(regionsHTML && regionsHTML.includes('1/1'), 'Region card: 1/1 online');

    // === 4. Recent events panel ===
    const body = await page.textContent('body');
    report(body.includes('最近事件'), 'Recent events panel exists');

    // === 5. Screenshot ===
    await page.screenshot({ path: '/tmp/pw_test/dashboard_with_data.png', fullPage: true });
    const fs = require('fs');
    const sz = fs.statSync('/tmp/pw_test/dashboard_with_data.png').size;
    report(sz > 5000, `Screenshot: ${sz} bytes (with real data)`);

    // === 6. API via browser fetch — verify data matches ===
    const fleet = await page.evaluate(async () => {
      const r = await fetch('/api/fleet');
      return await r.json();
    });
    report(fleet.overlords === 1, `API overlords = ${fleet.overlords} (expect 1)`);
    report(fleet.total_workers === 1, `API total_workers = ${fleet.total_workers} (expect 1)`);
    report(fleet.online_workers === 1, `API online_workers = ${fleet.online_workers} (expect 1)`);
    report(Array.isArray(fleet.regions) && fleet.regions.length === 1, `API regions count = ${fleet.regions?.length} (expect 1)`);
    report(fleet.regions[0]?.region === 'china-east', `API region name = ${fleet.regions[0]?.region}`);

    // === 7. Health API ===
    const health = await page.evaluate(async () => {
      const r = await fetch('/api/health');
      return await r.json();
    });
    report(health.status === 'healthy', 'API health = healthy');
    report(health.role === 'manager', 'API role = manager');

    // === 8. Timeline API ===
    const tlStatus = await page.evaluate(async () => (await fetch('/api/timeline')).status);
    report(tlStatus === 200, 'API timeline = 200');

    // === 9. No JS errors ===
    const errors = [];
    page.on('console', msg => { if (msg.type() === 'error') errors.push(msg.text()); });
    await page.reload();
    await page.waitForTimeout(2000);
    report(errors.length === 0, `Console errors: ${errors.length}`);

  } catch (e) {
    console.log(`  [FAIL] Exception: ${e.message}`);
    fail++;
  }

  await browser.close();
  console.log('');
  console.log(`Playwright: ${pass} PASS, ${fail} FAIL`);
  process.exit(fail > 0 ? 1 : 0);
})();
