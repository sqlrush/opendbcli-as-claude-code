/*-------------------------------------------------------------------------
 *
 * web_html.go
 *	  web_html.go contains the self-contained HTML for the Cerebrate
 *	  dashboard. Extracted from web.go to keep file sizes manageable
 *	  (<800 lines).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/web_html.go
 *
 *-------------------------------------------------------------------------
 */
// web_html.go contains the self-contained HTML for the Cerebrate dashboard.
// Extracted from web.go to keep file sizes manageable (<800 lines).
package cerebrate

// dashboardHTML is a self-contained single-page dashboard with timeline panel.
const dashboardHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OpenDB Autopilot — Cerebrate Dashboard</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
         background: #1a1a2e; color: #e0e0e0; }
  .header { background: #16213e; padding: 20px; text-align: center; border-bottom: 2px solid #0f3460; }
  .header h1 { color: #e94560; font-size: 24px; }
  .header .subtitle { color: #888; font-size: 14px; margin-top: 4px; }
  .stats { display: flex; justify-content: center; gap: 40px; padding: 20px; background: #16213e; }
  .stat { text-align: center; }
  .stat .value { font-size: 36px; font-weight: bold; color: #e94560; }
  .stat .label { font-size: 12px; color: #888; margin-top: 4px; }
  .regions { display: flex; flex-wrap: wrap; gap: 20px; padding: 20px; justify-content: center; }
  .region { background: #16213e; border: 1px solid #0f3460; border-radius: 8px;
            padding: 16px; min-width: 300px; flex: 1; max-width: 400px; cursor: pointer;
            transition: border-color 0.2s; }
  .region:hover { border-color: #e94560; }
  .region h3 { color: #e94560; margin-bottom: 8px; }
  .region .info { color: #aaa; font-size: 13px; line-height: 1.8; }
  .worker-list { margin-top: 10px; max-height: 200px; overflow-y: auto; }
  .worker { display: flex; justify-content: space-between; padding: 4px 8px;
            font-size: 12px; border-bottom: 1px solid #1a1a2e; }
  .worker .name { color: #ccc; }
  .badge { padding: 2px 8px; border-radius: 4px; font-size: 11px; }
  .badge-ok { background: #1b4332; color: #52b788; }
  .badge-warn { background: #583a1b; color: #f4a261; }
  .badge-err { background: #4a1525; color: #e94560; }
  .timeline-panel { max-width: 900px; margin: 0 auto; padding: 20px; }
  .timeline-panel h2 { color: #e94560; font-size: 18px; margin-bottom: 12px; }
  .timeline-event { display: flex; gap: 12px; padding: 8px 12px; border-left: 3px solid #333;
                    margin-bottom: 4px; font-size: 13px; }
  .timeline-event.critical { border-left-color: #e94560; }
  .timeline-event.warning { border-left-color: #f4a261; }
  .timeline-event.info { border-left-color: #52b788; }
  .timeline-event .time { color: #888; min-width: 80px; }
  .timeline-event .type { min-width: 100px; font-weight: 600; }
  .timeline-event .desc { color: #ccc; flex: 1; }
  .timeline-event a { color: #e94560; text-decoration: none; }
  .footer { text-align: center; padding: 10px; color: #555; font-size: 12px; }
  #last-update { color: #666; }
</style>
</head>
<body>
<div class="header">
  <h1>OpenDB Autopilot</h1>
  <div class="subtitle">Cerebrate Dashboard — L4 Autonomous Database Operations</div>
</div>
<div class="stats">
  <div class="stat"><div class="value" id="overlord-count">-</div><div class="label">Overlords</div></div>
  <div class="stat"><div class="value" id="worker-count">-</div><div class="label">Workers</div></div>
  <div class="stat"><div class="value" id="online-count">-</div><div class="label">Online</div></div>
</div>
<div class="regions" id="regions"></div>
<div class="timeline-panel">
  <h2>最近事件</h2>
  <div id="timeline"></div>
</div>
<div class="footer">Auto-refresh every 10s — <span id="last-update"></span></div>

<script>
async function refresh() {
  try {
    const resp = await fetch('/api/topology');
    const regions = await resp.json();

    // Update stats.
    let totalWorkers = 0, totalOnline = 0;
    regions.forEach(r => { totalWorkers += r.worker_count; totalOnline += r.online; });
    document.getElementById('overlord-count').textContent = regions.length;
    document.getElementById('worker-count').textContent = totalWorkers;
    document.getElementById('online-count').textContent = totalOnline;

    // Render regions (clickable).
    const container = document.getElementById('regions');
    container.innerHTML = '';
    regions.forEach(r => {
      const health = r.health ? r.health.score : 0;
      const groups = Object.entries(r.db_groups || {}).map(([k,v]) => k + '\u00d7' + v).join(', ');
      let workersHTML = '';
      (r.workers || []).forEach(w => {
        const badge = w.state === 'STATE_RUNNING' ? 'badge-ok' :
                      w.state === 'STATE_DEGRADED' ? 'badge-warn' : 'badge-err';
        const label = w.state === 'STATE_RUNNING' ? 'OK' :
                      w.state === 'STATE_DEGRADED' ? 'DEGRADED' : 'OFFLINE';
        workersHTML += '<div class="worker"><span class="name">' + w.id + ' (' + w.db_type + ')</span>' +
                       '<span class="badge ' + badge + '">' + label + ' ' + w.health + '/100</span></div>';
      });

      container.innerHTML += '<div class="region" onclick="window.location=\'/api/report/' + r.overlord_id + '\'">' +
        '<h3>' + r.overlord_id + ' (' + r.region + ')</h3>' +
        '<div class="info">Workers: ' + r.online + '/' + r.worker_count + ' online<br>' +
        'Health: ' + health + '/100<br>DB Types: ' + groups + '</div>' +
        '<div class="worker-list">' + workersHTML + '</div></div>';
    });

    document.getElementById('last-update').textContent = new Date().toLocaleTimeString();
  } catch(e) {
    console.error('Refresh failed:', e);
  }
}

async function refreshTimeline() {
  try {
    const resp = await fetch('/api/timeline?limit=10');
    const events = await resp.json();
    const container = document.getElementById('timeline');
    if (!events || events.length === 0) {
      container.innerHTML = '<div style="color:#666;font-size:13px">No recent events</div>';
      return;
    }
    container.innerHTML = '';
    events.forEach(e => {
      const ts = new Date(e.timestamp).toLocaleTimeString();
      const reportLink = e.report_id ? ' <a href="/api/reports/' + e.report_id + '/html">View Report</a>' : '';
      container.innerHTML += '<div class="timeline-event ' + (e.severity || '') + '">' +
        '<span class="time">' + ts + '</span>' +
        '<span class="type">' + (e.event_type || '') + '</span>' +
        '<span class="desc">' + (e.description || '') + reportLink + '</span></div>';
    });
  } catch(e) {
    console.error('Timeline refresh failed:', e);
  }
}

refresh();
refreshTimeline();
setInterval(refresh, 10000);
setInterval(refreshTimeline, 10000);
</script>
</body>
</html>`
