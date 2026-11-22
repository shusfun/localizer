import { Call, Events, Window } from '/wails/runtime.js';
import { attachMorphIcon, ICON_SPECS, mountStaticIcon } from '/icons.js';
import {
  bindModalDialog,
  createNoticeController,
  createToastManager,
  extractTokens,
  statusTone,
} from '/components.js';

const element = id => document.getElementById(id);
const invoke = (name, ...args) => Call.ByName('main.Service.' + name, ...args);
const number = new Intl.NumberFormat('zh-CN');
const PAGE_SIZE = 80;

const STATUS_LABEL = {
  translated: '已有译文',
  missing: '待补',
  kept: '保留原文',
};
const THEME_CYCLE = ['system', 'light', 'dark'];
const THEME_LABEL = {
  system: '主题：跟随系统',
  light: '主题：明亮',
  dark: '主题：暗色',
};

const taskButtons = [...document.querySelectorAll('.task')];
const filterButtons = [...document.querySelectorAll('[data-filter]')];
const appButtons = [...document.querySelectorAll('[data-app]')];

const showToast = createToastManager(element('toast-region'));
attachMorphIcon(element('notice-close'), ICON_SPECS.cancel, 'default');
const notice = createNoticeController(element('notice'), element('notice-text'), element('notice-close'));

const translateModal = bindModalDialog(element('translate-dialog'), '#limit');
const cleanModal = bindModalDialog(element('clean-dialog'), '#confirm-clean');

attachMorphIcon(element('minimize'), ICON_SPECS.minimize, 'default');
const maximizeIcon = attachMorphIcon(element('maximize'), ICON_SPECS.maximize, 'normal');
attachMorphIcon(element('close'), ICON_SPECS.close, 'default');
attachMorphIcon(element('detect-refresh'), ICON_SPECS.refresh, 'default');
const overrideChevron = attachMorphIcon(element('override-summary'), ICON_SPECS.chevron, 'collapsed', element('override-chevron'));
element('model-override').addEventListener('toggle', () => {
  overrideChevron.setState(element('model-override').open ? 'expanded' : 'collapsed');
});

mountStaticIcon(element('rail-icon-devin'), 'devin');
mountStaticIcon(element('rail-icon-agy'), 'agy');
mountStaticIcon(element('workspace-icon'), 'devin');
mountStaticIcon(element('search-icon'), 'search');

let themeMode = localStorage.getItem('localizer-theme') || 'system';
if (!THEME_CYCLE.includes(themeMode)) themeMode = 'system';
const themeIcon = attachMorphIcon(element('theme-toggle'), ICON_SPECS.theme, themeMode);

function applyTheme(mode) {
  themeMode = mode;
  if (mode === 'system') {
    document.documentElement.removeAttribute('data-theme');
  } else {
    document.documentElement.setAttribute('data-theme', mode);
  }
  localStorage.setItem('localizer-theme', mode);
  themeIcon.setState(mode);
  const label = THEME_LABEL[mode];
  element('theme-toggle').title = label;
  element('theme-toggle').setAttribute('aria-label', label);
}
applyTheme(themeMode);

element('theme-toggle').addEventListener('click', () => {
  const next = THEME_CYCLE[(THEME_CYCLE.indexOf(themeMode) + 1) % THEME_CYCLE.length];
  applyTheme(next);
});

let current = 'devin';
let filterState = '';
let panel = null;
let offset = 0;
let serial = 0;
let pending = false;
let poll = null;
let debounce = null;
let editingKey = null;

function formatBytes(size) {
  if (size < 1024) return number.format(size) + ' B';
  if (size < 1048576) return (size / 1024).toFixed(1) + ' KB';
  return (size / 1048576).toFixed(1) + ' MB';
}

function errorText(error) {
  return error?.message || String(error);
}

function isCurrentJobRunning() {
  return Boolean(panel?.Job.Running && (!panel.Job.App || panel.Job.App === current));
}

function updateControls() {
  const ready = Boolean(panel);
  const jobHere = isCurrentJobRunning();
  for (const button of taskButtons) {
    button.disabled = pending || !ready || jobHere;
  }
  for (const button of appButtons) {
    button.disabled = pending;
  }
  element('language').disabled = pending || !ready || jobHere;
  element('clean').disabled = pending || !ready || !panel.CacheBytes || panel.Job.Running;

  const translateBtn = element('translate');
  const otherAppRunning = Boolean(panel?.Job.Running && panel.Job.App && panel.Job.App !== current);
  translateBtn.textContent = jobHere ? '停止翻译' : '翻译缺口';
  translateBtn.classList.toggle('btn-danger-outline', jobHere);
  translateBtn.disabled = pending || !ready || otherAppRunning || (!jobHere && !panel.Total.Missing);

  const applyBtn = element('apply');
  applyBtn.disabled = pending || !ready || jobHere || !panel.Installed;
  applyBtn.textContent = current === 'devin'
    ? (panel?.NeedsSync ? '同步词典更新' : '准备中文启动')
    : panel?.NeedsSync && panel?.Applied
    ? '同步词典更新'
    : panel?.Applied
      ? '重新写入汉化'
      : '应用汉化';

  element('restore').disabled = pending || !ready || jobHere || !panel.Installed || !panel.CanRestore;
  element('open').disabled = pending || !ready || jobHere || !panel.Installed;

  element('main').setAttribute('aria-busy', String(pending));
}

function renderJobStrip(data) {
  const strip = element('job-strip');
  const job = data?.Job;
  if (!job || (!job.Running && !job.Message)) {
    strip.hidden = true;
    return;
  }
  if (!job.Running && job.App && job.App !== current) {
    strip.hidden = true;
    return;
  }
  const appTitle = job.App === 'agy' ? 'Antigravity' : job.App === 'devin' ? 'Devin' : '';
  const prefix = job.App && job.App !== current ? `正在后台处理 ${appTitle} · ` : '';
  if (job.Running) {
    element('job-text').textContent = `${prefix}${job.Message} · ${number.format(job.Done)} / ${number.format(job.Total)}`;
    const parts = [`已处理 ${number.format(job.Success)}`];
    if (job.Kept > 0) parts.push(`保留原文 ${number.format(job.Kept)}`);
    if (job.Failed > 0) parts.push(`待复核 ${number.format(job.Failed)}`);
    if (job.LastError) parts.push(job.LastError);
    element('job-sub').textContent = parts.join(' · ');
    const pct = job.Total > 0 ? Math.min(100, Math.round((job.Done / job.Total) * 100)) : 0;
    element('job-bar-fill').style.width = pct + '%';
    strip.hidden = false;
  } else {
    strip.hidden = true;
  }
}

function renderRail(apps) {
  if (!Array.isArray(apps)) return;
  for (const item of apps) {
    const dot = element('rail-dot-' + item.ID);
    const ver = element('rail-ver-' + item.ID);
    const fill = element('rail-fill-' + item.ID);
    const meta = element('rail-meta-' + item.ID);
    const gap = element('rail-gap-' + item.ID);
    if (ver) ver.textContent = item.Version;
    if (dot) {
      dot.className = 'rail-dot';
      const tone = statusTone(item.Status);
      if (tone === 'badge-ok') dot.classList.add('dot-ok');
      else if (tone === 'badge-warn') dot.classList.add('dot-warn');
      else if (tone === 'badge-danger') dot.classList.add('dot-danger');
    }
    const total = item.Translated + item.Missing + item.Kept;
    if (fill) {
      const covered = item.Translated + item.Kept;
      const pct = total > 0 ? Math.min(100, Math.max(3, Math.round((covered / total) * 100))) : 0;
      fill.style.width = pct + '%';
    }
    if (meta) {
      meta.textContent = item.Status;
    }
    if (gap) {
      gap.textContent = item.Missing > 0 ? `待补 ${number.format(item.Missing)}` : '已齐备';
      gap.classList.toggle('has-gap', item.Missing > 0);
    }
  }
}

function openRowEditor(tr, row, editBtn, editMorph) {
  if (editingKey && editingKey !== row.Key) {
    runFind(false);
  }
  editingKey = row.Key;
  tr.classList.add('is-editing');
  editMorph.setState('save');
  editBtn.title = '保存译文';
  editBtn.setAttribute('aria-label', '保存译文');

  const targetCell = tr.children[1];
  const editor = document.createElement('div');
  editor.className = 'inline-editor';

  const multiline = row.Source.includes('\n') || row.Source.length > 75;
  const textarea = document.createElement('textarea');
  textarea.className = multiline ? 'textarea-ctrl is-multiline' : 'textarea-ctrl';
  textarea.rows = multiline ? Math.min(5, Math.max(2, row.Source.split('\n').length)) : 1;
  textarea.value = row.Text || '';
  textarea.placeholder = '输入简体中文译文…';
  textarea.setAttribute('aria-label', '编辑译文');

  const errorLine = document.createElement('div');
  errorLine.className = 'field-error';
  errorLine.setAttribute('role', 'alert');
  errorLine.hidden = true;
  textarea.addEventListener('input', () => {
    if (!errorLine.hidden) {
      errorLine.hidden = true;
      textarea.classList.remove('has-error');
    }
  });

  const tokens = extractTokens(row.Source);
  let tokenBar = null;
  if (tokens.length > 0) {
    tokenBar = document.createElement('div');
    tokenBar.className = 'token-bar';
    for (const tok of tokens) {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'token-chip';
      chip.textContent = tok;
      chip.title = '点击插入受保护片段 ' + tok;
      chip.addEventListener('mousedown', event => event.preventDefault());
      chip.addEventListener('click', () => {
        const start = textarea.selectionStart ?? textarea.value.length;
        const end = textarea.selectionEnd ?? textarea.value.length;
        textarea.setRangeText(tok, start, end, 'end');
        textarea.focus();
      });
      tokenBar.appendChild(chip);
    }
  }

  editor.append(textarea);
  if (tokenBar) editor.append(tokenBar);
  editor.append(errorLine);
  targetCell.replaceChildren(editor);

  const actionsWrap = tr.querySelector('.row-actions');
  const cancelBtn = document.createElement('button');
  cancelBtn.type = 'button';
  cancelBtn.className = 'icon-btn icon-btn-sm';
  cancelBtn.title = '取消编辑 (Esc)';
  cancelBtn.setAttribute('aria-label', '取消编辑');
  attachMorphIcon(cancelBtn, ICON_SPECS.cancel, 'default');

  const extraButtons = [...actionsWrap.querySelectorAll('.row-extra')];
  for (const b of extraButtons) b.hidden = true;
  actionsWrap.appendChild(cancelBtn);

  let saving = false;
  const cancelEdit = () => {
    editingKey = null;
    runFind(false);
  };

  const commitSave = async () => {
    if (saving) return;
    saving = true;
    errorLine.hidden = true;
    editBtn.disabled = true;
    cancelBtn.disabled = true;
    try {
      await invoke('SaveEntry', current, row.Key, textarea.value, false, false);
      editingKey = null;
      await refresh(true);
      showToast('已保存词条译文');
    } catch (error) {
      errorLine.textContent = errorText(error);
      errorLine.hidden = false;
      textarea.classList.add('has-error');
      textarea.focus();
    } finally {
      saving = false;
      editBtn.disabled = false;
      cancelBtn.disabled = false;
    }
  };

  editBtn.onclick = event => {
    event.stopPropagation();
    commitSave();
  };
  cancelBtn.addEventListener('click', event => {
    event.stopPropagation();
    cancelEdit();
  });

  textarea.addEventListener('keydown', event => {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      cancelEdit();
      return;
    }
    if (event.key === 'Enter' && (!row.Source.includes('\n') && !event.shiftKey || event.ctrlKey || event.metaKey)) {
      event.preventDefault();
      commitSave();
    }
  });

  textarea.focus();
  textarea.setSelectionRange(textarea.value.length, textarea.value.length);
}

function buildRow(row) {
  const tr = document.createElement('tr');

  const sourceCell = document.createElement('td');
  sourceCell.className = 'col-source';
  const sourceText = document.createElement('div');
  sourceText.className = 'source-text';
  sourceText.textContent = row.Source;
  const metaBtn = document.createElement('button');
  metaBtn.type = 'button';
  metaBtn.className = 'entry-meta';
  metaBtn.textContent = row.Key.startsWith(row.Group + '.') ? row.Key : `${row.Group} · ${row.Key}`;
  metaBtn.title = '点击复制词条键';
  metaBtn.addEventListener('click', () => {
    navigator.clipboard?.writeText(row.Key).then(() => showToast('已复制词条键')).catch(() => {});
  });
  sourceCell.append(sourceText, metaBtn);

  const targetCell = document.createElement('td');
  targetCell.className = 'col-target';
  const targetText = document.createElement('div');
  targetText.className = 'target-text';
  if (row.State === 'translated') {
    targetText.textContent = row.Text;
  } else if (row.State === 'kept') {
    targetText.textContent = '保留英文原文';
    targetText.classList.add('target-placeholder');
  } else {
    targetText.textContent = '点击添加译文…';
    targetText.classList.add('target-placeholder');
  }
  targetCell.appendChild(targetText);

  const stateCell = document.createElement('td');
  stateCell.className = `col-state ${row.State}`;
  const stateInner = document.createElement('div');
  stateInner.className = 'state-cell-inner';

  const pill = document.createElement('span');
  pill.className = row.Customized ? 'state-pill is-custom' : 'state-pill';
  pill.textContent = STATUS_LABEL[row.State] + (row.Customized ? ' · 自定' : '');

  const actions = document.createElement('div');
  actions.className = 'row-actions';

  if (row.DefaultState !== 'kept') {
    const editBtn = document.createElement('button');
    editBtn.type = 'button';
    editBtn.className = 'icon-btn icon-btn-sm';
    editBtn.title = '编辑译文';
    editBtn.setAttribute('aria-label', '编辑译文');
    const editMorph = attachMorphIcon(editBtn, ICON_SPECS.editSave, 'edit');

    const startEdit = () => {
      if (pending || isCurrentJobRunning()) return;
      openRowEditor(tr, row, editBtn, editMorph);
    };
    editBtn.onclick = startEdit;
    targetCell.classList.add('target-editable');
    targetCell.addEventListener('click', event => {
      if (editingKey === row.Key || event.target.closest('.inline-editor')) return;
      startEdit();
    });
    actions.appendChild(editBtn);

    const keepBtn = document.createElement('button');
    keepBtn.type = 'button';
    keepBtn.className = 'icon-btn icon-btn-sm row-extra';
    const isKept = row.State === 'kept';
    keepBtn.setAttribute('aria-pressed', String(isKept));
    keepBtn.title = isKept ? '取消保留原文' : '标记为保留原文';
    keepBtn.setAttribute('aria-label', keepBtn.title);
    attachMorphIcon(keepBtn, ICON_SPECS.keep, isKept ? 'on' : 'off');
    keepBtn.addEventListener('click', () => {
      action(
        () => invoke('SaveEntry', current, row.Key, '', !isKept, isKept),
        isKept ? '已取消保留原文' : '已设为保留英文原文',
        true,
        true
      );
    });
    actions.appendChild(keepBtn);

    if (row.Customized && !isKept) {
      const resetBtn = document.createElement('button');
      resetBtn.type = 'button';
      resetBtn.className = 'icon-btn icon-btn-sm row-extra';
      resetBtn.title = '恢复默认词条';
      resetBtn.setAttribute('aria-label', '恢复默认词条');
      attachMorphIcon(resetBtn, ICON_SPECS.reset, 'default');
      resetBtn.addEventListener('click', () => {
        action(() => invoke('SaveEntry', current, row.Key, '', false, true), '已恢复默认词条', true, true);
      });
      actions.appendChild(resetBtn);
    }
  }

  stateInner.append(pill, actions);
  stateCell.appendChild(stateInner);

  tr.append(sourceCell, targetCell, stateCell);
  return tr;
}

async function find(preserveScroll = false) {
  const request = ++serial;
  const wrap = element('table-wrap');
  const scrollTop = preserveScroll ? wrap.scrollTop : 0;
  const query = element('query').value;
  const group = element('group').value;
  const result = await invoke('Find', current, query, filterState, group, offset);
  if (request !== serial) return;
  if (offset && offset >= result.Count) {
    offset = Math.max(0, Math.floor((result.Count - 1) / PAGE_SIZE) * PAGE_SIZE);
    return find(preserveScroll);
  }
  editingKey = null;
  element('results').replaceChildren(...result.Rows.map(buildRow));
  const hasFilters = Boolean(query.trim() || filterState || group);
  element('empty').hidden = result.Count > 0;
  element('reset-filters').hidden = !hasFilters;
  element('prev').disabled = offset === 0;
  element('next').disabled = offset + PAGE_SIZE >= result.Count;
  element('page-label').textContent = result.Count
    ? `${number.format(offset + 1)}–${number.format(Math.min(result.Count, offset + PAGE_SIZE))} / 共 ${number.format(result.Count)} 条`
    : '0 条';
  if (preserveScroll) {
    wrap.scrollTop = scrollTop;
  } else {
    wrap.scrollTop = 0;
  }
}

function applyPanel(data) {
  panel = data;
  mountStaticIcon(element('workspace-icon'), data.ID);
  element('app-name').textContent = data.Name;
  element('version').textContent = data.Version;
  element('status').textContent = data.Status;

  const statusBadge = element('status-badge');
  statusBadge.className = 'badge ' + statusTone(data.Status);
  statusBadge.title = data.Directory;

  const allCount = data.Total.Translated + data.Total.Missing + data.Total.Kept;
  element('total-all').textContent = number.format(allCount);
  element('translated').textContent = number.format(data.Total.Translated);
  element('missing').textContent = number.format(data.Total.Missing);
  element('kept').textContent = number.format(data.Total.Kept);

  const group = element('group');
  const keepGroup = group.value;
  group.replaceChildren(
    new Option('全部', ''),
    ...data.Groups.map(item => new Option(`${item.Name}（待补 ${number.format(item.Missing)}）`, item.Name))
  );
  if (data.Groups.some(item => item.Name === keepGroup)) {
    group.value = keepGroup;
  }

  element('devin-info').hidden = current !== 'devin';
  element('agy-info').hidden = current !== 'agy';
  element('language').value = data.Pref.Language;
  element('cache-size').textContent = '缓存 ' + formatBytes(data.CacheBytes);

  renderRail(data.Apps);
  renderJobStrip(data);
  updateControls();
  if (data.Job.Running) watchTranslation();
}

async function refresh(preserveScroll = false) {
  const id = current;
  const data = await invoke('Panel', id);
  if (id !== current) return;
  applyPanel(data);
  await find(preserveScroll);
}

async function action(work, successToast = '', refreshAfter = true, preserveScroll = false) {
  if (pending) return;
  pending = true;
  updateControls();
  notice.clear();
  try {
    const result = await work();
    if (result === false) return;
    if (refreshAfter) await refresh(preserveScroll);
    if (successToast) {
      showToast(typeof successToast === 'function' ? successToast(result) : successToast);
    }
  } catch (error) {
    notice.show(errorText(error), 'error');
  } finally {
    pending = false;
    updateControls();
  }
}

for (const button of appButtons) {
  button.addEventListener('click', () => {
    if (pending || current === button.dataset.app) return;
    current = button.dataset.app;
    for (const item of appButtons) {
      item.setAttribute('aria-pressed', String(item === button));
    }
    offset = 0;
    filterState = '';
    for (const f of filterButtons) {
      f.setAttribute('aria-pressed', String(f.dataset.filter === ''));
    }
    element('group').value = '';
    element('query').value = '';
    action(refresh, '', false);
  });
}

for (const button of filterButtons) {
  button.addEventListener('click', () => {
    const next = button.dataset.filter || '';
    if (filterState === next) return;
    filterState = next;
    for (const item of filterButtons) {
      item.setAttribute('aria-pressed', String(item === button));
    }
    runFind(true);
  });
}

function runFind(reset = false) {
  if (reset) offset = 0;
  find(false).catch(error => notice.show(errorText(error), 'error'));
}

element('query').addEventListener('input', () => {
  clearTimeout(debounce);
  debounce = setTimeout(() => runFind(true), 175);
});
element('group').addEventListener('change', () => runFind(true));
element('reset-filters').addEventListener('click', () => {
  filterState = '';
  for (const item of filterButtons) {
    item.setAttribute('aria-pressed', String(item.dataset.filter === ''));
  }
  element('group').value = '';
  element('query').value = '';
  runFind(true);
});

window.addEventListener('keydown', event => {
  if (event.key === '/' && !event.ctrlKey && !event.metaKey && !event.altKey) {
    const active = document.activeElement;
    if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA' || active.tagName === 'SELECT')) return;
    if (document.querySelector('dialog[open]')) return;
    event.preventDefault();
    element('query').focus();
  }
});

element('prev').addEventListener('click', () => {
  offset = Math.max(0, offset - PAGE_SIZE);
  runFind(false);
});
element('next').addEventListener('click', () => {
  offset += PAGE_SIZE;
  runFind(false);
});

async function syncMaximizedState() {
  try {
    const max = await Window.IsMaximised();
    maximizeIcon.setState(max ? 'maximized' : 'normal');
    const label = max ? '向下还原' : '最大化';
    element('maximize').title = label;
    element('maximize').setAttribute('aria-label', label);
  } catch {}
}

element('minimize').addEventListener('click', () => Window.Minimise());
element('maximize').addEventListener('click', async () => {
  await Window.ToggleMaximise();
  syncMaximizedState();
});
element('close').addEventListener('click', () => Window.Close());
element('titlebar-drag').addEventListener('dblclick', async () => {
  if (window._wails?.flags?.nonClientRegionTracking) return;
  await Window.ToggleMaximise();
  syncMaximizedState();
});

for (const name of ['common:WindowMaximise', 'common:WindowUnMaximise', 'common:WindowRestore', 'windows:WindowMaximise', 'windows:WindowUnMaximise']) {
  try {
    Events.On(name, () => syncMaximizedState());
  } catch {}
}
window.addEventListener('resize', () => syncMaximizedState());
syncMaximizedState();

element('apply').addEventListener('click', () => action(() => invoke('Apply', current, false), msg => msg));
element('restore').addEventListener('click', () => action(() => invoke('Apply', current, true), msg => msg));
element('open').addEventListener('click', () => action(() => invoke('Open', current), '已发出启动请求，请查看应用窗口'));
element('scan').addEventListener('click', () => action(
  () => invoke('Scan', current),
  count => `已核对本机版本，共 ${number.format(count)} 条词条`
));
element('import').addEventListener('click', () => action(() => invoke('Import', current), '已导入词典'));
element('export').addEventListener('click', () => action(() => invoke('Export', current), '已导出词典', false));

element('language').addEventListener('change', () => action(async () => {
  const nextPref = { ...panel.Pref, Language: element('language').value };
  await invoke('SaveSettings', current, nextPref);
  if (!panel.Applied) {
    return '偏好已保存，点击「应用汉化」后生效';
  }
  try {
    return await invoke('Apply', current, false);
  } catch (applyError) {
    await refresh(true);
    notice.show(`偏好已保存；尚未立即写入应用（${errorText(applyError)}），退出软件后点「同步词典更新」即可生效。`, 'warn');
    return false;
  }
}, msg => msg, true, true));

element('clean').addEventListener('click', event => {
  if (!panel) return;
  element('clean-summary').textContent = `将清除本地扫描与诊断缓存（预计释放 ${formatBytes(panel.CacheBytes)}），已保存的词典与原始资源备份不受影响。`;
  cleanModal.open(event.currentTarget);
});
element('confirm-clean').addEventListener('click', () => {
  cleanModal.close('confirm');
  action(() => invoke('Clean'), size => `已清理缓存 ${formatBytes(size)}`, true, true);
});

function watchTranslation() {
  if (poll) return;
  poll = setInterval(async () => {
    if (pending) return;
    try {
      const id = current;
      const data = await invoke('Panel', id);
      if (id !== current || pending) return;
      applyPanel(data);
      const job = data.Job;
      if (!job.Running) {
        clearInterval(poll);
        poll = null;
        await find(true);
        if (job.Success === 0 && job.Failed > 0) {
          notice.show(job.Message || job.LastError || '本机翻译未成功', 'error');
        } else if (job.Message) {
          showToast(job.Message);
        }
      }
    } catch (error) {
      clearInterval(poll);
      poll = null;
      notice.show(errorText(error), 'error');
    }
  }, 950);
}

let probeSerial = 0;
let probeDebounce = null;

function syncOverrideBadge() {
  const hasCustom = Boolean(element('endpoint').value.trim() || element('model').value.trim());
  element('override-pill').hidden = !hasCustom;
}

async function refreshModelProbe(autoExpandOnFail = false) {
  const reqId = ++probeSerial;
  const customEndpoint = element('endpoint').value.trim();
  const refreshBtn = element('detect-refresh');
  refreshBtn.disabled = true;
  element('detect-label').textContent = '正在检测本机服务…';
  try {
    const probe = await invoke('DetectModel', customEndpoint);
    if (reqId !== probeSerial) return;
    const badge = element('detect-badge');
    let tone = '';
    if (!probe.Available) tone = 'badge-danger';
    else if (probe.State === 'running' || probe.State === 'standby') tone = 'badge-ok';
    else tone = 'badge-warn';
    badge.className = tone ? `badge ${tone}` : 'badge';
    element('detect-label').textContent = probe.Label || '未知状态';

    const backendTag = element('detect-backend');
    backendTag.textContent = probe.Backend || '';
    backendTag.hidden = !probe.Backend;

    element('detect-detail').textContent = probe.Detail || '留空将自动连接已识别的本地服务';

    element('endpoint').placeholder = probe.Endpoint && !customEndpoint
      ? `自动连接 ${probe.Endpoint}`
      : '例如 127.0.0.1:19820';
    element('model').placeholder = probe.Model
      ? `默认 ${probe.Model}`
      : '留空自动选择模型';

    const list = element('detected-models');
    list.replaceChildren(...(probe.Models || []).map(m => new Option(m, m)));

    if (autoExpandOnFail && !probe.Available && !element('model-override').open) {
      element('model-override').open = true;
      element('endpoint').focus();
    }
  } catch (error) {
    if (reqId !== probeSerial) return;
    element('detect-badge').className = 'badge badge-danger';
    element('detect-label').textContent = '服务检测失败';
    element('detect-backend').hidden = true;
    element('detect-detail').textContent = errorText(error);
  } finally {
    if (reqId === probeSerial) {
      refreshBtn.disabled = false;
    }
  }
}

element('detect-refresh').addEventListener('click', () => refreshModelProbe(false));
element('endpoint').addEventListener('input', () => {
  syncOverrideBadge();
  clearTimeout(probeDebounce);
  probeDebounce = setTimeout(() => refreshModelProbe(false), 325);
});
element('model').addEventListener('input', syncOverrideBadge);

element('translate').addEventListener('click', event => {
  if (!panel) return;
  if (isCurrentJobRunning()) {
    invoke('Cancel').catch(error => notice.show(errorText(error), 'error'));
    return;
  }
  const selectedGroup = element('group').value;
  let missingCount = panel.Total.Missing;
  if (selectedGroup) {
    const foundGroup = panel.Groups.find(g => g.Name === selectedGroup);
    if (foundGroup) missingCount = foundGroup.Missing;
  }
  element('limit-all-opt').textContent = selectedGroup
    ? `当前分组全部待补（${number.format(missingCount)} 条）`
    : `全部待补词条（${number.format(missingCount)} 条）`;

  element('endpoint').value = panel.Pref.Endpoint || '';
  element('model').value = panel.Pref.Model || '';
  syncOverrideBadge();
  const hasSavedOverride = Boolean(panel.Pref.Endpoint || panel.Pref.Model);
  element('model-override').open = hasSavedOverride;
  overrideChevron.setState(hasSavedOverride ? 'expanded' : 'collapsed');
  element('translate-error').hidden = true;
  translateModal.open(event.currentTarget);
  refreshModelProbe(!hasSavedOverride);
});

element('start').addEventListener('click', async event => {
  const button = event.currentTarget;
  const errorNode = element('translate-error');
  button.disabled = true;
  errorNode.hidden = true;
  try {
    await invoke('SaveSettings', current, {
      ...panel.Pref,
      Endpoint: element('endpoint').value,
      Model: element('model').value,
    });
    await invoke('Translate', current, Number(element('limit').value), element('group').value);
    translateModal.close('start');
    showToast('已启动本机翻译');
    await refresh(true);
    watchTranslation();
  } catch (error) {
    errorNode.textContent = errorText(error);
    errorNode.hidden = false;
  } finally {
    button.disabled = false;
  }
});

action(refresh, '', false);
