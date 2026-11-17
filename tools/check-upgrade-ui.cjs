const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('C:/Program Files/WindowsApps/OpenAI.CodexPrimaryRuntime.v26-1112-611-0_26.1112.611.0_x64__3k8sg7r9htsxt/dependencies/node/node_modules/playwright');

let browser;
(async () => {
  const output = path.resolve(__dirname, '../ui-review');
  fs.mkdirSync(output, { recursive: true });
  browser = await chromium.launch({ headless: true, channel: 'chrome' });
  const page = await browser.newPage({ locale: 'zh-CN', colorScheme: 'dark', viewport: { width: 1440, height: 1000 } });
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const models = ['gemini-3.8-flash', 'gemini-4-carbon', '抗截断/gemini-4-carbon'].map(id => ({
    id, name: id, methods: ['generateContent', 'countTokens'], channels: ['playground'],
    capabilities: { chat_model: true, function_declarations: true, thinking_level: true },
    input_token_limit: 1048576, output_token_limit: 65536, thinking_levels: ['minimal', 'low', 'medium', 'high', 'xhigh'],
  }));
  const accounts = ['Ultra', 'Pro', 'Free'].map((tier, i) => ({
    id: `sample-${i}@example.invalid`, label: `sample-${i}@example.invalid`, enabled: true,
    state: 'ready', proxy: '', egress: '', locale: 'zh-CN', timezone: 'Asia/Shanghai',
    models: models.map(model => model.id), benefit_tier: tier, message: '',
  }));
  await page.route('**/api/models', route => route.fulfill({ json: { models } }));
  await page.route('**/api/accounts', route => route.fulfill({ json: { accounts } }));
  await page.goto('http://127.0.0.1:2078/?tab=accounts');
  await page.getByText('sample-0@example.invalid', { exact: true }).first().waitFor();
  await page.screenshot({ path: path.join(output, 'accounts-desktop.png'), fullPage: true });
  await page.goto('http://127.0.0.1:2078/?tab=models');
  await page.getByRole('button', { name: /gemini-4-carbon/ }).first().click();
  await page.getByText('可用账号层级', { exact: true }).waitFor();
  for (const tier of ['Ultra', 'Pro', 'Free']) {
    if (!(await page.getByRole('checkbox', { name: tier, exact: true }).isChecked())) throw new Error(`${tier} should default to enabled`);
  }
  await Promise.all([
    page.waitForResponse(response => response.url().endsWith('/api/config') && response.request().method() === 'PUT'),
    page.getByRole('checkbox', { name: 'Free', exact: true }).uncheck(),
  ]);
  const saved = await page.evaluate(async () => (await fetch('/api/config')).json());
  if (saved.model_tiers['gemini-4-carbon'].includes('Free')) throw new Error('tier setting was not saved');
  await Promise.all([
    page.waitForResponse(response => response.url().endsWith('/api/config') && response.request().method() === 'PUT'),
    page.getByRole('checkbox', { name: 'Free', exact: true }).check(),
  ]);
  await page.screenshot({ path: path.join(output, 'model-tiers-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: path.join(output, 'model-tiers-mobile.png'), fullPage: true });
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
  if (overflow) throw new Error('mobile page overflows horizontally');
  await page.goto('http://127.0.0.1:2078/?tab=settings&section=security');
  await page.locator('#setting-empty-output').waitFor();
  await page.locator('#setting-log-console').waitFor();
  await page.screenshot({ path: path.join(output, 'settings-mobile.png'), fullPage: true });
  await browser.close();
  if (errors.length) throw new Error(errors.join('\n'));
  fs.writeFileSync(path.join(output, 'result.json'), JSON.stringify({ pageErrors: errors, horizontalOverflow: overflow, accounts: 'synthetic', viewports: ['1440x1000', '390x844'] }, null, 2));
  console.log('UI review passed:', output);
})().catch(error => { console.error(error); process.exitCode = 1; }).finally(() => browser?.close());
