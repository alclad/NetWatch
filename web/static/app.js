(() => {
  'use strict';
  const $ = (s) => document.querySelector(s);
  const state = { devices: [], settings: {}, filter: 'all', group: '', query: '' };
  const els = { list: $('#deviceList'), empty: $('#emptyState'), toast: $('#toast'), connDot: $('#connectionDot'), connText: $('#connectionText'), deviceDialog: $('#deviceDialog'), settingsDialog: $('#settingsDialog') };
  const cards = new Map(); // id устройства -> DOM-элемент карточки (точечное обновление без перерисовки списка)
  const headings = new Map(); // группа -> DOM-элемент заголовка
  let renderScheduled = false;
  function scheduleRender() { if (renderScheduled) return; renderScheduled = true; setTimeout(() => { renderScheduled = false; render(); }, 80); }

  function statusOf(d) { const s = String(d.status || '').toLowerCase(); if (['up','online','ok','success','reachable'].includes(s)) return 'up'; if (['down','offline','fail','failed','error','unreachable'].includes(s)) return 'down'; return 'unknown'; }
  function statusLabel(s) { return s === 'up' ? 'В сети' : s === 'down' ? 'Недоступно' : 'Не проверялось'; }
  function escUrl(url) { try { const u = new URL(url, window.location.origin); return ['http:','https:'].includes(u.protocol) ? u.href : ''; } catch (_) { return ''; } }
  function notify(text, bad = false) { els.toast.textContent = text; els.toast.className = `toast visible ${bad ? 'bad-toast' : ''}`; clearTimeout(notify.timer); notify.timer = setTimeout(() => els.toast.className = 'toast', 3500); }
  function setConnection(ok, text) { els.connDot.className = `live-dot ${ok ? 'connected' : 'disconnected'}`; els.connText.textContent = text; }

  function render() {
    const q = state.query.trim().toLowerCase();
    const counts = { all: state.devices.length, up: 0, down: 0, unknown: 0 };
    state.devices.forEach(d => counts[statusOf(d)]++);
    $('#totalCount').textContent = counts.all; $('#onlineCount').textContent = counts.up; $('#offlineCount').textContent = counts.down;
    $('#lastUpdate').textContent = new Date().toLocaleTimeString('ru-RU', { hour:'2-digit', minute:'2-digit' });
    ['all','up','down','unknown'].forEach(k => { $(`#${k === 'all' ? 'all' : k}FilterCount`).textContent = counts[k]; });
    const visible = state.devices.filter(d => { const s = statusOf(d); const hay = `${d.name || ''} ${d.address || ''} ${d.url || ''} ${d.group || ''}`.toLowerCase(); return (state.filter === 'all' || s === state.filter) && (!state.group || (d.group || '') === state.group) && (!q || hay.includes(q)); });
    els.empty.hidden = state.devices.length !== 0 || visible.length !== 0;

    // Точечное обновление: карточки не пересоздаются при каждом ping-обновлении,
    // меняются только класс статуса и текст — список не «прыгает».
    const visibleIds = new Set(visible.map(d => d.id));
    let lastHeading = null;
    visible.forEach(d => {
      if (!state.group) {
        const g = groupOf(d);
        if (g !== lastHeading) {
          lastHeading = g;
          let h = headings.get(g);
          if (!h) { h = document.createElement('h3'); h.className = 'group-heading'; const t = document.createElement('span'); t.className = 'group-title'; t.textContent = g; const c = document.createElement('span'); c.className = 'group-count'; h.append(t, c); headings.set(g, h); }
          h.querySelector('.group-count').textContent = visible.filter(x => groupOf(x) === g).length;
          els.list.append(h); // append существующего узла — дешёвое перемещение
        }
      }
      let card = cards.get(d.id);
      if (!card) { card = deviceCard(d); cards.set(d.id, card); } else { updateCard(card, d); }
      els.list.append(card);
    });
    // Убрать карточки удалённых/отфильтрованных устройств
    for (const [id, el] of cards) if (!visibleIds.has(id)) { el.remove(); cards.delete(id); }
    for (const [g, el] of headings) {
      const used = !state.group && visible.some(d => groupOf(d) === g);
      if (!used) { el.remove(); headings.delete(g); }
    }
    const old = els.list.querySelector('.no-results'); if (old) old.remove();
    if (!visible.length && state.devices.length) { const p = document.createElement('p'); p.className='no-results'; p.textContent='Ничего не найдено по заданным условиям.'; els.list.append(p); }
    updateGroupControls();
  }
  function groupOf(d) { return (d.group || '').trim() || 'Без группы'; }
  function updateCard(card, d) {
    const s = statusOf(d);
    card.className = `device-card ${s}`;
    card.querySelector('.status-badge').textContent = statusLabel(s);
    const power = card.querySelector('.power');
    if (power) power.disabled = s !== 'up';
  }
  function updateGroupControls() {
    const groups = [...new Set(state.devices.map(d => (d.group || '').trim()).filter(Boolean))].sort((a, b) => a.localeCompare(b, 'ru'));
    const sel = $('#groupFilter');
    if (sel) { const cur = state.group; sel.replaceChildren(); const all = document.createElement('option'); all.value = ''; all.textContent = 'Все группы'; sel.append(all); groups.forEach(g => { const o = document.createElement('option'); o.value = g; o.textContent = g; sel.append(o); }); sel.value = groups.includes(cur) ? cur : ''; state.group = sel.value; }
    const dl = $('#groupSuggestions');
    if (dl) { dl.replaceChildren(); groups.forEach(g => { const o = document.createElement('option'); o.value = g; dl.append(o); }); }
  }
  function deviceCard(d) {
    const s = statusOf(d), card = document.createElement('article'); card.className = `device-card ${s}`;
    const indicator = document.createElement('span'); indicator.className = 'status-indicator';
    const info = document.createElement('div'); info.className = 'device-info';
    const title = document.createElement(d.url && escUrl(d.url) ? 'a' : 'span'); title.className='device-name'; title.textContent=d.name || d.address || 'Без имени'; if (title.tagName === 'A') { title.href=escUrl(d.url); title.target='_blank'; title.rel='noopener noreferrer'; }
    const address = document.createElement('span'); address.className='device-address'; address.textContent=d.address || 'Адрес не указан'; info.append(title, address);
    if (d.group) { const g = document.createElement('span'); g.className='group-badge'; g.textContent=d.group; address.append(document.createTextNode(' · '), g); }
    const stat = document.createElement('div'); stat.className='device-status'; const badge=document.createElement('span'); badge.className='status-badge'; badge.textContent=statusLabel(s); stat.append(badge);
    const actions=document.createElement('div'); actions.className='device-actions';
    const power=document.createElement('button'); power.className='icon-btn power'; power.type='button'; power.textContent='Выключить'; power.disabled = s !== 'up'; power.addEventListener('click', () => powerOff(d));
    const edit=document.createElement('button'); edit.className='icon-btn'; edit.type='button'; edit.textContent='Изменить'; edit.addEventListener('click', () => openDevice(d)); const del=document.createElement('button'); del.className='icon-btn danger'; del.type='button'; del.textContent='Удалить'; del.addEventListener('click', () => removeDevice(d)); actions.append(power, edit, del); card.append(indicator, info, stat, actions); return card;
  }
  async function powerOff(d) { if (!window.confirm(`Выключить «${d.name || d.address}»? Команда poweroff будет отправлена по SSH.`)) return; try { await api(`/api/devices/${encodeURIComponent(d.id)}/poweroff`, { method:'POST' }); notify('Команда выключения отправлена'); } catch (e) { notify(e.message, true); } }
  async function api(path, options = {}) { const res = await fetch(path, { ...options, headers: { 'Content-Type':'application/json', ...(options.headers || {}) } }); if (!res.ok) { let msg = `Ошибка ${res.status}`; try { const j=await res.json(); msg=j.error || msg; } catch (_) {} throw new Error(msg); } const text = await res.text(); return text ? JSON.parse(text) : null; }
  let lastSnapshot = '';
  function fingerprint(data) { return JSON.stringify(data.devices.map(d => [d.id, d.status, d.latency_ms, d.error, d.last_check, d.name, d.address, d.url, d.group])) + '|' + (data.system_error || ''); }
  function apply(data) { if (!data || !Array.isArray(data.devices)) return false; const fp = fingerprint(data); state.devices=data.devices; if (data.settings) state.settings=data.settings; const se=$('#systemError'); if (se) { se.hidden=!data.system_error; se.textContent=data.system_error||''; } if (fp === lastSnapshot) return false; lastSnapshot = fp; return true; }
  async function load() { try { apply(await api('/api/state')); render(); setConnection(true, 'Подключено'); } catch (e) { setConnection(false, 'Нет соединения'); notify(e.message, true); } }
  function openDevice(d = null) { $('#deviceDialogTitle').textContent = d ? 'Изменить устройство' : 'Добавить устройство'; $('#deviceId').value=d?.id || ''; $('#deviceName').value=d?.name || ''; $('#deviceAddress').value=d?.address || ''; $('#deviceUrl').value=d?.url || ''; $('#deviceGroup').value=d?.group || ''; $('#deviceError').textContent=''; els.deviceDialog.showModal(); }
  async function saveDevice(e) { if (e.submitter?.value === 'cancel') return; e.preventDefault(); const id=$('#deviceId').value; const body={name:$('#deviceName').value.trim(), address:$('#deviceAddress').value.trim(), url:$('#deviceUrl').value.trim(), group:$('#deviceGroup').value.trim()}; try { await api(id ? `/api/devices/${encodeURIComponent(id)}` : '/api/devices', { method:id?'PUT':'POST', body:JSON.stringify(body) }); els.deviceDialog.close(); notify(id ? 'Устройство обновлено' : 'Устройство добавлено'); await load(); } catch (err) { $('#deviceError').textContent=err.message; } }
  async function removeDevice(d) { if (!window.confirm(`Удалить «${d.name || d.address}»?`)) return; try { await api(`/api/devices/${encodeURIComponent(d.id)}`, {method:'DELETE'}); notify('Устройство удалено'); await load(); } catch (e) { notify(e.message, true); } }
  function openSettings() { $('#intervalSeconds').value=state.settings.interval_seconds ?? 60; $('#timeoutSeconds').value=state.settings.timeout_seconds ?? 5; $('#failureThreshold').value=state.settings.failure_threshold ?? 3; $('#concurrency').value=state.settings.concurrency ?? 10; $('#sshUser').value=state.settings.ssh_user ?? 'root'; $('#sshPort').value=state.settings.ssh_port ?? 22; $('#sshKeyPath').value=state.settings.ssh_key_path ?? ''; $('#settingsError').textContent=''; els.settingsDialog.showModal(); }
  async function saveSettings(e) { if (e.submitter?.value === 'cancel') return; e.preventDefault(); const settings={interval_seconds:+$('#intervalSeconds').value, timeout_seconds:+$('#timeoutSeconds').value, failure_threshold:+$('#failureThreshold').value, concurrency:+$('#concurrency').value, ssh_user:$('#sshUser').value.trim(), ssh_port:+$('#sshPort').value, ssh_key_path:$('#sshKeyPath').value.trim()}; try { await api('/api/settings', {method:'PUT', body:JSON.stringify(settings)}); els.settingsDialog.close(); notify('Настройки сохранены'); await load(); } catch (err) { $('#settingsError').textContent=err.message; } }
  $('#addDeviceBtn').addEventListener('click', () => openDevice()); $('#emptyAddBtn').addEventListener('click', () => openDevice()); $('#settingsBtn').addEventListener('click', openSettings); $('#deviceForm').addEventListener('submit', saveDevice); $('#settingsForm').addEventListener('submit', saveSettings); $('#searchInput').addEventListener('input', e => { state.query=e.target.value; render(); }); $('#groupFilter').addEventListener('change', e => { state.group=e.target.value; render(); }); document.querySelectorAll('.filter').forEach(b => b.addEventListener('click', () => { document.querySelectorAll('.filter').forEach(x => x.classList.remove('active')); b.classList.add('active'); state.filter=b.dataset.filter; render(); }));
  const events = new EventSource('/api/events'); events.addEventListener('state', e => { try { if (apply(JSON.parse(e.data))) { setConnection(true, 'Подключено'); scheduleRender(); } } catch (_) {} }); events.onopen=() => setConnection(true, 'Подключено'); events.onerror=() => setConnection(false, 'Переподключение…'); load();
})();
