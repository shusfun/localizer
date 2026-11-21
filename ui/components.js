const PROTECTED_TOKEN_RE = /`[^`]*`|<code(?:\s[^>]*)?>[\s\S]*?<\/code>|\{\{[^{}]+\}\}|\$\{[^{}]+\}|\{[0-9]+\}|https?:\/\/(?:[A-Za-z0-9._~:/?#\[\]@!$&'*+,;=%-]*[A-Za-z0-9/#%=_-])?|%[sdif]|<\/?[A-Za-z][^<>]*>|\]\([^()\s]*\)|(?:[A-Za-z]:[\\/]|~\/|\.\/)(?:[A-Za-z0-9._\\/-]*[A-Za-z0-9_\\/-])?|--[a-zA-Z][a-zA-Z0-9_-]*/g;

export function extractTokens(source) {
  return [...new Set(source.match(PROTECTED_TOKEN_RE) || [])];
}

export function createToastManager(container) {
  return function showToast(text) {
    if (!text) return;
    const item = document.createElement('div');
    item.className = 'toast-item';
    item.setAttribute('role', 'status');
    item.textContent = text;
    container.appendChild(item);
    while (container.children.length > 3) {
      container.firstElementChild.remove();
    }
    setTimeout(() => {
      item.classList.add('toast-leaving');
      setTimeout(() => item.remove(), 215);
    }, 2750);
  };
}

export function createNoticeController(box, textNode, closeBtn) {
  const clear = () => {
    box.hidden = true;
    textNode.textContent = '';
    box.classList.remove('notice-error', 'notice-warn');
  };
  closeBtn.addEventListener('click', clear);
  return {
    show(text, tone = 'error') {
      if (!text) {
        clear();
        return;
      }
      textNode.textContent = text;
      box.classList.toggle('notice-error', tone === 'error');
      box.classList.toggle('notice-warn', tone === 'warn');
      box.setAttribute('role', tone === 'error' ? 'alert' : 'status');
      box.hidden = false;
    },
    clear,
  };
}

export function bindModalDialog(dialog, focusSelector) {
  let opener = null;
  dialog.addEventListener('close', () => {
    const target = opener;
    opener = null;
    if (target && target.isConnected && typeof target.focus === 'function') {
      target.focus();
    }
  });
  dialog.addEventListener('click', event => {
    if (event.target === dialog) dialog.close('cancel');
  });
  return {
    open(trigger = document.activeElement) {
      opener = trigger instanceof HTMLElement ? trigger : null;
      dialog.showModal();
      const first = focusSelector ? dialog.querySelector(focusSelector) : dialog.querySelector('input, select, button');
      if (first instanceof HTMLElement) first.focus();
    },
    close(returnValue = '') {
      dialog.close(returnValue);
    },
  };
}

export function statusTone(status) {
  if (!status) return '';
  if (status.includes('未找到')) return 'badge-danger';
  if (status.includes('待同步') || status.includes('未完成') || status.includes('待切换')) return 'badge-warn';
  if (status.includes('已汉化')) return 'badge-ok';
  return '';
}
