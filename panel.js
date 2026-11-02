(() => {
  'use strict';
  let data = PANEL_DATA;
  let mode = 'quota';
  let expanded = new Set(window.__panelInitialFocus ? [window.__panelInitialFocus] : []);
  let confirmation = null;
  let pending = 0;
  let sequence = 0;
  let actionError = '';
  let lastFit = 0;
  const $ = id => document.getElementById(id);
  const tr = (en, zh) => data.language === 'zh-CN' ? zh : en;
  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  const icon = kind => {
    const holder = el('span');
    // Fixed artwork only; user/account strings are always assigned textContent.
    const shapes = {
      healthy:'<circle cx="8" cy="8" r="6"/><path d="m5 8 2 2 4-4"/>',
      low:'<path d="M8 2 14 13H2Z"/><path d="M8 6v3m0 1.5v1"/>',
      empty:'<circle cx="8" cy="8" r="6"/><path d="M5 8h6"/>',
      unknown:'<circle cx="8" cy="8" r="6"/><path d="M8 4.5V8l2 1.5"/>'
    };
    holder.innerHTML = `<svg class="status-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" aria-hidden="true">${shapes[kind] || shapes.unknown}</svg>`;
    return holder.firstChild;
  };
  function button(text, cls, handler) {
    const node = el('button', cls, text);
    node.type = 'button';
    node.disabled = Boolean(pending || data.busy);
    node.addEventListener('click', handler);
    return node;
  }
  function formatTime(raw) {
    const date = new Date(raw);
    if (!raw || !Number.isFinite(date.getTime()) || date.getFullYear() < 2000) return tr('Not provided', '未提供');
    return date.toLocaleString(data.language === 'zh-CN' ? 'zh-CN' : 'en-GB', {month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit'});
  }
  function age(raw) {
    const date = new Date(raw);
    if (!raw || !Number.isFinite(date.getTime()) || date.getFullYear() < 2000) return tr('Waiting for first update', '等待首次更新');
    const minutes = Math.max(0, Math.floor((Date.now() - date.getTime()) / 60000));
    if (!minutes) return tr('Updated just now', '刚刚更新');
    if (minutes < 60) return tr(`Updated ${minutes} min ago`, `${minutes}分钟前更新`);
    return tr(`Last update ${formatTime(raw)}`, `上次更新 ${formatTime(raw)}`);
  }
  function updatedText() {
    if (!data.accounts.length) return tr('Waiting for first update','等待首次更新');
    if (data.accounts.some(account => !account.fresh)) return tr('Some accounts need updating','部分账号用量待更新');
    return age(data.updatedAt);
  }
  function fit() {
    requestAnimationFrame(() => {
      const content = $('content');
      const child = content.firstElementChild;
      const height = Math.ceil($('title').closest('.panel-head').offsetHeight +
        document.querySelector('.panel-footer').offsetHeight + (child ? child.scrollHeight : 80) +
        ['banner','action-error'].reduce((total, id) => total + ($(id).hidden ? 0 : $(id).offsetHeight + 10), 0) + 2);
      if (height !== lastFit && typeof window.panelFit === 'function') {
        lastFit = height;
        Promise.resolve(window.panelFit(height)).catch(() => {});
      }
    });
  }
  async function send(type, fields = {}) {
    if (pending || data.busy) return;
    actionError = '';
    pending = ++sequence;
    const action = {requestId:pending, type, active:data.active, ...fields};
    render();
    try {
      if (typeof window.panelSend !== 'function') throw new Error(tr('The panel connection is unavailable', '面板连接暂不可用'));
      await window.panelSend(action);
    } catch (error) {
      pending = 0;
      actionError = String(error.message || error);
      render();
    }
  }
  function showConfirmation(type, account) {
    confirmation = {type, account:account || '', active:data.active};
    render();
    const cancel = document.querySelector('.cancel-confirm');
    if (cancel) cancel.focus();
  }
  function confirmationNode() {
    const box = el('div', 'confirm');
    const target = data.accounts.find(a => a.id === confirmation.account);
    const description = confirmation.type === 'switch'
      ? tr(`Switch to ${target ? target.name : ''}? Claude will restart and running replies may stop.`, `切换到${target ? target.name : ''}？这会重启Claude，正在运行的回复可能中断。`)
      : confirmation.type === 'add'
        ? tr('Claude will restart on its sign-in screen. Your current sign-in is kept.', 'Claude将重启并打开登录页面，当前登录状态会保留。')
        : tr('Importing chats restarts Claude. Running replies may stop.', '导入聊天会重启Claude，正在运行的回复可能中断。');
    box.append(el('p', '', description));
    const actions = el('div', 'confirm-actions');
    actions.append(button(tr('Cancel', '取消'), 'switch cancel-confirm', () => { confirmation = null; render(); }));
    actions.append(button(tr('Confirm restart', '确认重启'), 'primary confirm-action', () => {
      if (!confirmation || pending || data.busy) return;
      const request = confirmation;
      confirmation = null;
      send(request.type, {account:request.account, confirmed:true, active:request.active});
    }));
    box.append(actions);
    return box;
  }
  function detailLine(label, value) {
    const line = el('div','detail-line');
    line.append(el('span','',label), el('span','',value));
    return line;
  }
  function accountNode(account) {
    const card = el('article', `account${account.current ? ' current' : ''}`);
    card.dataset.account = account.id;
    const head = el('div','account-head');
    const identity = el('div','identity');
    const name = el('h2','',account.name);
    name.title = account.name + (account.plan ? ` (${account.plan})` : '');
    identity.append(name);
    if (account.current) identity.append(el('span','current-tag',tr('Current','当前')));
    const status = el('span',`account-status ${account.tone || 'unknown'}`);
    status.title = account.status;
    status.append(icon(account.tone), el('span','',account.status));
    head.append(identity,status);
    const meters = el('div','meters');
    for (const meter of account.meters) {
      const known = typeof meter.remaining === 'number' && Number.isFinite(meter.remaining);
      const row = el('div',`meter ${meter.tone || 'unknown'}${known ? '' : ' no-data'}`);
      const label = el('span','label',meter.name);
      label.title = meter.name;
      const track = el('div','track');
      track.setAttribute('role','meter');
      track.setAttribute('aria-label',tr(`${meter.name}, remaining`, `${meter.name}剩余`));
      if (known) {
        const amount = Math.max(0, Math.min(100, meter.remaining));
        const fill = el('div','fill');
        fill.style.width = `${amount}%`;
        track.setAttribute('aria-valuenow', String(amount));
        track.setAttribute('aria-valuemin','0'); track.setAttribute('aria-valuemax','100');
        track.append(fill);
      }
      row.append(label,track,el('span','value',known ? `${Math.round(meter.remaining)}%` : '—'));
      meters.append(row);
    }
    const footer = el('div','account-footer');
    const reset = el('div','reset');
    reset.append(icon('unknown'),el('span','',account.reset || age(account.checkedAt)));
    const actions = el('div','footer-actions');
    const detail = button(expanded.has(account.id) ? tr('Less','收起') : tr('Details','详情'), 'text-button detail', () => {
      if (expanded.has(account.id)) expanded.delete(account.id); else expanded.add(account.id);
      render();
    });
    detail.setAttribute('aria-expanded', String(expanded.has(account.id)));
    actions.append(detail);
    if (!account.current) actions.append(button(tr('Switch','切换账号'), 'switch switch-account', () => showConfirmation('switch',account.id)));
    footer.append(reset,actions);
    card.append(head,meters,footer);
    if (expanded.has(account.id)) {
      const details = el('div','details');
      if (account.plan) details.append(detailLine(tr('Plan','套餐'),account.plan));
      for (const meter of account.meters) {
        details.append(detailLine(tr(`${meter.name} reset`,`${meter.name}重置`),formatTime(meter.resetsAt)));
        if (typeof meter.allowance === 'number') details.append(detailLine(tr(`${meter.name} cap`,`${meter.name}上限`),tr(`${meter.allowance}% of total week`,`总周额度的${meter.allowance}%`)));
      }
      details.append(detailLine(tr('Updated','更新时间'),formatTime(account.checkedAt)));
      if (!account.fresh) details.append(el('p','problem',tr('These are last-known remaining values.','当前显示上次查询到的剩余额度。')));
      if (account.problem) details.append(el('p','problem',account.problem));
      card.append(details);
    } else if (account.problem) {
      card.append(el('p','problem',account.problem));
    }
    if (confirmation && confirmation.type === 'switch' && confirmation.account === account.id) card.append(confirmationNode());
    card.addEventListener('click', event => {
      if (event.target.closest('button,input,select,.details,.confirm')) return;
      if (expanded.has(account.id)) expanded.delete(account.id); else expanded.add(account.id);
      render();
    });
    return card;
  }
  function settingsNode() {
    const list = el('div','settings-list');
    const back = button(tr('‹ Back to quotas','‹ 返回额度'), 'text-button', () => { mode = 'quota'; confirmation = null; render(); });
    list.append(back);
    const toggle = (label, type, checked) => {
      const row = el('div','settings-row');
      const text = el('label','',label);
      const input = el('input');
      input.type = 'checkbox'; input.checked = checked; input.id = `setting-${type}`;
      input.disabled = Boolean(pending || data.busy);
      text.htmlFor = input.id;
      input.addEventListener('change',() => send(type,{enabled:input.checked}));
      row.append(text,input); list.append(row);
    };
    toggle(tr('Auto-switch when nearly full','额度接近用尽时自动切换'),'auto',data.autoSwitch);
    toggle(tr('Use expiring weekly allowance','优先使用即将重置的周额度'),'spend',data.spendWeekly);
    const row = el('div','settings-row');
    const label = el('label','',tr('Auto-switch threshold','自动切换阈值')); label.htmlFor = 'setting-threshold';
    const select = el('select'); select.id = label.htmlFor; select.disabled = Boolean(pending || data.busy);
    for (const value of [90,94,96,98]) { const option = el('option','',`${value}%`); option.value = value; option.selected = value === data.threshold; select.append(option); }
    select.addEventListener('change',() => send('threshold',{threshold:Number(select.value)})); row.append(label,select); list.append(row);
    toggle(tr('Start at login','开机启动'),'autostart',data.autostart);
    const actions = el('div','settings-actions');
    actions.append(button(tr('Add account…','添加账号…'),'',() => showConfirmation('add')));
    actions.append(button(tr('Import chats from all accounts','导入所有账号的聊天'),'',() => showConfirmation('import')));
    actions.append(button(tr('Open data folder','打开数据文件夹'),'',() => send('folder')));
    actions.append(button(tr('Check for updates','查看更新'),'',() => send('update')));
    list.append(actions);
    if (confirmation) list.append(confirmationNode());
    return list;
  }
  function render() {
    document.documentElement.lang = data.language || 'en';
    document.title = tr('Account quotas','账号额度');
    $('title').textContent = mode === 'quota' ? tr('Account quotas','账号额度') : tr('Account management','账号管理');
    $('subtitle').textContent = mode === 'quota' ? tr('Remaining · longer means more room','剩余额度 · 条越长，余量越多') : tr('Existing switching settings','沿用当前切换设置');
    $('count').textContent = mode === 'quota' ? tr(`${data.accounts.length} accounts`,`${data.accounts.length}个账号`) : '';
    $('settings').setAttribute('aria-label',tr('Settings','设置'));
    $('close').setAttribute('aria-label',tr('Close','关闭'));
    $('content').setAttribute('aria-label',tr('Account quotas','账号额度'));
    const banner = pending ? tr('Working…','正在处理…') : data.busy ? tr('Operation in progress…','操作进行中…') : data.status || data.note || '';
    $('banner').textContent = banner; $('banner').hidden = !banner;
    $('action-error').textContent = actionError; $('action-error').hidden = !actionError;
    const scrollTop = $('content').scrollTop;
    const focusId = document.activeElement && document.activeElement.id;
    $('content').replaceChildren();
    if (mode === 'settings') $('content').append(settingsNode());
    else {
      const list = el('div','account-list');
      for (const account of data.accounts) list.append(accountNode(account));
      if (!data.accounts.length) list.append(el('div','empty-state',tr('No saved accounts yet. Sign in to Claude and allow the switcher to detect it.','尚无已保存的账号。在Claude中登录后，等待切换器识别。')));
      $('content').append(list);
    }
    $('content').scrollTop = scrollTop;
    if (focusId && $(focusId) && !$(focusId).disabled) $(focusId).focus();
    $('updated').textContent = updatedText();
    $('manage').textContent = mode === 'quota' ? tr('Account management ›','账号管理 ›') : tr('Back to quotas','返回额度');
    $('switch-note').textContent = tr('Switching accounts restarts Claude','切换账号将重启Claude');
    fit();
  }
  window.panelReceive = message => {
    if (message.view) {
      if (confirmation && confirmation.active !== message.view.active) confirmation = null;
      data = message.view;
    }
    if (message.requestId && message.requestId === pending) { pending = 0; actionError = message.error || ''; }
    if (message.show) { mode = 'quota'; confirmation = null; actionError = ''; }
    if (message.focus) expanded.add(message.focus);
    render();
    if (message.focus) requestAnimationFrame(() => {
      const card = Array.from(document.querySelectorAll('.account')).find(node => node.dataset.account === message.focus);
      if (card) card.scrollIntoView({block:'nearest'});
    });
  };
  window.panelShown = () => {
    mode = 'quota'; confirmation = null; actionError = '';
    render();
  };
  function hide() { if (typeof window.panelHide === 'function') window.panelHide(); }
  $('close').addEventListener('click',hide);
  $('settings').addEventListener('click',() => { mode = mode === 'quota' ? 'settings' : 'quota'; confirmation = null; render(); });
  $('manage').addEventListener('click',() => { mode = mode === 'quota' ? 'settings' : 'quota'; confirmation = null; render(); });
  window.addEventListener('keydown',event => { if (event.key === 'Escape') { if (confirmation) { confirmation = null; render(); } else if (mode === 'settings') { mode = 'quota'; render(); } else hide(); } });
  window.addEventListener('blur',hide);
  document.querySelector('.panel').classList.add('opening');
  setTimeout(() => document.querySelector('.panel').classList.remove('opening'),320);
  setInterval(() => { $('updated').textContent = updatedText(); },30000);
  if (window.__panelQueued) window.panelReceive(window.__panelQueued);
  else render();
})();
