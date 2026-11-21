(function () {
  'use strict';
  if (globalThis.__devinLocalizerRunning || !document.body) return;
  globalThis.__devinLocalizerRunning = true;
  const surfaces = '[role="menu"],[role="menuitem"],[role="dialog"],[role="listbox"],[role="option"],[data-radix-popper-content-wrapper]';
  const blocked = 'code,pre,kbd,samp,textarea,[contenteditable=true],[data-slate-node],[data-transcript-row-key],.prose,[data-streamdown],.xterm,.monaco-editor,[translate=no]';
  const attributes = ['aria-label', 'title', 'data-tooltip-content'];
  const tools = /^Yes, (allow|always allow) calling (all tools|[^\n]+?) on the (.+?) MCP server(?: \(this session\))?$/;
  const staticText = new Map([['Allow', '允许'], ['Reject', '拒绝'], ['Always allow', '始终允许'], ['Allow once', '仅允许一次'], ['Allow for this session', '本次会话允许'], ['Run', '运行'], ['Run once', '仅运行一次'], ['Run always', '始终运行']]);
  function translateValue(raw) {
    const trimmed = raw.trim();
    if (!trimmed) return raw;
    let translation = staticText.get(trimmed);
    if (!translation) {
      const match = trimmed.match(tools);
      if (match) {
        const scope = trimmed.endsWith('(this session)') ? '本次会话' : '';
        const tool = match[2] === 'all tools' || match[2] === 'tools' ? '所有工具' : ' ' + match[2] + ' 工具';
        const verb = match[1] === 'always allow' ? '始终允许' : '允许';
        translation = verb + scope + '调用' + tool + '（' + match[3] + ' MCP 服务器）';
      }
    }
    return translation && translation !== trimmed ? raw.replace(trimmed, () => translation) : raw;
  }
  function visit(node) {
    if (node.nodeType === Node.TEXT_NODE) {
      const parent = node.parentElement;
      if (!parent || parent.closest(blocked) || !parent.closest(surfaces)) return;
      const next = translateValue(node.nodeValue);
      if (next !== node.nodeValue) node.nodeValue = next;
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE || node.matches(blocked) || node.closest(blocked)) return;
    if (node.matches('[role="button"],button,[role="menuitem"],[role="option"],[role="alert"]')) {
      for (const name of attributes) {
        const value = node.getAttribute(name);
        if (!value) continue;
        const next = translateValue(value);
        if (next !== value) node.setAttribute(name, next);
      }
    }
    for (const child of node.childNodes) visit(child);
  }
  visit(document.body);
  const queue = new Set();
  let scheduled = false;
  const observer = new MutationObserver(records => {
    for (const record of records) {
      if (record.type === 'childList') {
        for (const node of record.addedNodes) queue.add(node);
      } else {
        queue.add(record.target);
      }
    }
    if (!scheduled && queue.size) {
      scheduled = true;
      requestAnimationFrame(() => {
        scheduled = false;
        for (const node of queue) {
          if (node.isConnected) visit(node);
        }
        queue.clear();
        observer.takeRecords();
      });
    }
  });
  observer.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: attributes });
})();
