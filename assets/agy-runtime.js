(function (dictionary) {
  'use strict';

  const protectedRegion = [
    'script', 'style', 'noscript', 'template',
    'code', 'pre', 'kbd', 'samp', 'var', 'textarea',
    '[contenteditable="true"]', '[contenteditable=""]', '[role="log"]',
    '.monaco-editor', '.monaco-diff-editor', '.xterm', '.prismjs', '.prose', '[class*="language-"]',
    '[data-markdown-block-index]',
    '[data-testid="user-message-content"]',
    '[data-testid*="automation-instructions-markdown"]',
    '[data-testid*="terminal"]',
    '[data-testid*="diff-content"]',
    '[data-testid*="code-block"]',
    '[data-filepath]'
  ].join(',');

  const iconSelector = 'svg,math,canvas,i,[data-icon],[class*="material-symbol"],[class*="google-symbol"],[class*="material-icon"],[class*="codicon"]';
  const textAttributes = ['aria-label', 'title', 'placeholder', 'data-tooltip-content'];
  const watchedAttributes = [...textAttributes, 'aria-hidden', 'hidden', 'data-open', 'data-state'];

  const hasDict = dictionary && Object.keys(dictionary).length > 0;
  if (!hasDict) return;

  const normSpace = s => s.replace(/\r\n/g, '\n').replace(/[ \t]+/g, ' ').trim();
  const normDict = Object.create(null);
  for (const [k, v] of Object.entries(dictionary)) {
    normDict[normSpace(k)] = v;
  }

  function formatDurationZh(raw) {
    return raw
      .replace(/(\d+)\s*days?/gi, '$1 天')
      .replace(/(\d+)\s*hours?/gi, '$1 小时')
      .replace(/(\d+)\s*minutes?/gi, '$1 分钟')
      .replace(/(\d+)\s*mins?/gi, '$1 分钟')
      .replace(/(\d+)\s*seconds?/gi, '$1 秒')
      .replace(/\s*,\s*/g, ' ')
      .replace(/\s+and\s+/gi, ' ')
      .replace(/\s+/g, ' ')
      .trim();
  }

  const wordNumMap = {
    one: '1', two: '2', three: '3', four: '4', five: '5',
    six: '6', seven: '7', eight: '8', nine: '9', ten: '10', twelve: '12', twenty: '20'
  };

  const fnPatterns = [
    [
      /^You have used some of your (weekly|daily) limit\.\s*It will (?:fully )?refresh in (.+?)\.?$/i,
      (_, period, dur) => `已使用部分${period.toLowerCase() === 'daily' ? '今日' : '本周'}额度，将在 ${formatDurationZh(dur)} 后完全重置。`
    ],
    [
      /^You have reached your (weekly|daily) limit\.\s*It will (?:fully )?refresh in (.+?)\.?$/i,
      (_, period, dur) => `已达到${period.toLowerCase() === 'daily' ? '今日' : '本周'}额度上限，将在 ${formatDurationZh(dur)} 后重置。`
    ],
    [
      /^(?:Resets|Refreshes) in (.+?)\.?$/i,
      (_, dur) => `将在 ${formatDurationZh(dur)} 后重置`
    ],
    [
      /^([A-Za-z0-9]+)\s+Hours?\s+Limit Remaining$/i,
      (_, n) => `${wordNumMap[n.toLowerCase()] || n} 小时剩余额度`
    ],
  ];

  const patterns = [
    [/^Hey\s+(.+?),\s*what should we build today\?$/i, '嗨，$1，今天想构建点什么？'],
    [/^Welcome back,\s*(.+?)\.\s*Where should we pick up\?$/i, '欢迎回来，$1，我们从哪里继续？'],
    [/^Good to see you,\s*(.+?)\.\s*What['\u2019]s next\?$/i, '很高兴见到你，$1，接下来做什么？'],
    [/^Hey\s+([A-Za-z0-9_\u4e00-\u9fff -]{1,32})!$/, '嗨，$1！'],
    [/^Show\s+(\d+)\s+breakdowns?$/i, '展开 $1 项明细'],
    [/^Settings for (?:conversations|chats) outside of projects\.$/i, '未归属项目会话的默认设置。'],
    [/^Manage\s+(.+?)\s+app settings\.$/i, '管理 $1 桌面应用设置。'],
    [/^Available AI Credits:\s*(.+)$/i, '可用 AI 积分：$1'],
    [/^([0-9,]+)\s*\/\s*([0-9,]+)\s+tokens?\s*\(([^)]+)\)$/i, '$1 / $2 Token ($3)'],
    [/^\(([0-9,]+)\s+tokens?\)\s*(.+)$/i, '($1 Token) $2'],
    [/^Thought for (\d+(?:\.\d+)?s)$/i, '思考用时 $1'],
    [/^Thinking(?:\.{3}|…)?$/i, '正在思考…'],
    [/^Just now$/i, '刚刚'],
    [/^(\d+)\s*s ago$/i, '$1 秒前'],
    [/^(\d+)\s*m(?:in(?:ute)?s?)? ago$/i, '$1 分钟前'],
    [/^(\d+)\s*h(?:(?:ou)?rs?)? ago$/i, '$1 小时前'],
    [/^(\d+)\s*d(?:ays?)? ago$/i, '$1 天前'],
    [/^(\d+)\s*w(?:eeks?)? ago$/i, '$1 周前'],
    [/^(\d+)\s+tools?$/i, '$1 个工具'],
    [/^(\d+)\s+tools?\s+enabled$/i, '已启用 $1 个工具'],
    [/^(\d+)\s+tools?\s+excluded$/i, '已排除 $1 个工具'],
    [/^(\d+)\s+tasks?\s+running$/i, '$1 个任务运行中'],
    [/^(\d+)\s+models?\s+selected$/i, '已选 $1 个模型'],
    [/^(\d+)\s+lines?\s+added,\s*(\d+)\s+lines?\s+removed$/i, '新增 $1 行，删除 $2 行'],
    [/^(\d+)\s+active conversations?$/i, '$1 个进行中会话'],
    [/^(\d+)\s+archived conversations?$/i, '$1 个已归档会话'],
    [/^Marked\s+(\d+)\s+conversations?\s+as\s+read$/i, '已将 $1 个会话标为已读'],
    [/^(\d+)\/(\d+)\s+steps?$/i, '$1 / $2 步'],
    [/^(\d+)\s+browser steps?$/i, '$1 个浏览器步骤'],
    [/^(\d+)\s+subagents?\s+blocked$/i, '$1 个子代理受阻'],
    [/^(\d+)\s+comments?$/i, '$1 条评论'],
    [/^(\d+)\s+unresolved$/i, '$1 项未解决'],
    [/^(\d+)\s+settings?$/i, '$1 项设置'],
    [/^(\d+)\s+skipped$/i, '已跳过 $1 项'],
    [/^(\d+)\s+failed$/i, '失败 $1 项'],
    [/^(\d+)\s+files?\s+changed$/i, '$1 个文件已更改'],
    [/^Edited\s+(.+)$/, '已编辑 $1'],
    [/^Editing\s+(.+)$/, '正在编辑 $1'],
    [/^Created\s+(.+)$/, '已创建 $1'],
    [/^Creating\s+(.+)$/, '正在创建 $1'],
    [/^Deleted\s+(.+)$/, '已删除 $1'],
    [/^Explored\s+(.+)$/, '已浏览 $1'],
    [/^Listed directory\s+(.+)$/, '已列出目录 $1'],
    [/^Listing\s+(.+)$/, '正在列出 $1'],
    [/^Forked from\s+(.+)$/, '分支自 $1'],
    [/^Starting\s+(.+)$/, '正在启动 $1'],
    [/^Started\s+(.+)$/, '已启动 $1'],
    [/^Loading\s+(.+?)\.{3}$/, '正在加载 $1…'],
    [/^Configure\s+(.+)$/, '配置 $1'],
    [/^More actions for\s+(.+)$/, '$1 的更多操作'],
    [/^MCP server\s+(.+?)\s+requires authentication$/i, 'MCP 服务器 $1 需要身份验证'],
    [/^This\s+(?:project|conversation|chat)\s+overrides your global permission settings\.$/i, '当前范围已覆盖全局权限设置。'],
    [/^Your Plan:\s*(.+)$/i, '当前套餐：$1'],
    [/^(.+?)\s*\u00b7\s*This computer$/i, '$1 · 当前电脑'],
    [/^(.+?),\s*read-only$/i, '$1（只读）'],
    [/^Error while running\s+(.+)$/i, '运行 $1 时出错'],
  ];

  function translateValue(raw) {
    if (!raw) return raw;
    const trimmed = raw.trim();
    if (!trimmed || trimmed.length > 520) return raw;
    let mapped = Object.prototype.hasOwnProperty.call(dictionary, trimmed)
      ? dictionary[trimmed]
      : normDict[normSpace(trimmed)];
    if (!mapped) {
      for (const [re, fn] of fnPatterns) {
        const m = trimmed.match(re);
        if (m) {
          mapped = fn(...m);
          break;
        }
      }
    }
    if (!mapped) {
      for (const [re, tpl] of patterns) {
        if (re.test(trimmed)) {
          mapped = trimmed.replace(re, tpl);
          break;
        }
      }
    }
    if (!mapped || mapped === trimmed) return raw;
    const lead = raw.slice(0, raw.indexOf(trimmed));
    const tail = raw.slice(lead.length + trimmed.length);
    return lead + mapped + tail;
  }

  const isProtected = el => el.closest(protectedRegion) !== null;

  function update(node) {
    if (!node) return;
    if (node.nodeType === Node.TEXT_NODE) {
      const parent = node.parentElement;
      if (parent && !isProtected(parent) && !parent.closest(iconSelector)) {
        const next = translateValue(node.nodeValue);
        if (next !== node.nodeValue) node.nodeValue = next;
      }
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE || isProtected(node) || node.matches(iconSelector)) return;
    for (const name of textAttributes) {
      const value = node.getAttribute(name);
      if (value) {
        const next = translateValue(value);
        if (next !== value) node.setAttribute(name, next);
      }
    }
    for (const child of node.childNodes) update(child);
  }

  function boot() {
    const root = document.body || document.documentElement;
    if (!root) return;
    update(root);
    if (globalThis.__localizerObserver) return;

    const queue = new Set();
    let scheduled = false;
    const observer = new MutationObserver(records => {
      for (const record of records) {
        if (record.type === 'childList') {
          for (const added of record.addedNodes) queue.add(added);
        } else {
          queue.add(record.target);
        }
      }
      if (!scheduled && queue.size) {
        scheduled = true;
        requestAnimationFrame(() => {
          scheduled = false;
          const batch = [...queue];
          queue.clear();
          for (const item of batch) {
            if (item.isConnected) update(item);
          }
        });
      }
    });
    observer.observe(document.documentElement, {
      subtree: true,
      childList: true,
      characterData: true,
      attributes: true,
      attributeFilter: watchedAttributes,
    });
    globalThis.__localizerObserver = observer;
  }

  if (document.body) {
    boot();
  } else {
    window.addEventListener('DOMContentLoaded', boot, { once: true });
  }
})
