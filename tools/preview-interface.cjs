const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const dist = path.resolve(__dirname, '../internal/webui/dist');
const models = ['gemini-3.8-flash', 'gemini-4-carbon', '抗截断/gemini-4-carbon'].map(id => ({
  id, name: id, methods: ['generateContent', 'countTokens'], channels: ['playground'],
  capabilities: { chat_model: true, function_declarations: true, thinking_level: true },
  input_token_limit: 1048576, output_token_limit: 65536,
  thinking_levels: ['minimal', 'low', 'medium', 'high', 'xhigh'],
}));
const accounts = ['Ultra', 'Pro', 'Free'].map((tier, i) => ({
  id: `sample-${i}@example.invalid`, label: `sample-${i}@example.invalid`, enabled: true,
  state: 'ready', proxy: '', egress: '', locale: 'zh-CN', timezone: 'Asia/Shanghai',
  models: models.map(model => model.id), benefit_tier: tier, message: '',
}));
const config = {
  model_tiers: {}, empty_output_error: true, log_console: false, auto_start: false,
  hide_dock_icon: false, request_body_log: false, log_file: false, log_max_total_mb: 100,
  admin_auth_enabled: false, admin_username: '', admin_password_set: false,
  build_native_nonstream: false, nonstream_keepalive: false, fake_stream: false,
  youtube_links: false, search_model_aliases: false, discover_hidden_models: true,
  google_search: false, continuation_rewrite: false, continuation_prompt: '',
  strip_thought_signatures: false, first_event_hedge: '0s', first_event_hedge_models: '',
  auth_states: 'auth', listen_addr: '127.0.0.1:2078', preview_listen_addr: '127.0.0.1:2079',
  proxy_api_key: '', active_listen_addr: '127.0.0.1:2078', reserved_listen_addr: '',
  active_preview_listen_addr: '127.0.0.1:2079', active_proxy_api_key: '',
  management_restart_required: false, service_restart_required: false, proxy: '',
  active_proxy: '', proxy_pool: [], init_timeout: '2m', request_timeout: '18m',
  warm_worker_limit: 600, max_active_workers: 800, warm_startup_concurrency: 50,
  per_account_concurrency: 5, max_account_switches: -1, routing_strategy: 'round-robin',
  account_order: '', upstream_channels: ['playground'], model_mapping: '', model_channels: '',
  model_accounts: '', api_key_accounts: '', waa_backend: 'spidermonkey', media_output: 'inline',
  media_base_url: '', environment: [], invalid: [],
};
const json = (res, value, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value)); };
const latency = { avg_ms: 267, p50_ms: 261, p95_ms: 286, p99_ms: 301 };
const stats = { requests: 240, succeeded: 238, failed: 2, canceled: 0, rate_limited: 0, input_tokens: 48000, cached_tokens: 12000, reasoning_tokens: 4000, reply_tokens: 20000, total_tokens: 72000, duration: { ...latency, avg_ms: 2300 }, first_event: latency, queue_avg_ms: 4.5 };
http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  if (url.pathname === '/api/events') {
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
    res.write(': local preview\n\n');
    const timer = setInterval(() => res.write(': keepalive\n\n'), 15000);
    req.on('close', () => clearInterval(timer));
    return;
  }
  if (url.pathname === '/api/config' && req.method === 'PUT') {
    let body = ''; for await (const chunk of req) body += chunk;
    Object.assign(config, JSON.parse(body)); return json(res, config);
  }
  const data = {
    '/api/auth/session': { enabled: false, authenticated: true, username: '本地界面预览' },
    '/api/status': { state: 'STOPPED', running: false, ready: true, version: 'local-preview', active_requests: 0, accounts: { total: 3, ready: 3, busy: 0, cooldown: 0, auth_required: 0 }, camoufox: {}, geoip: {}, chrome_import: false },
    '/api/config': config, '/api/accounts': { accounts }, '/api/models': { models },
    '/api/accounts/exits': { exits: [{ proxy: '', ip: '127.0.0.1', country: '', timezone: 'Asia/Shanghai', locale: 'zh-CN' }] },
    '/api/cooldowns': { cooldowns: [] }, '/api/requests': { requests: [] },
    '/api/accounts/tasks': { tasks: [] }, '/api/first-event-latency': { latencies: [] },
    '/api/update': { current: 'local-preview', latest: 'local-preview', mode: 'development', available: false, updating: false },
    '/api/prompts': { prompts: [], total: 0 },
    '/v1/models': { object: 'list', data: models.map(model => ({ id: model.id, object: 'model', owned_by: 'local-preview' })) },
    '/api/usage/records': { items: [] },
    '/api/usage': {
      from: new Date(Date.now() - 86400000).toISOString(), to: new Date().toISOString(),
      generated_at: new Date().toISOString(), bucket_seconds: 3600,
      totals: stats, previous: stats, cost: 0, previous_cost: 0, unpriced_models: [],
      recent: { minutes: 5, requests: 12, tokens: 3600 },
      buckets: Array.from({ length: 24 }, (_, i) => ({ ...stats, requests: 6 + i % 7, total_tokens: 1500 + i * 60, cost: 0, at: new Date(Date.now() - (23 - i) * 3600000).toISOString() })),
      stack_by: 'model', series: [{ key: 'gemini-3.8-flash', requests: Array.from({ length: 24 }, (_, i) => 6 + i % 7), tokens: Array.from({ length: 24 }, (_, i) => 1500 + i * 60) }],
      groups: { model: [{ key: 'gemini-3.8-flash', ...stats }], account: [], channel: [], protocol: [], state: [], status: [] },
      pairs: [], options: { model: ['gemini-3.8-flash'], account: [], channel: ['playground'], protocol: [], state: [] },
    },
  };
  if (url.pathname in data) return json(res, data[url.pathname]);
  if (url.pathname.startsWith('/api/')) return json(res, { error: { message: '界面预览使用模拟数据' } }, 400);
  let file = path.resolve(dist, '.' + decodeURIComponent(url.pathname));
  if (!file.startsWith(dist + path.sep)) file = path.join(dist, 'index.html');
  const ext = path.extname(file);
  let gzip = false;
  if (!fs.existsSync(file) && fs.existsSync(file + '.gz')) { file += '.gz'; gzip = true; }
  if (!fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(dist, 'index.html');
  const type = { '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.png': 'image/png', '.webp': 'image/webp' }[ext] || 'text/html';
  res.writeHead(200, { 'Content-Type': type, ...(gzip ? { 'Content-Encoding': 'gzip' } : {}) });
  fs.createReadStream(file).pipe(res);
}).listen(2078, '127.0.0.1', () => console.log('Local UI preview with synthetic accounts: http://localhost:2078/?tab=accounts'));
