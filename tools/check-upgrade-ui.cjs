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
  const historyRequests = [];
  page.on('request', request => {
    if (/\/api\/prompts(?:\/[^/]+)?$/.test(new URL(request.url()).pathname)) historyRequests.push(request.url());
  });
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
  const checkedPages = [];
  for (const theme of ['dark', 'light']) {
    await page.evaluate(theme => localStorage.setItem('user_theme', theme), theme);
    await page.setViewportSize({ width: 1440, height: 1000 });
    for (const tab of ['accounts', 'access', 'models', 'requests', 'usage', 'settings', 'version', 'logs']) {
      await page.goto(`http://127.0.0.1:2078/?tab=${tab}`);
      await page.locator(tab === 'logs' ? 'main [role="group"]' : 'main h2').first().waitFor();
      await page.evaluate(() => document.fonts.ready);
      if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error(`${theme}/${tab} overflows`);
      await page.screenshot({ path: path.join(output, `${theme}-${tab}.png`), fullPage: true });
      checkedPages.push(`${theme}/${tab}`);
    }
  }
  const sidebarWidth = await page.locator('.app-sidebar').evaluate(element => element.getBoundingClientRect().width);
  if (sidebarWidth !== 280) throw new Error(`unexpected sidebar width: ${sidebarWidth}`);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('http://127.0.0.1:2078/?tab=accounts');
  await page.getByText('sample-0@example.invalid', { exact: true }).first().waitFor();
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('light mobile overflows');
  await page.screenshot({ path: path.join(output, 'light-mobile.png'), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('http://127.0.0.1:2078/prompts/new_chat');
  await page.locator('.makersuite-layout').waitFor();
  await page.screenshot({ path: path.join(output, 'api-playground.png'), fullPage: true });
  const sidebar = await page.locator('.app-sidebar').elementHandle();
  const navGeometry = () => page.locator('.app-sidebar').evaluate(element =>
    [...element.querySelectorAll('nav button')].map(button => {
      const { x, y, width, height } = button.getBoundingClientRect();
      return { text: button.textContent.trim(), x, y, width, height };
    }));
  const stableGeometry = JSON.stringify(await navGeometry());
  const draft = 'local preview draft';
  await page.locator('.ms-prompt-box textarea').fill(draft);
  await page.locator('.console-nav-item').filter({ hasText: '用量' }).click();
  await page.getByRole('heading', { name: '用量', exact: true }).waitFor();
  if (JSON.stringify(await navGeometry()) !== stableGeometry) throw new Error('navigation shifted on page change');
  for (const label of ['结果与错误', '耗时分位', 'API 估算费用', '首个事件 P50', 'Token 构成']) {
    if (await page.getByText(label, { exact: true }).count()) throw new Error(`removed usage section visible: ${label}`);
  }
  await page.setViewportSize({ width: 2550, height: 1323 });
  const padding = await page.locator('main header.page-x').evaluate(element => getComputedStyle(element).paddingLeft);
  if (padding !== '24px') throw new Error(`unexpected wide-screen padding: ${padding}`);
  await page.waitForTimeout(350);
  await page.screenshot({ path: path.join(output, 'light-usage-wide.png'), fullPage: true });
  await page.locator('.console-nav-item').filter({ hasText: '服务配置' }).click();
  await page.getByRole('button', { name: '容量', exact: true }).click();
  await page.locator('fieldset').first().waitFor();
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await page.screenshot({ path: path.join(output, 'settings-capacity-wide.png'), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator('.console-nav-item').filter({ hasText: 'API 试用' }).click();
  await page.locator('.ms-prompt-box textarea').waitFor();
  if (!(await sidebar.evaluate(element => element.isConnected))) throw new Error('sidebar was recreated');
  if (JSON.stringify(await navGeometry()) !== stableGeometry) throw new Error('navigation shifted returning to playground');
  if (await page.locator('.ms-prompt-box textarea').inputValue() !== draft) throw new Error('draft lost during navigation');
  if (await page.getByText('历史记录', { exact: true }).count()) throw new Error('history entry remains');
  await page.keyboard.press('Control+/');
  await page.getByRole('dialog').waitFor();
  if (await page.getByText('近期活动', { exact: true }).count()) throw new Error('history remains in palette');
  await page.keyboard.press('Escape');
  await page.setViewportSize({ width: 390, height: 844 });
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('mobile playground overflows');
  await page.waitForTimeout(350);
  await page.screenshot({ path: path.join(output, 'playground-mobile.png'), fullPage: true });
  if (historyRequests.length) throw new Error(`unexpected history requests: ${historyRequests.join(', ')}`);
  await browser.close();
  if (errors.length) throw new Error(errors.join('\n'));
  fs.writeFileSync(path.join(output, 'result.json'), JSON.stringify({ pageErrors: errors, horizontalOverflow: overflow, accounts: 'synthetic', sidebarWidth, checkedPages, stableSidebar: true, draftPreserved: true, historyRequests, widePadding: padding, viewports: ['1440x1000', '390x844', '2550x1323'] }, null, 2));
  console.log('UI review passed:', output);
})().catch(error => { console.error(error); process.exitCode = 1; }).finally(() => browser?.close());
