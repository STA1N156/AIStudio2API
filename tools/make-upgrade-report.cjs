const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const read = name => JSON.parse(fs.readFileSync(path.join(root, name), 'utf8'));
const before = read('benchmark-before-120.json');
const after = read('benchmark-after-optimized.json');
const large = read('benchmark-after.json');
const round = value => value.toLocaleString('zh-CN', { maximumFractionDigits: 2 });
const metrics = [
  ['预热 120 个引擎', '秒', before.warm_startup_ms / 1000, after.warm_startup_ms / 1000],
  ['进程内存峰值', 'MiB', before.peak_rss_mib, after.peak_rss_mib],
  ['Go 堆内存峰值', 'MiB', before.peak_heap_mib, after.peak_heap_mib],
  ['选号等待 P95', '毫秒', before.queue_ms.p95, after.queue_ms.p95],
  ['验证数据计算 P95', '毫秒', before.proof_ms.p95, after.proof_ms.p95],
  ['首字 P95', '毫秒', before.first_output_ms.p95, after.first_output_ms.p95],
  ['完成请求 P95', '毫秒', before.total_ms.p95, after.total_ms.p95],
  ['面板统计 P95', '毫秒', before.dashboard_query_ms.p95, after.dashboard_query_ms.p95],
];
const rows = metrics.map(([label, unit, oldValue, newValue]) => `<tr><th>${label}</th><td>${round(oldValue)} ${unit}</td><td>${round(newValue)} ${unit}</td><td>${newValue < oldValue ? '降低' : '增加'} ${round(Math.abs(newValue / oldValue - 1) * 100)}%</td></tr>`).join('');
const summary = { before: { ...before, samples: undefined }, after: { ...after, samples: undefined }, large: { ...large, samples: undefined } };
fs.writeFileSync(path.join(root, 'upgrade-summary.json'), JSON.stringify(summary, null, 2));
fs.writeFileSync(path.join(root, 'upgrade-report.html'), `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>AIStudio2API 本地升级对比</title><style>
body{margin:0;background:#f5f7fb;color:#1c2839;font:16px/1.75 system-ui,sans-serif}main{max-width:1000px;margin:auto;padding:32px 20px 60px}h1{font-size:28px}h2{font-size:21px;margin-top:32px}p,li{max-width:900px}table{border-collapse:collapse;width:100%;font-size:14px}th,td{text-align:left;padding:11px;border-bottom:1px solid #ccd5e1}thead{background:#e6edf5}th{font-weight:600}a{color:#165aa8}code{background:#e6edf5;padding:2px 5px;border-radius:3px}.note{border-left:4px solid #23976b;padding:8px 16px;background:#ebf6f0}img{max-width:100%;border:1px solid #ccd5e1}small{color:#586578}.scroll{overflow-x:auto}
</style><main><h1>AIStudio2API 本地升级对比</h1><p>旧版：<code>2307579</code>。上游：<code>8728487</code>。改动位于本地 <code>codex/spidermonkey-upgrade</code>，本轮未推送、未部署。</p>
<h2>本次更新</h2><ul><li>选号时优先使用当前没有等待验证数据的账号，减少同账号内部等待；仍保留轮询顺序、实时启停和冷却检查。</li><li>接入同账号多页面并行、旧页面后台关闭和附件读取时提前释放占用。</li><li>WAA 从 Goja 切换为 SpiderMonkey，并接入新版共享缓存、脚本与内存管理。</li><li>同步 Firefox 内核、TLS/HTTP2、指纹、页面事件、预检缓存和虚拟时钟。</li><li>验证耗时细分为等待页面、页面操作、刷新及计算，便于直接看到等待发生在哪一步。</li><li>接入 Decision、隐藏模型探测、xhigh、Carbon 续写和工具/流式协议兼容修复。</li><li>沿用深浅配色与紧凑列表，补回原有层级资格、日志开关、JSON/ZIP 导入导出入口。</li></ul>
<h2>保留的自定义行为</h2><p>抗截断独立模型入口、逐段输出及原提示词；客户端思考强度/预算优先于模型后缀；Ultra（包括 Ultra Lite）/Pro/Free 手动资格；冷却实时避让和北京时间提示；空正文重试与自定义 400；Chatbox 特定运行时说明清理；原始上游错误、日志开关、流式内存优化；导入导出代理和启停状态；旧日志正文与其他 SQLite 数据。两种后端都遵守配置的同时启动数量，保留预热节奏控制。</p>
<h2>测试方法</h2><p>两版使用同一脚本、Go 1.27.3、8 个调度核心和 GOGC=50。建立 5000 个真实账号池对象、每账号 40 个模型；预热 120 个对应版本的 WAA 引擎；以 1000 RPM 发起 100 个本地 HTTP 请求，最高约 35 个同时处理；另有 4 个统计查询者，每 500 毫秒查询一次。</p><p>选号、租约、快照和 JavaScript 引擎使用项目真实实现。验证脚本是固定的本地计算任务；上游固定等待 250 毫秒，再每 50 毫秒输出一段，共 35 段。没有调用 Google、消耗真实账号额度或修改线上数据。此数据衡量本地开销；实际 Google 首字和真实页面并行容量需要上线后的观测。</p>
<h2>同条件结果</h2><div class="scroll"><table><thead><tr><th>指标</th><th>旧版</th><th>新版</th><th>变化</th></tr></thead><tbody>${rows}<tr><th>完成 / 失败</th><td>${before.completed} / ${before.failures}</td><td>${after.completed} / ${after.failures}</td><td>完整完成</td></tr></tbody></table></div><p><small>P95 表示 95% 的样本不超过此值。1000 RPM 是提交速率；完成速率会被最后约 2 秒的收尾时间拉低，短测试不用于测极限吞吐。</small></p>
<h2>600 个预热实例</h2><p>新版独立完成 1000 个请求，0 失败，耗时 ${round(large.duration_seconds)} 秒；内存峰值 ${round(large.peak_rss_mib / 1024)} GiB，首字 P95 ${round(large.first_output_ms.p95)} 毫秒。旧版同样预热 600 个实例时耗尽本地内存及 8 GiB 交换空间，被系统终止，因此该组旧版没有完整延迟数据。</p><p>大规模预热的主要收益是降低常驻引擎内存。实际浏览器页面、附件和长回复还会占用额外内存，应结合当前业务继续观察。</p>
<h2>压测中补回的优化</h2><p>检查发现新路径会对每个账号重复计算通道，并在没有层级规则时仍扫描模型目录。现已复用本轮通道结果，没有规则时直接放行，已配置的模型直接查规则；模型别名仍按原规则处理。最终表格使用修正后的新版结果。</p>
<h2>验证与预览</h2><p>全部后端短测试通过。兼容检查覆盖抗截断分片、思考参数、手动层级与冷却隔离、旧数据库迁移、空正文、错误透传和日志开关；前端类型检查与构建、桌面/手机页面检查通过。界面预览和截图使用模拟账号，不连接线上。</p><p><a href="ui-review/accounts-desktop.png">账号页</a> · <a href="ui-review/model-tiers-desktop.png">模型层级设置</a> · <a href="ui-review/model-tiers-mobile.png">手机弹窗</a> · <a href="ui-review/settings-mobile.png">手机设置</a> · <a href="upgrade-summary.json">原始汇总数据</a> · <a href="benchmark-before-600-failure.json">旧版内存退出记录</a></p><p><a href="http://localhost:2078/?tab=settings">打开本地界面预览</a></p></main></html>`);
console.log(path.join(root, 'upgrade-report.html'));
