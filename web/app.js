'use strict';
// Burrow admin UI. No framework, no build step. App state lives in the URL.

// ---------- helpers ----------
const $ = (s, el = document) => el.querySelector(s);
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, '');
    else el.setAttribute(k, v);
  }
  for (const c of kids.flat(Infinity)) if (c != null && c !== false) el.append(c.nodeType ? c : String(c));
  return el;
}
const SVGNS = 'http://www.w3.org/2000/svg';
const ICONS = {
  nas: 'M4 4h16v7H4zM4 13h16v7H4zM7 7.5h.01M7 16.5h.01M11 7.5h6M11 16.5h6',
  folder: 'M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2h8.5A1.5 1.5 0 0 1 21 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5z',
  home: 'M4 10.5L12 4l8 6.5V20h-5.5v-5.5h-5V20H4z',
  chevron: 'M9.5 6l6 6-6 6',
  file: 'M6 3h8l4 4v14H6zM14 3v4h4',
  image: 'M4 5h16v14H4zM4 16l5-5 4 4 3-3 4 4M15 9.5a1.5 1.5 0 1 0 0-.01',
  video: 'M3 6h13v12H3zM16 10l5-3v10l-5-3',
  audio: 'M9 18V6l11-2v12M9 18a3 3 0 1 1-3-3 3 3 0 0 1 3 3zM20 16a3 3 0 1 1-3-3 3 3 0 0 1 3 3z',
  doc: 'M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 16h6',
  archive: 'M4 4h16v4H4zM5 8v12h14V8M10 12h4',
  code: 'M8 7l-5 5 5 5M16 7l5 5-5 5',
  '3d': 'M12 3l8 4.5v9L12 21l-8-4.5v-9zM12 12l8-4.5M12 12v9M12 12L4 7.5',
  search: 'M10.5 17a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13zM15.5 15.5L21 21',
  link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
  star: 'M12 3.5l2.6 5.3 5.9.9-4.3 4.1 1 5.8L12 16.9l-5.2 2.7 1-5.8L3.5 9.7l5.9-.9z',
  x: 'M6 6l12 12M18 6L6 18',
  download: 'M12 4v11M7 10l5 5 5-5M5 20h14',
  copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
  inbox: 'M3 13l3-8h12l3 8v6H3zM3 13h5l1 3h6l1-3h5',
  activity: 'M3 12h4l3-8 4 16 3-8h4',
  chart: 'M4 20V10M10 20V4M16 20v-7M22 20H2',
  gear: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
  qr: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h2v2h-2zM18 14h2v2h-2zM14 18h2v2h-2zM18 18h2v2h-2z',
  up: 'M12 19V5M5 12l7-7 7 7',
  user: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 20a8 8 0 0 1 16 0',
  phone: 'M8 2h8a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H8a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zM11 18h2',
  trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3',
  upload: 'M12 16V4M7 9l5-5 5 5M5 20h14',
  plus: 'M12 5v14M5 12h14',
  edit: 'M4 20h4L19 9l-4-4L4 16zM13 7l4 4',
};
function icon(name, cls = '') {
  const s = document.createElementNS(SVGNS, 'svg');
  s.setAttribute('viewBox', '0 0 24 24');
  s.setAttribute('class', 'icon ' + cls);
  s.setAttribute('aria-hidden', 'true');
  const p = document.createElementNS(SVGNS, 'path');
  p.setAttribute('d', ICONS[name] || ICONS.file);
  s.append(p);
  return s;
}
// put replaces el's children, skipping null and false (replaceChildren would print "null").
function put(el, ...kids) { el.replaceChildren(...kids.flat(Infinity).filter(k => k != null && k !== false)); }
function info(text) {
  return h('button', { type: 'button', class: 'info', 'aria-label': 'More about this' }, 'i', h('span', { class: 'tip', role: 'tooltip' }, text));
}
addEventListener('keydown', e => { if (e.key === 'Escape') document.querySelectorAll('.info').forEach(b => b.dataset.dismissed = ''); });
addEventListener('pointerover', e => { const b = e.target.closest?.('.info'); if (b) delete b.dataset.dismissed; });

async function api(path, opts = {}) {
  const o = { method: opts.method || 'GET', headers: { 'X-Burrow': '1' } };
  if (opts.body !== undefined) { o.body = JSON.stringify(opts.body); o.headers['Content-Type'] = 'application/json'; }
  const r = await fetch(path, o);
  let data = null;
  try { data = await r.json(); } catch { /* empty */ }
  if (r.status === 401 && !opts.quiet) { boot(); throw new Error('Signed out'); }
  if (!r.ok) throw new Error((data && data.error) || `Request failed (${r.status})`);
  return data;
}

const fmtSize = n => {
  if (n == null) return '';
  if (n < 1024) return n + ' B';
  const u = ['KB', 'MB', 'GB', 'TB', 'PB'];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return (n >= 100 ? n.toFixed(0) : n.toFixed(1)) + ' ' + u[i];
};
const thisYear = new Date().getFullYear();
function fmtDate(t) {
  if (!t) return '';
  const d = new Date(t * 1000);
  const opt = { month: 'short', day: 'numeric' };
  if (d.getFullYear() !== thisYear) opt.year = 'numeric';
  else { opt.hour = '2-digit'; opt.minute = '2-digit'; opt.hour12 = false; }
  return d.toLocaleString('en-US', opt);
}
function fmtWhen(t) {
  if (!t) return 'never';
  const s = t - Date.now() / 1000;
  const a = Math.abs(s);
  const unit = a < 3600 ? [Math.round(a / 60), 'minute'] : a < 86400 ? [Math.round(a / 3600), 'hour'] : [Math.round(a / 86400), 'day'];
  const txt = `${unit[0]} ${unit[1]}${unit[0] === 1 ? '' : 's'}`;
  return s > 0 ? `in ${txt}` : `${txt} ago`;
}
const fullDate = t => t ? new Date(t * 1000).toLocaleString('en-US', { dateStyle: 'medium', timeStyle: 'short', hour12: false }) : '';
const parentOf = p => p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '';
async function copy(text, btn) {
  try { await navigator.clipboard.writeText(text); } catch {
    const t = h('textarea', {}, text); document.body.append(t); t.select(); document.execCommand('copy'); t.remove();
  }
  if (btn) { const old = btn.textContent; btn.textContent = 'Copied'; setTimeout(() => (btn.textContent = old), 1400); }
}
function shareState(s) {
  if (s.state === 'active' && s.expires && s.expires - Date.now() / 1000 < 86400) return 'expiring';
  return s.state;
}
const STATE_TEXT = { active: 'Active', expiring: 'Expires soon', expired: 'Expired', revoked: 'Turned off', 'used-up': 'Limit reached', missing: 'Target missing', unavailable: 'Owner lost access' };

// ---------- URL state ----------
function getState() {
  const p = new URLSearchParams(location.search);
  return { view: p.get('view') || 'browse', path: p.get('path') || '', q: p.get('q') || '', kind: p.get('kind') || '', sort: p.get('sort') || '', sel: p.get('sel') || '', share: p.get('share') || '', tab: p.get('tab') || '', within: p.get('within') || '', add: p.get('add') || '', user: p.get('user') || '' };
}
function nav(patch, replace = false) {
  const s = { ...getState(), ...patch };
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(s)) if (v && !(k === 'view' && v === 'browse')) p.set(k, v);
  const url = '?' + p.toString();
  if (url === location.search) { route(); return; }
  history[replace ? 'replaceState' : 'pushState'](null, '', url || '?');
  route();
}
addEventListener('popstate', () => route());

// ---------- boot / gate screens ----------
let ME = null;
async function boot() {
  const st = await fetch('/api/state').then(r => r.json());
  ME = st;
  if (!st.setup_done) return setupScreen(st);
  if (!st.signed_in) return loginScreen();
  shell();
  route();
}

function gate(form) {
  const app = $('#app');
  app.replaceChildren(h('div', { class: 'gate' }, form));
}

function loginScreen() {
  const err = h('p', { class: 'err', role: 'alert' });
  const codeField = h('label', { class: 'field', hidden: true }, h('span', {}, 'Authenticator code', info('The 6-digit code from your authenticator app, or one of your recovery codes.')),
    h('input', { name: 'code', autocomplete: 'one-time-code', inputmode: 'numeric', class: 'mono' }));
  const f = h('form', {},
    h('h1', {}, h('img', { src: '/favicon.svg', alt: '' }), 'Burrow'),
    h('label', { class: 'field' }, h('span', {}, 'Username'), h('input', { name: 'user', autocomplete: 'username', required: true, autofocus: true })),
    h('label', { class: 'field' }, h('span', {}, 'Password'), h('input', { name: 'password', type: 'password', autocomplete: 'current-password', required: true })),
    codeField,
    err,
    h('button', { class: 'btn primary', type: 'submit' }, 'Sign in'));
  f.addEventListener('submit', async e => {
    e.preventDefault();
    err.textContent = '';
    const r = await fetch('/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Burrow': '1' }, body: JSON.stringify({ user: f.user.value, password: f.password.value, code: f.code.value }) });
    const d = await r.json().catch(() => ({}));
    if (r.ok) { boot(); return; }
    if (d.need_code) { codeField.hidden = false; f.code.required = true; f.code.focus(); return; }
    err.textContent = d.error || 'Sign-in failed';
  });
  gate(f);
}

async function setupScreen(st, key = '') {
  if (st.needs_key && !key) {
    const err = h('p', { class: 'err', role: 'alert' });
    const f = h('form', {},
      h('h1', {}, h('img', { src: '/favicon.svg', alt: '' }), 'Set up Burrow'),
      h('label', { class: 'field' }, h('span', {}, 'Setup key', info('Printed in the container log on first start. Only needed when setting up from outside your local network.')), h('input', { name: 'key', required: true, class: 'mono', autofocus: true })),
      err, h('button', { class: 'btn primary', type: 'submit' }, 'Continue'));
    f.addEventListener('submit', async e => {
      e.preventDefault();
      const r = await fetch('/api/roots/available?key=' + encodeURIComponent(f.key.value));
      if (r.ok) setupScreen(st, f.key.value); else err.textContent = 'That key is wrong.';
    });
    gate(f);
    return;
  }
  const roots = await fetch('/api/roots/available?key=' + encodeURIComponent(key)).then(r => r.ok ? r.json() : []).catch(() => []);
  const err = h('p', { class: 'err', role: 'alert' });
  const checks = h('div', { class: 'checks' }, roots.length ? roots.map(r => h('label', {}, h('input', { type: 'checkbox', value: r.name, checked: true }), r.name))
    : h('p', { class: 'muted' }, 'No folders found. Check the container\u2019s /data mount.'));
  const f = h('form', {},
    h('h1', {}, h('img', { src: '/favicon.svg', alt: '' }), 'Set up Burrow'),
    h('label', { class: 'field' }, h('span', {}, 'Admin username'), h('input', { name: 'user', autocomplete: 'username', required: true, value: 'admin' })),
    h('label', { class: 'field' }, h('span', {}, 'Password', info('At least 10 characters. If you forget it: docker exec burrow burrow reset-password <username>')), h('input', { name: 'password', type: 'password', autocomplete: 'new-password', minlength: 10, required: true })),
    h('label', { class: 'field' }, h('span', {}, 'Public address', info('The https address people will open, for example https://share.example.com. Links and QR codes use it. Leave empty to use whatever address you open Burrow on.')), h('input', { name: 'url', placeholder: 'https://share.example.com', inputmode: 'url' })),
    h('div', { class: 'field' }, h('span', {}, 'Folders to index and share', info('Top-level folders of the library mount. Unchecked folders are invisible to search, browsing, links and MCP.')), checks),
    err,
    h('button', { class: 'btn primary', type: 'submit' }, 'Finish setup'));
  f.addEventListener('submit', async e => {
    e.preventDefault();
    err.textContent = '';
    const sel = [...checks.querySelectorAll('input:checked')].map(i => i.value);
    try {
      await api('/api/setup', { method: 'POST', body: { user: f.user.value, password: f.password.value, public_url: f.url.value, roots: sel, key }, quiet: true });
      boot();
    } catch (x) { err.textContent = x.message; }
  });
  gate(f);
}

// ---------- shell ----------
let LIBS = [];
function shell() {
  const app = $('#app');
  const searchInput = h('input', { type: 'text', enterkeyhint: 'search', id: 'q', placeholder: 'Search every file and folder', autocomplete: 'off', spellcheck: 'false', 'aria-label': 'Search' });
  let t = null;
  searchInput.addEventListener('input', () => {
    clearTimeout(t);
    t = setTimeout(() => {
      const v = searchInput.value.trim();
      const s = getState();
      if (v) nav({ view: 'search', q: v, sel: '' }, s.view === 'search');
      else if (s.view === 'search') nav({ view: 'browse', q: '', sel: '' }, true);
    }, 110);
  });
  searchInput.addEventListener('keydown', e => {
    if (e.key === 'ArrowDown') { e.preventDefault(); moveSel(1); }
    if (e.key === 'Enter') { e.preventDefault(); openSel(); }
  });
  app.replaceChildren(
    h('div', { class: 'shell' },
      h('aside', { class: 'side' },
        h('div', { class: 'brand' }, h('img', { src: '/favicon.svg', alt: '' }), h('span', {}, 'Burrow')),
        h('nav', { class: 'nav', id: 'nav', 'aria-label': 'Sections' }),
        h('div', { class: 'side-foot', id: 'foot' })),
      h('section', { class: 'main' },
        h('div', { class: 'bar' },
          h('label', { class: 'search' }, icon('search'), searchInput, h('kbd', { class: 'hide-s' }, '/'))),
        h('div', { class: 'sub', id: 'sub' }),
        h('div', { class: 'content', id: 'content' })),
      h('aside', { class: 'panel', id: 'panel', hidden: true, 'aria-label': 'Details' })));
  loadNav();
  refreshFoot();
  setInterval(refreshFoot, 15000);
}

async function loadNav() {
  const [libs, favs] = await Promise.all([api('/api/ls?path='), api('/api/favorites')]);
  LIBS = libs.entries;
  const s = getState();
  const navEl = $('#nav');
  const link = (label, ic, patch, current, extra) =>
    h('a', { href: '?' + new URLSearchParams(Object.entries(patch).filter(([, v]) => v)).toString(), 'aria-current': current ? 'page' : null,
      onclick: e => { e.preventDefault(); nav({ q: '', sel: '', share: '', tab: '', kind: '', sort: '', within: '', path: '', add: '', user: '', ...patch }); } }, icon(ic), h('span', {}, label), extra);
  const inLib = s.view === 'browse';
  navEl.replaceChildren(...[
    h('h3', {}, 'Library'),
    link('All libraries', 'folder', { view: 'browse', path: '' }, inLib && !s.path),
    LIBS.map(l => h('div', { class: 'lib' }, link(l.name, l.remote ? 'nas' : 'folder', { view: 'browse', path: l.path }, inLib && (s.path === l.path || s.path.startsWith(l.path + '/'))))),
    favs.length ? h('h3', { class: 'lib' }, 'Favorites') : null,
    favs.map(f => h('div', { class: 'lib' }, link(f.name, f.dir ? 'folder' : f.kind || 'file', f.dir ? { view: 'browse', path: f.path } : { view: 'browse', path: parentOf(f.path), sel: f.path }, inLib && s.path === f.path))),
    link('Recently deleted', 'trash', { view: 'trash' }, s.view === 'trash'),
    h('h3', {}, 'Sharing'),
    link('Links', 'link', { view: 'shares' }, s.view === 'shares'),
    link('Activity', 'activity', { view: 'activity' }, s.view === 'activity'),
    link('Overview', 'chart', { view: 'overview' }, s.view === 'overview'),
    ME.admin ? link('Users', 'user', { view: 'users' }, s.view === 'users') : null,
    link('Settings', 'gear', { view: 'settings' }, s.view === 'settings')].flat(Infinity).filter(Boolean));
}

async function refreshFoot() {
  try {
    const st = await api('/api/status', { quiet: true });
    $('#foot').replaceChildren(...[
      st.files != null ? h('div', { class: 'num' }, `${st.files.toLocaleString()} files, ${fmtSize(st.bytes)}`) : null, // totals are admin-only
      h('div', {}, st.scanning ? `Scanning, ${st.scanned_dirs.toLocaleString()} folders so far` : st.last_end ? `Indexed ${fmtWhen(st.last_end)}` : 'Not indexed yet')].filter(Boolean));
  } catch { /* ignore */ }
}

// ---------- routing ----------
let CURRENT = [];      // entries in the current list (for keyboard navigation)
let routeSeq = 0;
async function route() {
  if (!$('#content')) return;
  const s = getState();
  const seq = ++routeSeq;
  const q = $('#q');
  if (q && document.activeElement !== q) q.value = s.view === 'search' ? s.q : '';
  delete $('#content').dataset.drop;
  loadNav();
  try {
    if (s.view === 'search') await viewSearch(s, seq);
    else if (s.view === 'shares') await viewShares(s, seq);
    else if (s.view === 'activity') await viewActivity(s, seq);
    else if (s.view === 'overview') await viewOverview(s, seq);
    else if (s.view === 'settings') await viewSettings(s, seq);
    else if (s.view === 'users' && ME.admin) await viewUsers(s, seq);
    else if (s.view === 'trash') await viewTrash(s, seq);
    else await viewBrowse(s, seq);
  } catch (e) {
    if (seq !== routeSeq) return;
    put($('#sub'), );
    put($('#content'), h('div', { class: 'empty' }, h('b', {}, 'Couldn’t load this view.'), e.message, ' ', h('a', { href: '#', onclick: ev => { ev.preventDefault(); route(); } }, 'Retry')));
  }
  if (seq !== routeSeq) return;
  if (s.sel) openPanelFor(s.sel); else if (s.share) openSharePanel(s.share); else if (s.tab === 'newdrop' && s.view === 'shares') openDropPanel(); else if (s.tab === 'newfolder' && s.view === 'shares') openFolderPanel(s.add); else if (s.view === 'users' && ME.admin && (s.user || s.tab === 'newuser')) openUserPanel(s.user); else closePanel(false);
}

// ---------- browse ----------
async function viewBrowse(s, seq) {
  const data = await api('/api/ls?path=' + encodeURIComponent(s.path));
  if (seq !== routeSeq) return;
  const crumbs = h('nav', { class: 'crumbs', 'aria-label': 'Folder path' });
  const home = [icon('home', 'home'), h('span', { class: 'home-label' }, 'Libraries')];
  crumbs.append(s.path ? h('a', { href: '?', 'aria-label': 'Libraries', onclick: e => { e.preventDefault(); nav({ path: '', sel: '' }); } }, home) : h('b', { 'aria-current': 'page' }, home));
  let acc = '';
  const segs = s.path ? s.path.split('/') : [];
  segs.forEach((seg, i) => {
    acc = acc ? acc + '/' + seg : seg;
    const p = acc;
    crumbs.append(icon('chevron', 'sep'));
    crumbs.append(i === segs.length - 1 ? h('b', { 'aria-current': 'page' }, seg) : h('a', { href: '?path=' + encodeURIComponent(p), 'data-drop': p, onclick: e => { e.preventDefault(); nav({ path: p, sel: '' }); } }, seg));
  });
  const actions = s.path && (data.can_share || data.can_write) ? h('div', { class: 'row' },
    data.can_write ? uploadButton(s.path) : null,
    data.can_write ? newFolderControl(s.path) : null,
    data.can_share ? h('button', { class: 'btn', onclick: () => nav({ sel: s.path }) }, icon('link'), 'Share this folder') : null) : null;
  put($('#sub'), crumbs, h('span', { class: 'grow' }), actions);
  CURRENT = data.entries;
  if (data.can_write && s.path) $('#content').dataset.drop = s.path;
  put($('#content'), fileTable(data.entries, { search: false }) || (s.path ? emptyState('This folder is empty.')
    : ME.admin ? emptyState('No libraries are enabled.', 'Pick folders in Settings.') : emptyState('No folders are shared with you yet.', 'Ask an admin for access.')));
}

function emptyState(title, body) { return h('div', { class: 'empty' }, h('b', {}, title), body || ''); }

function highlight(text, terms) {
  if (!terms.length) return [text];
  const low = text.toLowerCase();
  const marks = [];
  for (const t of terms) {
    let i = 0;
    while ((i = low.indexOf(t, i)) !== -1) { marks.push([i, i + t.length]); i += t.length; }
  }
  if (!marks.length) return [text];
  marks.sort((a, b) => a[0] - b[0]);
  const out = [];
  let pos = 0;
  for (const [a, b] of marks) {
    if (a < pos) continue;
    out.push(text.slice(pos, a), h('mark', {}, text.slice(a, b)));
    pos = b;
  }
  out.push(text.slice(pos));
  return out;
}

function kindIcon(e) {
  if (e.remote) return icon('nas', 'k-folder');
  if (e.dir) return icon('folder', 'k-folder');
  if (e.kind === 'image' && ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp'].includes(e.ext)) return h('img', { class: 'th', loading: 'lazy', alt: '', src: '/api/thumb?path=' + encodeURIComponent(e.path) });
  return icon(e.kind || 'file');
}

function fileTable(entries, { search, terms = [] }) {
  if (!entries.length) return null;
  const s = getState();
  const tb = h('tbody');
  for (const e of entries) {
    const name = search ? highlight(e.name, terms) : [e.name];
    const tr = h('tr', { 'aria-selected': s.sel === e.path ? 'true' : 'false', 'data-path': e.path, tabindex: '-1',
      'data-drop': e.dir ? e.path : null, draggable: e.path.includes('/') ? 'true' : null, ondragstart: ev => dragOut(ev, e),
      onclick: ev => { if (ev.target.closest('a')) return; nav({ sel: e.path }, true); },
      ondblclick: () => openEntry(e) },
      h('td', { class: 'name' },
        h('span', {}, kindIcon(e),
          e.dir ? h('a', { class: 'label', href: '?path=' + encodeURIComponent(e.path), onclick: ev => { ev.preventDefault(); openEntry(e); } }, name) : h('span', { class: 'label' }, name),
          h('span', { class: 'flags' }, e.shared ? h('span', { title: `${e.shared} active link${e.shared > 1 ? 's' : ''}` }, icon('link')) : null, e.fav ? h('span', { title: 'Favorite' }, icon('star')) : null)),
        search ? h('span', { class: 'where' }, highlight(parentOf(e.path) || '/', terms)) : h('span', { class: 'where phone-meta' }, e.dir ? fmtDate(e.mtime) : `${fmtSize(e.size)}, ${fmtDate(e.mtime)}`)),
      h('td', { class: 'r num muted' }, e.dir ? '' : fmtSize(e.size)),
      h('td', { class: 'r num muted hide-s' }, fmtDate(e.mtime)));
    tb.append(tr);
  }
  return h('table', { class: search ? 'files' : 'files browse' },
    h('thead', {}, h('tr', {}, h('th', {}, 'Name'), h('th', { class: 'r' }, 'Size'), h('th', { class: 'r hide-s' }, 'Modified'))), tb);
}

function openEntry(e) {
  if (e.dir) nav({ view: 'browse', path: e.path, q: '', sel: '' });
  else nav({ sel: e.path }, true);
}
function moveSel(d) {
  if (!CURRENT.length) return;
  const s = getState();
  let i = CURRENT.findIndex(e => e.path === s.sel);
  i = Math.max(0, Math.min(CURRENT.length - 1, i + d));
  nav({ sel: CURRENT[i].path }, true);
  setTimeout(() => document.querySelector(`tr[data-path="${CSS.escape(CURRENT[i].path)}"]`)?.scrollIntoView({ block: 'nearest' }), 30);
}
function openSel() {
  const s = getState();
  const e = CURRENT.find(x => x.path === s.sel) || CURRENT[0];
  if (e) openEntry(e);
}
addEventListener('keydown', e => {
  const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName);
  if (e.key === '/' && !typing) { e.preventDefault(); $('#q')?.focus(); $('#q')?.select(); return; }
  if (e.key === 'Escape') {
    if (!$('#panel')?.hidden) { closePanel(); return; }
    if (typing) document.activeElement.blur();
    return;
  }
  if (typing) return;
  if (e.key === 'ArrowDown' || e.key === 'j') { e.preventDefault(); moveSel(1); }
  if (e.key === 'ArrowUp' || e.key === 'k') { e.preventDefault(); moveSel(-1); }
  if (e.key === 'Enter') { e.preventDefault(); openSel(); }
  if (e.key === 'Backspace' && getState().view === 'browse' && getState().path) { e.preventDefault(); nav({ path: parentOf(getState().path), sel: '' }); }
});

// ---------- search ----------
const KINDS = [['', 'Everything'], ['folder', 'Folders'], ['doc', 'Documents'], ['image', 'Images'], ['video', 'Video'], ['audio', 'Audio'], ['3d', '3D'], ['code', 'Code'], ['archive', 'Archives']];
async function viewSearch(s, seq) {
  const qs = new URLSearchParams({ q: s.q, kind: s.kind, sort: s.sort, root: s.within, limit: 300 });
  const res = await api('/api/search?' + qs);
  if (seq !== routeSeq) return;
  const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Type' },
    KINDS.map(([k, label]) => h('button', { type: 'button', 'aria-pressed': s.kind === k ? 'true' : 'false', onclick: () => nav({ kind: k, sel: '' }, true) }, label)));
  const sort = h('select', { 'aria-label': 'Sort', onchange: e => nav({ sort: e.target.value }, true) },
    [['', 'Best match'], ['newest', 'Newest'], ['oldest', 'Oldest'], ['largest', 'Largest'], ['name', 'Name']].map(([v, l]) => h('option', { value: v, selected: s.sort === v }, l)));
  const within = h('select', { 'aria-label': 'Library', onchange: e => nav({ within: e.target.value }, true) },
    h('option', { value: '' }, 'All libraries'), LIBS.map(l => h('option', { value: l.path, selected: s.within === l.path }, l.name)));
  const more = res.total > res.entries.length ? ` (showing ${res.entries.length})` : '';
  put($('#sub'), seg, within, sort, h('span', { class: 'grow' }),
    h('span', { class: 'timing num' }, `${res.total.toLocaleString()}${res.total >= 10000 ? '+' : ''} result${res.total === 1 ? '' : 's'}${more} in ${res.took_ms.toFixed(1)} ms`));
  const terms = s.q.toLowerCase().split(/\s+/).filter(w => w && !w.startsWith('-') && !w.includes(':'));
  CURRENT = res.entries;
  put($('#content'), fileTable(res.entries, { search: true, terms }) ||
    emptyState(`Nothing matches “${s.q}”.`, s.kind || s.within ? 'Try Everything and All libraries, or fewer words.' : 'Every word has to appear somewhere in the path. Try fewer words.'));
}

// ---------- side panel: file or folder ----------
function panelEl() { return $('#panel'); }
function closePanel(updateUrl = true) {
  const p = panelEl();
  if (!p || p.hidden) return;
  p.hidden = true;
  p.replaceChildren();
  if (updateUrl) nav({ sel: '', share: '', add: '', user: '', tab: ['newdrop', 'newfolder', 'newuser'].includes(getState().tab) ? '' : getState().tab }, true);
}
function panelShell(title, ...body) {
  const p = panelEl();
  p.hidden = false;
  p.replaceChildren(
    h('div', { class: 'panel-head' }, h('h2', {}, title), h('button', { class: 'iconbtn', 'aria-label': 'Close', onclick: () => closePanel() }, icon('x'))),
    h('div', { class: 'panel-body' }, body));
}

let panelFor = '';
async function openPanelFor(path) {
  document.querySelectorAll('.files tr').forEach(tr => tr.setAttribute('aria-selected', tr.dataset.path === path ? 'true' : 'false'));
  if (panelFor === path && !panelEl().hidden) return;
  panelFor = path;
  let d;
  try { d = await api('/api/info?path=' + encodeURIComponent(path)); } catch (e) { panelShell('Not available', h('p', { class: 'muted' }, e.message)); return; }
  if (panelFor !== path) return;
  const e = d.entry;
  const raw = '/api/raw?path=' + encodeURIComponent(e.path);
  let pv = null;
  if (d.preview === 'image') pv = h('img', { src: raw, alt: e.name });
  else if (d.preview === 'video') pv = h('video', { src: raw, controls: true, preload: 'metadata' });
  else if (d.preview === 'audio') pv = h('audio', { src: raw, controls: true, preload: 'metadata' });
  else if (d.preview === 'pdf') pv = h('iframe', { src: raw, title: e.name });
  else if (d.preview === 'text') { pv = h('pre', {}, 'Loading…'); fetch(raw).then(r => r.text()).then(t => pv.textContent = t.slice(0, 20000)); }

  const favBtn = h('button', { class: 'iconbtn', 'aria-pressed': e.fav ? 'true' : 'false', 'aria-label': 'Favorite', title: 'Favorite',
    onclick: async () => {
      const on = favBtn.getAttribute('aria-pressed') !== 'true';
      if (on) await api('/api/favorites', { method: 'POST', body: { path: e.path } }); else await api('/api/favorites?path=' + encodeURIComponent(e.path), { method: 'DELETE' });
      favBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
      loadNav();
    } }, icon('star'));
  const copyBtn = h('button', { class: 'btn', onclick: ev => copy(e.path, ev.currentTarget) }, icon('copy'), 'Copy path');
  const s = getState();
  panelShell(e.name,
    pv ? h('div', { class: 'pv' }, pv) : null,
    h('dl', { class: 'facts' },
      h('dt', {}, 'Folder'), h('dd', {}, h('a', { href: '?path=' + encodeURIComponent(parentOf(e.path)), onclick: ev => { ev.preventDefault(); nav({ view: 'browse', path: parentOf(e.path), q: '', sel: e.path }); } }, parentOf(e.path) || 'Libraries')),
      e.dir ? [h('dt', {}, 'Contains'), h('dd', { class: 'num' }, `${(d.items || 0).toLocaleString()} items, ${fmtSize(d.total_size || 0)}`)] : [h('dt', {}, 'Size'), h('dd', { class: 'num' }, `${fmtSize(e.size)} (${e.size.toLocaleString()} bytes)`)],
      h('dt', {}, 'Modified'), h('dd', {}, fullDate(e.mtime))),
    h('div', { class: 'row' },
      e.dir ? h('a', { class: 'btn', href: '/api/zip?path=' + encodeURIComponent(e.path) }, icon('download'), 'Download .zip')
        : h('a', { class: 'btn', href: raw + '&dl=1' }, icon('download'), 'Download'),
      e.dir && s.path !== e.path ? h('button', { class: 'btn', onclick: () => nav({ view: 'browse', path: e.path, q: '', sel: '' }) }, 'Open') : null,
      copyBtn, favBtn),
    d.can_change ? h('div', { class: 'row' }, renameControl(e), deleteControl(e)) : null,
    d.can_share || d.shares.length ? h('h3', {}, 'Share links') : null,
    d.can_share || d.shares.length ? linksList(d.shares, () => { panelFor = ''; openPanelFor(path); route(); }) : null,
    d.can_share ? h('div', { class: 'row' }, newLinkForm(e, () => { panelFor = ''; openPanelFor(path); route(); }), addToFolderControl(e, () => { panelFor = ''; openPanelFor(path); route(); })) : null);
}

function addToFolderControl(e, done) {
  const wrap = h('div', { class: 'row' });
  const open = async () => {
    const all = (await api('/api/shares')).filter(x => x.mode === 'collection' && x.state === 'active');
    const sel = h('select', { 'aria-label': 'Shared folder' },
      all.map(x => h('option', { value: x.id }, x.label)), h('option', { value: '__new' }, 'New shared folder\u2026'));
    const err = h('span', { class: 'err' });
    wrap.replaceChildren(sel,
      h('button', { class: 'btn primary', onclick: async () => {
        try {
          if (sel.value === '__new') { nav({ view: 'shares', tab: 'newfolder', sel: '', share: '', q: '', add: e.path }); return; }
          await api(`/api/shares/${sel.value}/items`, { method: 'POST', body: { paths: [e.path] } });
          done();
        } catch (x) { err.textContent = x.message; }
      } }, 'Add'),
      h('button', { class: 'btn', onclick: () => wrap.replaceChildren(btn) }, 'Cancel'), err);
    sel.focus();
  };
  const btn = h('button', { class: 'btn', onclick: open }, icon('folder'), 'Add to shared folder');
  wrap.append(btn);
  return wrap;
}

function expiryControl(defHours) {
  const opts = [['1', '1 hour'], ['24', '1 day'], ['168', '7 days'], ['720', '30 days'], ['-1', 'Never'], ['date', 'Pick a date']];
  const sel = h('select', { name: 'exp' }, opts.map(([v, l]) => h('option', { value: v }, l)));
  const def = String(defHours || 168);
  sel.value = opts.some(o => o[0] === def) ? def : (defHours > 0 ? '168' : '-1');
  const date = h('input', { type: 'datetime-local', name: 'expdate', hidden: true });
  sel.addEventListener('change', () => { date.hidden = sel.value !== 'date'; });
  const read = () => {
    if (sel.value === 'date') { const t = Date.parse(date.value); return t ? { expires_at: Math.floor(t / 1000) } : { expires_hours: -1 }; }
    return { expires_hours: Number(sel.value) };
  };
  return { el: h('div', { class: 'row' }, sel, date), read };
}

let SETTINGS = null;
async function settings() { if (!SETTINGS) SETTINGS = await api('/api/settings'); return SETTINGS; }

function newLinkForm(e, done) {
  const wrap = h('div');
  const openBtn = h('button', { class: 'btn primary', onclick: async () => {
    const st = await settings();
    const exp = expiryControl(st.default_expiry_hours);
    const err = h('p', { class: 'err' });
    const f = h('form', { class: 'stack' },
      h('div', { class: 'field' }, h('span', {}, 'Expires'), exp.el),
      h('div', { class: 'grid2' },
        h('label', { class: 'field' }, h('span', {}, 'Password'), h('input', { name: 'pw', type: 'text', autocomplete: 'off', placeholder: 'None' })),
        h('label', { class: 'field' }, h('span', {}, 'Download limit', info('Each browser counts once per file, whether it previews or downloads it. Download all counts once.')), h('input', { name: 'max', type: 'number', min: 0, placeholder: 'None' }))),
      h('label', { class: 'field' }, h('span', {}, 'Note to recipient'), h('textarea', { name: 'note', rows: 2 })),
      h('label', { class: 'field' }, h('span', {}, 'Custom link name', info('Optional. Replaces the random part of the link, e.g. /s/taxes-for-mom. Random links are much harder to guess.')), h('input', { name: 'slug', class: 'mono', autocomplete: 'off', placeholder: 'Random' })),
      err,
      h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'submit' }, 'Create link'), h('button', { class: 'btn', type: 'button', onclick: () => wrap.replaceChildren(openBtn) }, 'Cancel')));
    f.addEventListener('submit', async ev => {
      ev.preventDefault();
      try {
        const s = await api('/api/shares', { method: 'POST', body: { path: e.path, mode: 'link', password: f.pw.value, max_downloads: Number(f.max.value || 0), note: f.note.value, slug: f.slug.value, ...exp.read() } });
        copy(s.url);
        done();
      } catch (x) { err.textContent = x.message; }
    });
    wrap.replaceChildren(f);
    f.querySelector('select').focus();
  } }, icon('link'), 'New link');
  wrap.append(openBtn);
  return wrap;
}

function linksList(shares, changed) {
  if (!shares.length) return h('p', { class: 'muted' }, 'No links yet.');
  return h('div', { class: 'links' }, shares.map(s => linkCard(s, changed)));
}

function linkCard(s, changed) {
  const st = shareState(s);
  const qrBox = h('div', { class: 'qr', hidden: true });
  const qrBtn = h('button', { class: 'iconbtn', title: 'QR code', 'aria-label': 'QR code', 'aria-pressed': 'false', onclick: () => {
    const on = qrBox.hidden;
    qrBox.hidden = !on;
    qrBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
    if (on && !qrBox.childElementCount) qrBox.append(h('img', { src: `/api/shares/${s.id}/qr.png?size=400`, alt: 'QR code for ' + s.url }), h('a', { href: `/api/shares/${s.id}/qr.png?size=1024&download=1` }, 'Download QR (PNG)'));
  } }, icon('qr'));
  const urlIn = h('input', { value: s.url, readonly: true, 'aria-label': 'Link', onfocus: e => e.target.select() });
  return h('div', { class: 'link' },
    h('div', { class: 'row' }, h('span', { class: 'state ' + st }, STATE_TEXT[st] || st), h('span', { class: 'grow' }),
      h('a', { href: '?view=shares&share=' + s.id, onclick: e => { e.preventDefault(); nav({ view: 'shares', share: s.id, sel: '', q: '' }); } }, 'Details')),
    h('div', { class: 'url' }, urlIn, h('button', { class: 'btn', onclick: e => copy(s.url, e.currentTarget) }, 'Copy'), qrBtn),
    h('div', { class: 'meta' },
      h('span', {}, s.expires ? `Expires ${fmtWhen(s.expires)}` : 'No expiry'),
      h('span', { class: 'num' }, `${s.downloads}${s.max_downloads ? ' of ' + s.max_downloads : ''} download${s.downloads === 1 && !s.max_downloads ? '' : 's'}`),
      s.has_password ? h('span', {}, 'Password') : null,
      s.last_access ? h('span', {}, `Opened ${fmtWhen(s.last_access)}`) : null),
    qrBox,
    s.state === 'active' ? h('div', { class: 'row' }, h('button', { class: 'btn danger', onclick: async () => { await api('/api/shares/' + s.id, { method: 'PATCH', body: { revoked: true } }); changed(); } }, 'Turn off')) : null);
}

// ---------- shares view ----------
async function viewShares(s, seq) {
  const all = await api('/api/shares');
  if (seq !== routeSeq) return;
  const filter = s.tab || 'active';
  const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Filter' },
    [['active', 'Active'], ['all', 'All'], ['collection', 'Shared folders'], ME.admin ? ['drop', 'File drops'] : null, ['inactive', 'Inactive']].filter(Boolean).map(([k, l]) =>
      h('button', { type: 'button', 'aria-pressed': filter === k ? 'true' : 'false', onclick: () => nav({ tab: k === 'active' ? '' : k, share: '' }, true) }, l)));
  put($('#sub'), h('b', {}, 'Links'), seg, h('span', { class: 'grow' }),
    h('button', { class: 'btn', onclick: () => nav({ tab: 'newfolder', share: '' }) }, icon('folder'), 'New shared folder'),
    ME.admin ? h('button', { class: 'btn', onclick: () => nav({ tab: 'newdrop', share: '' }) }, icon('inbox'), 'New file drop') : null);
  const rows = all.filter(x => ['all', 'newdrop', 'newfolder'].includes(filter) ? true : filter === 'active' ? x.state === 'active' : filter === 'drop' || filter === 'collection' ? x.mode === filter : x.state !== 'active');
  CURRENT = [];
  if (!rows.length) {
    put($('#content'), emptyState(filter === 'active' ? 'No active links.' : 'Nothing here.', filter === 'active' ? 'Select any file or folder and choose New link.' : ''));
    return;
  }
  const tb = h('tbody');
  for (const x of rows) {
    const st = shareState(x);
    tb.append(h('tr', { 'aria-selected': s.share === x.id ? 'true' : 'false', onclick: () => nav({ share: x.id }, true) },
      h('td', { class: 'name' }, h('span', {}, icon(x.mode === 'drop' ? 'inbox' : x.dir ? 'folder' : 'file', x.dir && x.mode !== 'drop' ? 'k-folder' : ''), h('span', { class: 'label' }, x.label || x.path)),
        h('span', { class: 'where' }, x.mode === 'drop' ? 'File drop into drop/' + x.path : x.mode === 'collection' ? `Shared folder, ${x.items || 0} item${x.items === 1 ? '' : 's'} plus uploads${x.allow_upload ? ', recipients can add files' : ''}` : x.path)),
      h('td', {}, h('span', { class: 'state ' + st }, STATE_TEXT[st])),
      ME.admin ? h('td', { class: 'muted hide-s' }, x.owner) : null,
      h('td', { class: 'r num muted hide-s' }, x.expires ? fmtDate(x.expires) : 'Never'),
      h('td', { class: 'r num' }, x.mode === 'drop' ? '' : `${x.downloads}${x.max_downloads ? '/' + x.max_downloads : ''}`),
      h('td', { class: 'r muted hide-s' }, x.last_access ? fmtWhen(x.last_access) : '')));
  }
  put($('#content'), h('table', { class: 'files' },
    h('thead', {}, h('tr', {}, h('th', {}, 'Link'), h('th', {}, 'State'), ME.admin ? h('th', { class: 'hide-s' }, 'Owner') : null, h('th', { class: 'r hide-s' }, 'Expires'), h('th', { class: 'r' }, 'Downloads'), h('th', { class: 'r hide-s' }, 'Last opened'))), tb));
}

async function collectionEditor(x, changed) {
  const v = await api(`/api/shares/${x.id}/items`);
  const fileIn = h('input', { type: 'file', multiple: true, hidden: true });
  const status = h('span', { class: 'muted' });
  fileIn.addEventListener('change', async () => {
    if (!fileIn.files.length) return;
    const fd = new FormData();
    for (const f of fileIn.files) fd.append('files', f, f.name);
    status.textContent = `Uploading ${fileIn.files.length} file${fileIn.files.length === 1 ? '' : 's'}…`;
    const r = await fetch(`/api/shares/${x.id}/upload`, { method: 'POST', headers: { 'X-Burrow': '1' }, body: fd });
    status.textContent = r.ok ? '' : 'Upload failed';
    changed();
  });
  const row = (e, actions, missing) => h('tr', {},
    h('td', { class: 'name' }, h('span', {}, kindIcon(e), h('span', { class: 'label' }, e.name)), e.path !== e.name ? h('span', { class: 'where' }, missing ? 'Missing or no longer shared: ' + e.path : parentOf(e.path)) : null),
    h('td', { class: 'r num muted' }, e.dir ? '' : fmtSize(e.size)),
    h('td', { class: 'r' }, actions));
  const items = v.items.map(it => row(it, h('button', { class: 'btn', onclick: async () => { await api(`/api/shares/${x.id}/items?path=${encodeURIComponent(it.path)}`, { method: 'DELETE' }); changed(); } }, 'Remove'), it.missing));
  const ups = v.uploads.map(u => row({ ...u, path: u.name, kind: u.dir ? 'folder' : u.kind, ext: '' }, h('button', { class: 'btn danger', onclick: async ev => {
    const b = ev.currentTarget;
    if (!b.dataset.armed) { b.dataset.armed = '1'; b.textContent = 'Delete file'; return; }
    await api(`/api/shares/${x.id}/uploads?name=${encodeURIComponent(u.name)}`, { method: 'DELETE' }); changed();
  } }, 'Delete')));
  return h('div', {},
    h('h3', {}, 'Contents'),
    items.length || ups.length ? h('table', { class: 'files' }, h('tbody', {}, items, ups)) : h('p', { class: 'muted' }, 'Empty.'),
    h('div', { class: 'row', style: 'margin-top:8px' }, ME.admin ? [h('button', { class: 'btn', onclick: () => fileIn.click() }, icon('up'), 'Upload files'), fileIn, status] : null,
      info(ME.admin ? `Add files from any folder with Add to shared folder, or upload new ones here. Uploads are saved to ${v.upload_folder}, which is created with the first upload. Library items are linked, not copied, so changes to them show up for recipients right away.`
        : 'Add files from any folder you can share with Add to shared folder. Items are linked, not copied, so changes to them show up for recipients right away.')));
}

async function openFolderPanel(addPath) {
  const st = await settings();
  const exp = expiryControl(st.default_expiry_hours);
  const err = h('p', { class: 'err' });
  const f = h('form', { class: 'stack' },
    h('label', { class: 'field' }, h('span', {}, 'Name'), h('input', { name: 'label', required: true, placeholder: 'Trip photos for Dad' })),
    addPath ? h('p', { class: 'muted' }, 'Starts with ', h('b', {}, addPath)) : null,
    h('div', { class: 'field' }, h('span', {}, 'Expires', info('The link and everything in it stop working at this time. You can extend it later.')), exp.el),
    h('label', { class: 'field' }, h('span', {}, 'Password'), h('input', { name: 'pw', autocomplete: 'off', placeholder: 'None' })),
    h('label', { class: 'field' }, h('span', {}, 'Note to recipient'), h('textarea', { name: 'note', rows: 2 })),
    ME.admin ? h('div', { class: 'checks' }, h('label', {}, h('input', { type: 'checkbox', name: 'up' }), 'Recipients can add files')) : null,
    err,
    h('button', { class: 'btn primary', type: 'submit' }, 'Create shared folder'));
  f.addEventListener('submit', async ev => {
    ev.preventDefault();
    try {
      const s = await api('/api/shares', { method: 'POST', body: { mode: 'collection', label: f.label.value, password: f.pw.value, note: f.note.value, allow_upload: !!f.up?.checked, items: addPath ? [addPath] : [], ...exp.read() } });
      copy(s.url);
      nav({ tab: '', add: '', share: s.id });
    } catch (x) { err.textContent = x.message; }
  });
  panelShell('New shared folder', f);
  f.label.focus();
}

async function openSharePanel(id) {
  let x;
  try { x = (await api('/api/shares')).find(s => s.id === id); } catch { x = null; }
  if (!x) { panelShell('Link not found', h('p', { class: 'muted' }, 'It may have been deleted.')); return; }
  const events = await api(`/api/shares/${id}/events?limit=100`);
  const changed = () => { openSharePanel(id); if (getState().view === 'shares') viewShares(getState(), routeSeq); };
  const exp = expiryControl(-1);
  const purge = x.mode !== 'link' ? h('div', { class: 'checks', hidden: true }, h('label', {}, h('input', { type: 'checkbox' }), 'Also delete the files uploaded to it')) : null;
  const editErr = h('p', { class: 'err' });
  const saved = h('span', { class: 'saved' });
  const f = h('form', { class: 'stack' },
    h('label', { class: 'field' }, h('span', {}, 'Label'), h('input', { name: 'label', value: x.label })),
    h('div', { class: 'field' }, h('span', {}, 'Change expiry'), exp.el),
    h('div', { class: 'grid2' },
      h('label', { class: 'field' }, h('span', {}, x.has_password ? 'New password' : 'Password'), h('input', { name: 'pw', autocomplete: 'off', placeholder: x.has_password ? 'Unchanged' : 'None' })),
      x.mode === 'link' ? h('label', { class: 'field' }, h('span', {}, 'Download limit'), h('input', { name: 'max', type: 'number', min: 0, value: x.max_downloads || '', placeholder: 'None' })) : h('span')),
    h('label', { class: 'field' }, h('span', {}, 'Note to recipient'), h('textarea', { name: 'note', rows: 2 }, x.note)),
    x.mode === 'collection' && ME.admin ? h('div', { class: 'checks' }, h('label', {}, h('input', { type: 'checkbox', name: 'up', checked: x.allow_upload }), 'Recipients can add files')) : null,
    editErr,
    h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'submit' }, 'Save changes'), saved));
  exp.el.querySelector('select').prepend(h('option', { value: 'keep', selected: true }, x.expires ? `Keep (${fullDate(x.expires)})` : 'Keep (never)'));
  exp.el.querySelector('select').value = 'keep';
  f.addEventListener('submit', async ev => {
    ev.preventDefault();
    const body = { label: f.label.value, note: f.note.value };
    if (f.pw.value) body.password = f.pw.value;
    if (f.max) body.max_downloads = Number(f.max.value || 0);
    if (f.up) body.allow_upload = f.up.checked;
    const sel = exp.el.querySelector('select').value;
    if (sel !== 'keep') Object.assign(body, exp.read());
    try { await api('/api/shares/' + id, { method: 'PATCH', body }); saved.textContent = 'Saved ' + new Date().toLocaleTimeString('en-US', { hour12: false }); changed(); } catch (e) { editErr.textContent = e.message; }
  });
  const contents = x.mode === 'collection' ? await collectionEditor(x, changed) : null;
  const target = x.mode === 'drop' || x.mode === 'collection'
    ? h('dd', {}, 'drop/' + x.path)
    : h('dd', {}, h('a', { href: '#', onclick: e => { e.preventDefault(); nav({ view: 'browse', path: x.dir ? x.path : parentOf(x.path), sel: x.path, share: '', tab: '' }); } }, x.path));
  panelShell(x.label || x.path,
    linkCard(x, changed),
    h('dl', { class: 'facts' },
      h('dt', {}, x.mode === 'link' ? 'Shares' : 'Uploads to'), target,
      ME.admin ? [h('dt', {}, 'Owner'), h('dd', {}, x.owner)] : null,
      h('dt', {}, 'Created'), h('dd', {}, fullDate(x.created)),
      h('dt', {}, 'Link id'), h('dd', { class: 'mono' }, x.id)),
    contents,
    h('h3', {}, 'Edit'), f,
    h('div', { class: 'row' },
      x.state !== 'active' && x.state !== 'missing' ? h('button', { class: 'btn', onclick: async () => { await api('/api/shares/' + id, { method: 'PATCH', body: { revoked: false, reset_count: x.state === 'used-up' } }); changed(); } }, x.state === 'used-up' ? 'Reset download count' : 'Turn back on') : null,
      h('button', { class: 'btn danger', onclick: async ev => {
        const b = ev.currentTarget;
        if (b.dataset.armed) { await api('/api/shares/' + id + (purge && purge.querySelector('input').checked ? '?purge=1' : ''), { method: 'DELETE' }); nav({ share: '' }, true); return; }
        b.dataset.armed = '1'; b.textContent = `Delete “${x.label || x.id}”`;
        if (purge) purge.hidden = false;
      } }, 'Delete link'), purge),
    h('h3', {}, 'Activity'),
    events.length ? h('table', { class: 'files' }, h('tbody', {}, events.map(ev => h('tr', {},
      h('td', { class: 'num muted' }, fmtDate(ev.ts)),
      h('td', {}, EVENT_TEXT[ev.kind] || ev.kind, ev.detail ? h('span', { class: 'where' }, ev.detail) : null),
      h('td', { class: 'r mono muted', title: ev.ua }, ev.ip))))) : h('p', { class: 'muted' }, 'Nobody has opened this link yet.'));
}
const EVENT_TEXT = { view: 'Opened', download: 'Downloaded', zip: 'Downloaded .zip', upload: 'Uploaded', 'password-fail': 'Wrong password' };

async function openDropPanel() {
  const st = await settings();
  const exp = expiryControl(st.default_expiry_hours);
  const err = h('p', { class: 'err' });
  const f = h('form', { class: 'stack' },
    h('label', { class: 'field' }, h('span', {}, 'Name', info('Also the folder name the uploads are saved in, inside the drop folder.')), h('input', { name: 'label', required: true, placeholder: 'Photos from Mom' })),
    h('div', { class: 'field' }, h('span', {}, 'Expires'), exp.el),
    h('label', { class: 'field' }, h('span', {}, 'Password'), h('input', { name: 'pw', autocomplete: 'off', placeholder: 'None' })),
    h('label', { class: 'field' }, h('span', {}, 'Note on the upload page'), h('textarea', { name: 'note', rows: 2 })),
    err,
    h('button', { class: 'btn primary', type: 'submit' }, 'Create file drop'));
  f.addEventListener('submit', async ev => {
    ev.preventDefault();
    try {
      const s = await api('/api/shares', { method: 'POST', body: { mode: 'drop', label: f.label.value, password: f.pw.value, note: f.note.value, ...exp.read() } });
      copy(s.url);
      nav({ tab: '', share: s.id });
    } catch (x) { err.textContent = x.message; }
  });
  panelShell('New file drop', f);
  f.label.focus();
}

// ---------- activity ----------
async function viewActivity(s, seq) {
  const tab = s.tab || 'files';
  const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Activity type' },
    [['files', 'File changes'], ['links', 'Link activity']].map(([k, l]) => h('button', { type: 'button', 'aria-pressed': tab === k ? 'true' : 'false', onclick: () => nav({ tab: k === 'files' ? '' : k }, true) }, l)));
  CURRENT = [];
  if (tab === 'links') {
    const ev = await api('/api/events?limit=500');
    if (seq !== routeSeq) return;
    put($('#sub'), seg);
    if (!ev.length) { put($('#content'), emptyState('No link activity yet.')); return; }
    put($('#content'), h('table', { class: 'files' },
      h('thead', {}, h('tr', {}, h('th', {}, 'When'), h('th', {}, 'Event'), h('th', {}, 'Link'), h('th', { class: 'r hide-s' }, 'From'))),
      h('tbody', {}, ev.map(e => h('tr', { onclick: () => nav({ view: 'shares', share: e.share, tab: 'all' }) },
        h('td', { class: 'num muted' }, fmtDate(e.ts)),
        h('td', {}, EVENT_TEXT[e.kind] || e.kind, e.detail ? h('span', { class: 'where' }, e.detail) : null),
        h('td', { class: 'mono' }, e.share),
        h('td', { class: 'r mono muted hide-s', title: e.ua }, e.ip))))));
    return;
  }
  const since = { '': 7, '1': 1, '30': 30, '180': 180 }[s.sort] || 7;
  const kind = s.kind;
  const rows = await api(`/api/changes?since=${Math.floor(Date.now() / 1000 - since * 86400)}&kind=${kind}&root=${encodeURIComponent(s.within)}&limit=1000`);
  if (seq !== routeSeq) return;
  const winSel = h('select', { 'aria-label': 'Time window', onchange: e => nav({ sort: e.target.value }, true) },
    [['1', 'Last 24 hours'], ['', 'Last 7 days'], ['30', 'Last 30 days'], ['180', 'Last 180 days']].map(([v, l]) => h('option', { value: v, selected: s.sort === v }, l)));
  const kindSel = h('select', { 'aria-label': 'Change type', onchange: e => nav({ kind: e.target.value }, true) },
    [['', 'All changes'], ['added', 'Added'], ['modified', 'Modified'], ['deleted', 'Deleted']].map(([v, l]) => h('option', { value: v, selected: kind === v }, l)));
  const within = h('select', { 'aria-label': 'Library', onchange: e => nav({ within: e.target.value }, true) },
    h('option', { value: '' }, 'All libraries'), LIBS.map(l => h('option', { value: l.path, selected: s.within === l.path }, l.name)));
  put($('#sub'), seg, winSel, kindSel, within, h('span', { class: 'grow' }), h('span', { class: 'timing num' }, `${rows.length.toLocaleString()}${rows.length >= 1000 ? '+' : ''} changes`));
  if (!rows.length) { put($('#content'), emptyState('No changes in this window.', 'Changes are recorded after the first full scan finishes.')); return; }
  const verb = { added: 'Added', modified: 'Modified', deleted: 'Deleted' };
  put($('#content'), h('table', { class: 'files' },
    h('thead', {}, h('tr', {}, h('th', {}, 'Item'), h('th', {}, 'Change'), h('th', { class: 'r' }, 'Size'), h('th', { class: 'r hide-s' }, 'When'))),
    h('tbody', {}, rows.map(c => h('tr', { onclick: () => c.kind !== 'deleted' && nav({ view: 'browse', path: parentOf(c.path), sel: c.path, tab: '', sort: '', kind: '', within: '' }) },
      h('td', { class: 'name' }, h('span', {}, icon(c.dir ? 'folder' : 'file', c.dir ? 'k-folder' : ''), h('span', { class: 'label' }, c.path.split('/').pop())), h('span', { class: 'where' }, parentOf(c.path))),
      h('td', { class: c.kind === 'deleted' ? 'err' : '' }, verb[c.kind]),
      h('td', { class: 'r num muted' }, c.dir || c.kind === 'deleted' ? '' : fmtSize(c.size)),
      h('td', { class: 'r num muted hide-s' }, fmtDate(c.ts)))))));
}

// ---------- overview ----------
const KIND_NAMES = { image: 'Images', video: 'Video', audio: 'Audio', doc: 'Documents', archive: 'Archives', code: 'Code', '3d': '3D models', other: 'Other' };
async function viewOverview(s, seq) {
  const st = await api('/api/stats');
  if (seq !== routeSeq) return;
  CURRENT = [];
  const ix = st.status;
  put($('#sub'), h('b', {}, 'Overview'), h('span', { class: 'grow' }),
    ME.admin ? h('button', { class: 'btn', onclick: async e => { await api('/api/rescan', { method: 'POST' }); e.currentTarget.textContent = 'Rescan started'; } }, 'Rescan now') : null);
  const sum = k => st.by_root.reduce((a, r) => a + r[k], 0); // non-admins: totals of the folders they can read
  const bars = (rows, label) => {
    const max = Math.max(1, ...rows.map(r => r.bytes));
    return h('div', { class: 'chart' }, rows.map(r => [
      h('span', {}, label(r)),
      h('div', { class: 'track' }, h('div', { class: 'fill', style: `inline-size:${(r.bytes / max * 100).toFixed(2)}%` })),
      h('span', { class: 'v' }, `${fmtSize(r.bytes)}, ${r.files.toLocaleString()} files`)]));
  };
  const days = st.changes_7d;
  const dmax = Math.max(1, ...days);
  const dayName = i => new Date(Date.now() - (6 - i) * 86400000).toLocaleDateString('en-US', { weekday: 'short' });
  put($('#content'), h('div', { class: 'page' },
    ME.admin ? h('div', { class: 'stats' },
      h('div', {}, h('b', {}, ix.files.toLocaleString()), 'files'),
      h('div', {}, h('b', {}, ix.dirs.toLocaleString()), 'folders'),
      h('div', {}, h('b', {}, fmtSize(ix.bytes)), 'indexed'),
      h('div', {}, h('b', {}, String(st.active_shares)), 'active links'))
      : h('div', { class: 'stats' },
        h('div', {}, h('b', {}, sum('files').toLocaleString()), 'files'),
        h('div', {}, h('b', {}, fmtSize(sum('bytes'))), 'in your folders'),
        h('div', {}, h('b', {}, String(st.active_shares)), 'active links')),
    ME.admin ? h('p', { class: 'muted' },
      ix.scanning ? `Scan running: ${ix.scanned_dirs.toLocaleString()} folders checked.` : `Last full scan ${fmtWhen(ix.last_end)}, took ${(ix.last_duration_ms / 1000).toFixed(1)} s.`,
      ' ', ix.watching ? `Live updates on (${ix.watches.toLocaleString()} folders watched).` : 'Live updates off; changes appear after each scan.',
      ix.watch_note ? h('span', { class: 'err' }, ' ' + ix.watch_note) : null,
      ix.error ? h('span', { class: 'err' }, ' Last scan error: ' + ix.error) : null)
      : h('p', { class: 'muted' }, ix.scanning ? 'Scan running.' : `Last full scan ${fmtWhen(ix.last_end)}.`),
    h('h2', {}, 'Size by library'), st.by_root.length ? bars(st.by_root, r => r.name) : h('p', { class: 'muted' }, ME.admin ? 'No libraries enabled.' : 'No folders are shared with you yet.'),
    h('h2', {}, 'Size by type'), bars(st.by_kind, r => KIND_NAMES[r.name] || r.name),
    h('h2', {}, 'Changes per day'),
    h('div', { class: 'cols', role: 'img', 'aria-label': 'Changes per day, last 7 days: ' + days.join(', ') },
      days.map((n, i) => h('div', { class: 'c' }, h('span', { class: 'n' }, n.toLocaleString()), h('div', { class: 'b', style: `block-size:${(n / dmax * 100).toFixed(1)}%` }), h('span', {}, dayName(i)))))));
}

// ---------- users (admins only) ----------
const ACCESS = [['read', 'View'], ['share', 'View and share'], ['write', 'View and edit'], ['all', 'View, edit and share'], ['none', 'No access']];
const grantAccess = g => !g.read ? 'none' : g.write && g.share ? 'all' : g.write ? 'write' : g.share ? 'share' : 'read';
function accessSummary(u) {
  if (u.admin) return 'Every folder';
  const open = u.grants.filter(g => g.read).map(g => g.path);
  return open.length ? open.join(', ') : 'No folders yet';
}

async function viewUsers(s, seq) {
  const users = await api('/api/users');
  if (seq !== routeSeq) return;
  CURRENT = [];
  put($('#sub'), h('b', {}, 'Users'), h('span', { class: 'grow' }),
    h('button', { class: 'btn', onclick: () => nav({ tab: 'newuser', user: '' }) }, icon('user'), 'New user'));
  put($('#content'), h('table', { class: 'files' },
    h('thead', {}, h('tr', {}, h('th', {}, 'User'), h('th', {}, 'Role'), h('th', { class: 'hide-s' }, 'Two-factor'), h('th', {}, 'State'))),
    h('tbody', {}, users.map(u => h('tr', { 'aria-selected': s.user === String(u.id) ? 'true' : 'false', onclick: () => nav({ user: String(u.id), tab: '' }, true) },
      h('td', { class: 'name' }, h('span', {}, icon('user'), h('span', { class: 'label' }, u.username)), h('span', { class: 'where' }, accessSummary(u))),
      h('td', {}, u.admin ? 'Admin' : 'User'),
      h('td', { class: 'hide-s muted' }, u.two_factor ? 'On' : 'Off'),
      h('td', {}, h('span', { class: 'state ' + (u.disabled ? 'revoked' : 'active') }, u.disabled ? 'Turned off' : 'Active')))))));
}

// folderSuggest fills a datalist with folders matching q (the libraries when q is empty).
async function folderSuggest(options, q) {
  try {
    const paths = q ? (await api('/api/search?' + new URLSearchParams({ q, kind: 'folder', limit: 20 }))).entries.map(e => e.path) : LIBS.map(l => l.path);
    options.replaceChildren(...paths.map(p => h('option', { value: p })));
  } catch { /* suggestions are optional */ }
}

// grantsEditor edits a user's folder access: one row per folder, most specific wins.
function grantsEditor(grants) {
  const list = h('div', { class: 'grants' });
  const options = h('datalist', { id: 'folder-options' });
  let t = null;
  const suggest = q => folderSuggest(options, q);
  const addRow = (g = { path: '', read: true, share: false }) => {
    const path = h('input', { value: g.path, list: 'folder-options', class: 'mono', placeholder: 'Folder, e.g. DOCUMENTS/Taxes', 'aria-label': 'Folder', autocomplete: 'off', spellcheck: 'false',
      oninput: e => { clearTimeout(t); t = setTimeout(() => suggest(e.target.value.trim()), 150); }, onfocus: e => suggest(e.target.value.trim()) });
    const access = h('select', { 'aria-label': 'Access' }, ACCESS.map(([v, l]) => h('option', { value: v }, l)));
    access.value = grantAccess(g);
    const note = h('span', { class: 'note' });
    // edit only works where the folder is mounted writable; say so next to the choice
    const check = async () => {
      note.textContent = '';
      const p = path.value.trim();
      if (!p || !['write', 'all'].includes(access.value)) return;
      try { if (!(await api('/api/ls?path=' + encodeURIComponent(p), { quiet: true })).can_write) note.textContent = 'Mounted read-only, so edits won\u2019t work yet'; } catch { /* not a folder yet */ }
    };
    access.addEventListener('change', check);
    path.addEventListener('change', check);
    check();
    const row = h('div', { class: 'grant' }, path, access, h('button', { type: 'button', class: 'iconbtn', 'aria-label': 'Remove folder', title: 'Remove', onclick: () => row.remove() }, icon('x')), note);
    list.append(row);
    return path;
  };
  grants.forEach(g => addRow(g));
  const read = () => [...list.querySelectorAll('.grant')].map(r => {
    const a = r.querySelector('select').value;
    return { path: r.querySelector('input').value.trim(), read: a !== 'none', share: a === 'share' || a === 'all', write: a === 'write' || a === 'all' };
  }).filter(g => g.path);
  const el = h('div', { class: 'field' },
    h('span', {}, 'Folder access', info('Each folder includes everything inside it. The most specific folder wins, so No access on a subfolder hides it inside a folder they can view. Edit (upload, new folders, rename, delete to trash) also needs the folder mounted writable. Links a user made stop working if they lose share access.')),
    list, options,
    h('div', { class: 'row' }, h('button', { type: 'button', class: 'btn', onclick: () => addRow().focus() }, 'Add folder')));
  return { el, read };
}

async function openUserPanel(id) {
  const isNew = !id;
  let u = { username: '', admin: false, disabled: false, two_factor: false, grants: [] };
  if (!isNew) {
    try { u = (await api('/api/users')).find(x => String(x.id) === String(id)); } catch { u = null; }
    if (!u) { panelShell('User not found', h('p', { class: 'muted' }, 'It may have been deleted.')); return; }
  }
  const err = h('p', { class: 'err' });
  const saved = h('span', { class: 'saved' });
  const grants = grantsEditor(u.grants);
  const role = h('select', { name: 'role' }, h('option', { value: 'user' }, 'User'), h('option', { value: 'admin' }, 'Admin'));
  role.value = u.admin ? 'admin' : 'user';
  const grantsBox = h('div', { hidden: u.admin }, grants.el);
  role.addEventListener('change', () => { grantsBox.hidden = role.value === 'admin'; });
  const f = h('form', { class: 'stack' },
    isNew ? h('label', { class: 'field' }, h('span', {}, 'Username'), h('input', { name: 'username', required: true, autocomplete: 'off', spellcheck: 'false' })) : null,
    h('label', { class: 'field' }, h('span', {}, isNew ? 'Password' : 'New password', info('At least 10 characters. Setting a new one signs the user out everywhere.')),
      h('input', { name: 'pw', type: 'password', autocomplete: 'new-password', minlength: 10, required: isNew, placeholder: isNew ? '' : 'Unchanged' })),
    h('label', { class: 'field' }, h('span', {}, 'Role', info('Admins see every folder and manage users and settings.')), role),
    grantsBox,
    isNew ? null : h('div', { class: 'checks' }, h('label', {}, h('input', { type: 'checkbox', name: 'off', checked: u.disabled }), 'Account turned off')),
    err,
    h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'submit' }, isNew ? 'Create user' : 'Save changes'), saved));
  f.addEventListener('submit', async ev => {
    ev.preventDefault();
    err.textContent = ''; saved.textContent = '';
    const body = { admin: role.value === 'admin', grants: grants.read() };
    try {
      if (isNew) {
        const nu = await api('/api/users', { method: 'POST', body: { ...body, username: f.username.value.trim(), password: f.pw.value } });
        nav({ tab: '', user: String(nu.id) });
        return;
      }
      if (f.pw.value) body.password = f.pw.value;
      body.disabled = f.off.checked;
      await api('/api/users/' + u.id, { method: 'PATCH', body });
      f.pw.value = '';
      saved.textContent = 'Saved ' + new Date().toLocaleTimeString('en-US', { hour12: false });
      viewUsers(getState(), routeSeq);
    } catch (x) { err.textContent = x.message; }
  });
  const actErr = h('p', { class: 'err' });
  const account = isNew ? null : [
    h('h3', {}, 'Account'),
    h('dl', { class: 'facts' }, h('dt', {}, 'Created'), h('dd', {}, fullDate(u.created)), h('dt', {}, 'Two-factor'), h('dd', {}, u.two_factor ? 'On' : 'Off')),
    h('div', { class: 'row' },
      u.two_factor ? h('button', { class: 'btn', type: 'button', onclick: async () => {
        try { await api('/api/users/' + u.id, { method: 'PATCH', body: { reset_2fa: true } }); openUserPanel(id); viewUsers(getState(), routeSeq); } catch (x) { actErr.textContent = x.message; }
      } }, 'Turn off two-factor') : null,
      u.username !== ME.user ? h('button', { class: 'btn danger', type: 'button', onclick: async ev => {
        const b = ev.currentTarget;
        if (!b.dataset.armed) { b.dataset.armed = '1'; b.textContent = `Delete ${u.username}`; return; }
        try { await api('/api/users/' + u.id, { method: 'DELETE' }); nav({ user: '' }); } catch (x) { actErr.textContent = x.message; }
      } }, 'Delete user') : null,
      u.username !== ME.user ? info('Deleting turns off every link this user made. Files are never touched.') : null),
    actErr,
    h('h3', {}, 'Phones'),
    devicesList(true, d => d.user_id === u.id)];
  panelShell(isNew ? 'New user' : u.username, f, account);
  if (isNew) f.username.focus();
}

// ---------- API tokens ----------
// Each token offers a chosen set of MCP tools (each Off, Ask or On), in every folder
// the account can see or only in some, and the REST API only if scripts need it.
const TOOL_GROUPS = [['read', 'Read'], ['share', 'Share'], ['edit', 'Edit'], ['admin', 'Admin']];
const TOOL_MODES = [['off', 'Off'], ['ask', 'Ask'], ['on', 'On']];
const TOKEN_PRESETS = [['Read only', ['read']], ['Read and share', ['read', 'share']], ['Everything', ['read', 'share', 'edit', 'admin']], ['Nothing', []]];
const toolMode = (sc, name) => sc.tools[name] || 'off';

function scopeSummary(sc, tools) {
  const on = tools.filter(t => toolMode(sc, t.name) !== 'off');
  if (!on.length) return 'Nothing';
  const preset = TOKEN_PRESETS.find(([, gs]) => tools.every(t => gs.includes(t.group) === (toolMode(sc, t.name) !== 'off')));
  const asks = on.filter(t => toolMode(sc, t.name) === 'ask').length;
  return [preset ? preset[0] : `${on.length} of ${tools.length} tools`,
    sc.folders.length ? sc.folders.join(', ') : 'every folder',
    asks === on.length ? 'asks before every call' : asks ? 'asks before some calls' : 'doesn’t ask',
    sc.api ? 'REST API too' : null].filter(Boolean).join(' · ');
}

// folderListEditor edits a list of folders, with the same suggestions as folder access.
function folderListEditor(paths) {
  const list = h('div', { class: 'grants' });
  const options = h('datalist', { id: 'token-folder-options' });
  let t = null;
  const add = (p = '') => {
    const input = h('input', { value: p, list: 'token-folder-options', class: 'mono', placeholder: 'Folder, e.g. DOCUMENTS/Taxes', 'aria-label': 'Folder', autocomplete: 'off', spellcheck: 'false',
      oninput: e => { clearTimeout(t); t = setTimeout(() => folderSuggest(options, e.target.value.trim()), 150); }, onfocus: e => folderSuggest(options, e.target.value.trim()) });
    const row = h('div', { class: 'grant folder' }, input, h('button', { type: 'button', class: 'iconbtn', 'aria-label': 'Remove folder', title: 'Remove', onclick: () => row.remove() }, icon('x')));
    list.append(row);
    return input;
  };
  paths.forEach(p => add(p));
  const read = () => [...list.querySelectorAll('input')].map(i => i.value.trim()).filter(Boolean);
  const el = h('div', { class: 'stack' }, list, options, h('div', { class: 'row' }, h('button', { type: 'button', class: 'btn', onclick: () => add().focus() }, 'Add folder')));
  return { el, read, add };
}

function tokenPanel(tok, tools, mcpURL, changed) {
  const isNew = !tok;
  const sc = isNew ? { tools: {}, folders: [], api: false } : { ...tok.scope, tools: { ...tok.scope.tools } };
  if (isNew) tools.forEach(t => { if (t.group === 'read' || t.group === 'share') sc.tools[t.name] = 'ask'; });
  const err = h('p', { class: 'err' });
  const saved = h('span', { class: 'saved' });
  const name = h('input', { name: 'name', value: isNew ? '' : tok.name, placeholder: 'e.g. Claude Code', required: true, autocomplete: 'off' });

  const redraw = {};
  const modeSeg = t => {
    const el = h('div', { class: 'seg', role: 'group', 'aria-label': t.name });
    redraw[t.name] = () => put(el, TOOL_MODES.map(([v, l]) => h('button', { type: 'button', 'aria-pressed': toolMode(sc, t.name) === v ? 'true' : 'false',
      onclick: () => { sc.tools[t.name] = v; redraw[t.name](); } }, l)));
    redraw[t.name]();
    return el;
  };
  const presets = h('div', { class: 'row' }, TOKEN_PRESETS.map(([label, gs]) => h('button', { type: 'button', class: 'btn',
    onclick: () => tools.forEach(t => { sc.tools[t.name] = gs.includes(t.group) ? 'ask' : 'off'; redraw[t.name](); }) }, label)));
  const toolList = h('div', { class: 'tools' }, TOOL_GROUPS.filter(([g]) => tools.some(t => t.group === g)).map(([g, label]) => [
    h('h3', {}, label),
    tools.filter(t => t.group === g).map(t => h('div', { class: 'tool' },
      h('div', {}, h('div', { class: 'mono' }, t.name), h('div', { class: 'muted' }, t.description.split(/(?<=\.) /)[0])), modeSeg(t)))]));

  const folders = folderListEditor(sc.folders);
  const whereAll = h('input', { type: 'radio', name: 'where', value: 'all', checked: !sc.folders.length });
  const whereSome = h('input', { type: 'radio', name: 'where', value: 'some', checked: sc.folders.length > 0 });
  const folderBox = h('div', { hidden: !sc.folders.length }, folders.el);
  [whereAll, whereSome].forEach(r => r.addEventListener('change', () => {
    folderBox.hidden = whereAll.checked;
    if (whereSome.checked && !folders.read().length) folders.add().focus();
  }));
  const apiBox = h('input', { type: 'checkbox', name: 'api', checked: sc.api });

  const f = h('form', { class: 'stack' },
    h('label', { class: 'field' }, h('span', {}, 'Name'), name),
    h('div', { class: 'field' },
      h('span', {}, 'Tools', info('Ask: Claude Code asks you before every call, in every permission mode, and offers no “don’t ask again”. Other MCP clients may treat it like On. On: the client’s own permission settings decide. Off: the token doesn’t offer the tool at all.')),
      presets, toolList),
    h('div', { class: 'field' },
      h('span', {}, 'Folders', info('A token never reaches further than your account. Limited to some folders, it searches, reads, shares and edits only inside them, and sees only the links that point there.')),
      h('div', { class: 'checks' }, h('label', {}, whereAll, 'Every folder your account can see'), h('label', {}, whereSome, 'Only these folders')),
      folderBox),
    h('div', { class: 'field' }, h('span', {}, 'REST API', info('Off: the token works only at the MCP endpoint. On: it also works for the REST API, with the same folders, and with sharing and editing only if it offers a share or edit tool.')),
      h('div', { class: 'checks' }, h('label', {}, apiBox, 'Scripts can also use this token on the REST API'))),
    err,
    h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'submit' }, isNew ? 'Create token' : 'Save changes'), saved));
  f.addEventListener('submit', async ev => {
    ev.preventDefault();
    err.textContent = ''; saved.textContent = '';
    const scope = { tools: sc.tools, folders: whereSome.checked ? folders.read() : [], api: apiBox.checked };
    try {
      if (isNew) {
        const t = await api('/api/tokens', { method: 'POST', body: { name: name.value.trim(), scope } });
        changed();
        panelShell(t.name, tokenCreated(t));
        return;
      }
      await api('/api/tokens/' + tok.id, { method: 'PATCH', body: { name: name.value.trim(), scope } });
      saved.textContent = 'Saved ' + new Date().toLocaleTimeString('en-US', { hour12: false });
      changed();
    } catch (x) { err.textContent = x.message; }
  });
  panelShell(isNew ? 'New token' : tok.name, f);
  if (isNew) name.focus();
}

// tokenCreated shows a new token once, with a ready-made client config.
function tokenCreated(t) {
  const cfg = JSON.stringify({ mcpServers: { burrow: { type: 'http', url: t.mcp_url, headers: { Authorization: 'Bearer ' + t.token } } } }, null, 2);
  return [
    h('p', {}, h('b', {}, 'Copy this token now. It won’t be shown again.')),
    h('div', { class: 'secret' }, t.token),
    h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: ev => copy(t.token, ev.currentTarget) }, 'Copy token')),
    h('h3', {}, 'Claude Code'),
    h('p', {}, 'Install the Burrow plugin, then paste the MCP endpoint and this token when it asks:'),
    h('pre', { class: 'code' }, '/plugin install burrow --marketplace xattribution/burrow'),
    h('dl', { class: 'facts' }, h('dt', {}, 'MCP endpoint'), h('dd', { class: 'mono' }, t.mcp_url)),
    h('h3', {}, 'Other MCP clients'),
    h('pre', { class: 'code' }, cfg),
    h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: ev => copy(cfg, ev.currentTarget) }, 'Copy config'))];
}

function tokenList(initial, tools, mcpURL) {
  const box = h('div', { class: 'stack' });
  const reload = async () => draw(await api('/api/tokens'));
  const draw = toks => put(box,
    toks.length ? h('table', { class: 'files' }, h('tbody', {}, toks.map(t => h('tr', {},
      h('td', { class: 'name' }, h('span', { class: 'label' }, t.name), h('span', { class: 'where' }, scopeSummary(t.scope, tools))),
      h('td', { class: 'muted hide-s' }, t.last_used ? `Used ${fmtWhen(t.last_used)}${t.last_ip ? ' from ' + t.last_ip : ''}` : 'Never used'),
      h('td', { class: 'r' }, h('div', { class: 'row end' },
        h('button', { class: 'btn', type: 'button', onclick: () => tokenPanel(t, tools, mcpURL, reload) }, 'Edit'),
        h('button', { class: 'btn danger', type: 'button', onclick: async () => { await api('/api/tokens/' + t.id, { method: 'DELETE' }); closePanel(false); reload(); } }, 'Delete'))))))) : null,
    h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: () => tokenPanel(null, tools, mcpURL, reload) }, icon('plus'), 'New token')));
  draw(initial);
  return box;
}

// ---------- settings ----------
async function viewSettings(s, seq) {
  SETTINGS = null;
  const st = await settings();
  const [tokens, tools] = await Promise.all([api('/api/tokens'), api('/api/tokens/tools')]);
  if (seq !== routeSeq) return;
  CURRENT = [];
  put($('#sub'), h('b', {}, 'Settings'));
  const section = (title, help, body, save) => {
    const saved = h('span', { class: 'saved' });
    const err = h('span', { class: 'err' });
    const f = h('form', { class: 'stack' }, body, save ? h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'submit' }, 'Save'), saved, err) : null);
    f.addEventListener('submit', e => e.preventDefault());
    if (save) f.addEventListener('submit', async e => {
      e.preventDefault(); err.textContent = ''; saved.textContent = '';
      try { await save(f); SETTINGS = null; saved.textContent = 'Saved ' + new Date().toLocaleTimeString('en-US', { hour12: false }); loadNav(); } catch (x) { err.textContent = x.message; }
    });
    return [h('h2', {}, title, help ? [' ', info(help)] : null), f];
  };
  const themeSel = h('select', { onchange: e => setTheme(e.target.value) }, [['', 'Match system'], ['light', 'Light'], ['dark', 'Dark']].map(([v, l]) => h('option', { value: v, selected: (localGet('theme') || '') === v }, l)));
  const mcpURL = (st.public_url || location.origin) + '/mcp';
  const admin = st.admin;
  put($('#content'), h('div', { class: 'page' },
    admin ? [section('Public address', 'Share links and QR codes are built from this address.',
      h('input', { name: 'url', value: st.public_url, placeholder: location.origin, inputmode: 'url' }),
      f => api('/api/settings', { method: 'PUT', body: { public_url: f.url.value } })),
    section('Home address for phones', 'Paired phones use this address on your home network and the public address everywhere else. Leave it empty to use the address of this browser tab when you pair.',
      h('input', { name: 'lan', value: st.lan_url, placeholder: location.origin, inputmode: 'url' }),
      f => api('/api/settings', { method: 'PUT', body: { lan_url: f.lan.value } })),
    section('Public port', `Port ${st.public_addr || '(off)'} is the one to forward from your reverse proxy. It always serves share links, and nothing else unless you turn it on here.`,
      h('div', { class: 'checks' },
        h('label', {}, h('input', { type: 'checkbox', name: 'pui', checked: st.public_ui }), 'Also serve the web UI and its API'),
        h('label', {}, h('input', { type: 'checkbox', name: 'pmcp', checked: st.public_mcp }), 'Also serve MCP (still needs an API token)')),
      f => api('/api/settings', { method: 'PUT', body: { public_ui: f.pui.checked, public_mcp: f.pmcp.checked } })),
    section('Reach from anywhere', 'Paired phones reach Burrow away from home over tethersys, with no port forwarded on your router: directly when the two networks allow it, through an encrypted relay when they don\u2019t. It carries what the public port serves (the phone app and share links), still end-to-end encrypted, and only for phones paired here. Removing a phone cuts it off.',
      [h('div', { class: 'checks' }, h('label', {}, h('input', { type: 'checkbox', name: 'tether', checked: st.tether }), 'Let paired phones reach Burrow without a port forward')), tetherStatus()],
      f => api('/api/settings', { method: 'PUT', body: { tether: f.tether.checked } }).then(() => setTimeout(() => viewSettingsTether(), 1500))),
    section('Libraries', `Top-level folders inside ${st.data_dir}. Only checked folders can be searched, browsed or shared.`,
      h('div', { class: 'checks' }, st.roots.map(r => h('label', {}, h('input', { type: 'checkbox', name: 'root', value: r.name, checked: r.enabled }), r.name))),
      f => api('/api/settings', { method: 'PUT', body: { roots: [...f.querySelectorAll('input[name=root]:checked')].map(i => i.value) } })),
    section('Connected NAS', 'Other Burrow servers and SMB shares this Burrow reaches for you. Each shows up as a library; give people access to it in Users like any folder. Passwords and tokens are stored encrypted and never shown again. Search and share links cover this NAS only, for now.',
      remotesEditor(), null),
    section('Hidden names', 'One name or pattern per line, matched against each file and folder name. * and ? work as wildcards. Matching items are skipped everywhere.',
      h('textarea', { name: 'ex', rows: 6, class: 'mono' }, st.excludes),
      f => api('/api/settings', { method: 'PUT', body: { excludes: f.ex.value } })),
    section('Link defaults', null,
      h('div', { class: 'grid2' },
        h('label', { class: 'field' }, h('span', {}, 'Default expiry (hours)', info('Used when a link is created without choosing an expiry. 0 means links never expire by default.')), h('input', { name: 'exp', type: 'number', min: 0, value: st.default_expiry_hours })),
        h('label', { class: 'field' }, h('span', {}, 'File drop size limit (MB per file)'), h('input', { name: 'mb', type: 'number', min: 1, value: st.max_upload_mb }))),
      f => api('/api/settings', { method: 'PUT', body: { default_expiry_hours: Number(f.exp.value), max_upload_mb: Number(f.mb.value) } })),
    section('Admin access', 'Optional. Comma-separated IPs or CIDR ranges allowed to open this admin page and the API, e.g. 192.168.1.0/24, 100.64.0.0/10. Share links keep working from anywhere. Leave empty to allow any network.',
      h('input', { name: 'cidrs', value: st.admin_cidrs, placeholder: 'Any network', class: 'mono' }),
      f => api('/api/settings', { method: 'PUT', body: { admin_cidrs: f.cidrs.value } }))] : null,
    section('Phones', 'The Burrow app browses, shares, backs up and syncs your files. A paired phone acts as you, with your folder access, and talks to Burrow end-to-end encrypted, so even your reverse proxy only sees scrambled data. Remove a phone to cut it off at once.',
      (() => { const devs = devicesList(false); return [devs, pairPhone(() => devs.reload())]; })(), null),
    section('MCP and API tokens', 'Tokens let an LLM client (or a script) work with your files through the MCP endpoint. A token reaches no further than your account, and only as far as you set: which tools, which folders, and whether Claude Code asks you before each call. Tokens can\u2019t change account settings. Delete one to cut its access.',
      [h('dl', { class: 'facts' }, h('dt', {}, 'MCP endpoint'), h('dd', { class: 'mono' }, mcpURL)), tokenList(tokens, tools, mcpURL)], null),
    section('Password', null,
      h('div', { class: 'grid2' },
        h('label', { class: 'field' }, h('span', {}, 'Current password'), h('input', { name: 'cur', type: 'password', autocomplete: 'current-password', required: true })),
        h('label', { class: 'field' }, h('span', {}, 'New password'), h('input', { name: 'nw', type: 'password', autocomplete: 'new-password', minlength: 10, required: true }))),
      async f => { await api('/api/password', { method: 'POST', body: { current: f.cur.value, new: f.nw.value } }); f.reset(); }),
    twoFactorSection(st),
    section('Appearance', null, h('label', { class: 'field inline' }, h('span', {}, 'Theme'), themeSel), null),
    h('h2', {}, 'Account'),
    h('p', {}, `Signed in as ${st.user}${admin ? ', an admin' : ''}.${admin ? ` Index rescans every ${st.scan_hours} hours.` : ''} Version ${ME.version}.`),
    h('button', { class: 'btn', onclick: async () => { await api('/api/logout', { method: 'POST' }); boot(); } }, 'Sign out')));
}

function twoFactorSection(st) {
  const box = h('div', { class: 'stack' });
  const err = h('p', { class: 'err' });
  if (st.two_factor) {
    const f = h('form', { class: 'stack' },
      h('p', {}, `On. ${st.recovery_left} recovery code${st.recovery_left === 1 ? '' : 's'} left.`),
      h('div', { class: 'grid2' },
        h('label', { class: 'field' }, h('span', {}, 'Password'), h('input', { name: 'pw', type: 'password', autocomplete: 'current-password', required: true })),
        h('label', { class: 'field' }, h('span', {}, 'Current code'), h('input', { name: 'code', autocomplete: 'one-time-code', inputmode: 'numeric', class: 'mono', required: true }))),
      err, h('div', { class: 'row' }, h('button', { class: 'btn danger', type: 'submit' }, 'Turn off two-factor sign-in')));
    f.addEventListener('submit', async e => {
      e.preventDefault();
      try { await api('/api/2fa/disable', { method: 'POST', body: { password: f.pw.value, code: f.code.value } }); route(); } catch (x) { err.textContent = x.message; }
    });
    box.append(f);
  } else {
    box.append(h('p', {}, 'Off.'), h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: async () => {
      const d = await api('/api/2fa/start', { method: 'POST' });
      const f = h('form', { class: 'stack' },
        h('p', {}, 'Scan this with an authenticator app (Aegis, 1Password, Google Authenticator), then enter the code it shows.'),
        h('div', { class: 'qr' }, h('img', { src: d.qr, alt: 'Authenticator setup QR code' })),
        h('details', {}, h('summary', {}, 'Enter the key by hand'), h('div', { class: 'secret' }, d.secret)),
        h('div', { class: 'grid2' },
          h('label', { class: 'field' }, h('span', {}, 'Code from the app'), h('input', { name: 'code', autocomplete: 'one-time-code', inputmode: 'numeric', class: 'mono', required: true, maxlength: 6 })),
          h('label', { class: 'field' }, h('span', {}, 'Your password'), h('input', { name: 'pw', type: 'password', autocomplete: 'current-password', required: true }))),
        err, h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'submit' }, 'Turn on')));
      f.addEventListener('submit', async e => {
        e.preventDefault();
        try {
          const r = await api('/api/2fa/enable', { method: 'POST', body: { code: f.code.value, password: f.pw.value } });
          const txt = r.recovery_codes.join('\n');
          box.replaceChildren(
            h('p', {}, h('b', {}, 'Two-factor sign-in is on. Save these recovery codes now. Each works once if you lose your phone, and they won’t be shown again.')),
            h('pre', { class: 'code' }, txt),
            h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: ev => copy(txt, ev.currentTarget) }, 'Copy codes'),
              h('button', { class: 'btn', type: 'button', onclick: () => route() }, 'Done')));
        } catch (x) { err.textContent = x.message; }
      });
      box.replaceChildren(f);
      f.code.focus();
    } }, 'Set up two-factor sign-in')));
  }
  return [h('h2', {}, 'Two-factor sign-in', ' ', info('After your password, sign-in asks for a code from an authenticator app. API tokens for MCP are not affected; delete a token to cut its access.')), box];
}

function localGet(k) { try { return localStorage.getItem('burrow.' + k); } catch { return null; } }
function localSet(k, v) { try { if (v) localStorage.setItem('burrow.' + k, v); else localStorage.removeItem('burrow.' + k); } catch { /* private mode */ } }
function setTheme(v) { localSet('theme', v); if (v) document.documentElement.dataset.theme = v; else delete document.documentElement.dataset.theme; }
setTheme(localGet('theme') || '');

boot();

// ---------- phones ----------
function devicesList(all, keep = () => true) {
  const box = h('div', { class: 'stack' }, h('p', { class: 'muted' }, 'Loading…'));
  const load = async () => {
    let ds;
    try { ds = (await api('/api/devices' + (all ? '?all=1' : ''))).filter(keep); } catch (x) { put(box, h('p', { class: 'err' }, x.message)); return; }
    if (!ds.length) { put(box, h('p', { class: 'muted' }, 'No phones paired.')); return; }
    put(box, h('table', { class: 'files' }, h('tbody', {}, ds.map(d => h('tr', {},
      h('td', { class: 'name' }, h('span', {}, icon('phone'), h('span', { class: 'label' }, d.name)), h('span', { class: 'where' }, [d.model, d.app_version && 'app ' + d.app_version].filter(Boolean).join(', ') || d.platform)),
      h('td', { class: 'muted' }, d.last_seen ? `${fmtWhen(d.last_seen)}, ${{ public: 'away', tether: 'over tethersys' }[d.last_via] || 'at home'}` : 'Not yet', d.last_ip && d.last_via !== 'tether' ? h('span', { class: 'where' }, d.last_ip) : null),
      h('td', { class: 'muted hide-s' }, 'Paired ' + fmtDate(d.created)),
      h('td', { class: 'r' }, h('button', { class: 'btn danger', type: 'button', onclick: async ev => {
        const b = ev.currentTarget;
        if (!b.dataset.armed) { b.dataset.armed = '1'; b.textContent = 'Remove ' + d.name; return; }
        try { await api('/api/devices/' + d.id, { method: 'DELETE' }); load(); } catch (x) { b.textContent = x.message; }
      } }, 'Remove')))))));
  };
  load();
  box.reload = load;
  return box;
}

function pairPhone(onPaired) {
  const box = h('div', { class: 'stack' });
  const start = async () => {
    let d;
    try { d = await api('/api/devices/pair', { method: 'POST' }); } catch (x) { put(box, h('p', { class: 'err' }, x.message), startBtn); return; }
    const left = h('b', { class: 'num' });
    const status = h('p', {}, 'Waiting for the phone…');
    put(box,
      h('p', {}, 'In the Burrow app, tap ', h('b', {}, 'Pair with Burrow'), ' and scan this code. It works once and expires in ', left, '.'),
      h('div', { class: 'qr' }, h('img', { src: d.qr, alt: 'Pairing QR code' })),
      h('dl', { class: 'facts' },
        h('dt', {}, 'At home'), h('dd', { class: 'mono' }, d.lan || 'Not set (the phone will only use the public address)'),
        h('dt', {}, 'Away'), h('dd', { class: 'mono' }, d.public || 'Not set (the phone will only work at home)')),
      h('details', {}, h('summary', {}, 'Can’t scan?'),
        h('p', { class: 'muted' }, 'Open this link on the phone itself. It holds a one-time secret: don’t send it through chat or email.'),
        h('div', { class: 'secret' }, d.link),
        h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: ev => copy(d.link, ev.currentTarget) }, 'Copy link'))),
      status,
      h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: async () => {
        stop = true; await api('/api/devices/pair/' + d.id, { method: 'DELETE' }).catch(() => {}); put(box, startBtn);
      } }, 'Cancel')));
    let stop = false;
    const tick = async () => {
      if (stop || !box.isConnected) return;
      const secs = Math.max(0, Math.round(d.expires - Date.now() / 1000));
      left.textContent = `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}`;
      let st;
      try { st = await api('/api/devices/pair/' + d.id, { quiet: true }); } catch { st = { status: 'waiting' }; }
      if (st.status === 'paired') {
        put(box, h('p', {}, h('b', {}, `Paired with ${st.device.name}.`), ' It can now reach Burrow at home and away.'), startBtn);
        onPaired?.();
        return;
      }
      if (st.status === 'expired' || secs === 0) { put(box, h('p', { class: 'muted' }, 'That code expired.'), startBtn); return; }
      setTimeout(tick, 2000);
    };
    tick();
  };
  const startBtn = h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: start }, icon('phone'), 'Pair a phone'));
  put(box, startBtn);
  return box;
}

// ---------- changing files ----------
const CHUNK = 8 << 20;
function uploadButton(dir) {
  const input = h('input', { type: 'file', multiple: true, hidden: true, onchange: () => { if (input.files.length) uploadFiles(dir, [...input.files]); input.value = ''; } });
  return h('span', {}, input, h('button', { class: 'btn', type: 'button', onclick: () => input.click() }, icon('upload'), 'Upload'));
}

function uploadTray() {
  let t = $('#tray');
  if (!t) {
    t = h('div', { class: 'tray', id: 'tray', role: 'status' },
      h('div', { class: 'row' }, h('b', {}, 'Transfers'), h('span', { class: 'grow' }), h('button', { class: 'iconbtn', 'aria-label': 'Close', onclick: () => t.remove() }, icon('x'))),
      h('ul', {}));
    document.body.append(t);
  }
  return $('ul', t);
}

// files are File objects for dir itself, or { f, dir } for files inside dropped folders
async function uploadFiles(dir, files) {
  const list = uploadTray();
  const rows = files.map(x => {
    const f = x instanceof File ? x : x.f;
    const d = x instanceof File ? dir : x.dir;
    const bar = h('progress', { max: f.size || 1, value: 0 });
    const st = h('span', { class: 'muted' }, 'Waiting');
    list.append(h('li', {}, h('span', { class: 'label' }, d === dir ? f.name : d.slice(dir.length + 1) + '/' + f.name), bar, st));
    return { f, dir: d, bar, st };
  });
  for (const { f, dir, bar, st } of rows) {
    try {
      st.textContent = 'Sending';
      const up = await api('/api/uploads', { method: 'POST', body: { dir, name: f.name, size: f.size, mtime: f.lastModified } });
      let off = 0, tries = 0;
      while (off < f.size) {
        try {
          const r = await fetch(`/api/uploads/${up.id}?offset=${off}`, { method: 'PUT', headers: { 'X-Burrow': '1' }, body: f.slice(off, Math.min(off + CHUNK, f.size)) });
          const d = await r.json().catch(() => ({}));
          if (r.status === 409 && d.offset != null) { off = d.offset; continue; }
          if (!r.ok) throw Object.assign(new Error(d.error || `Upload failed (${r.status})`), { final: true });
          off = d.offset; bar.value = off; tries = 0;
        } catch (x) {
          // a dropped connection: ask where the upload got to and carry on
          if (x.final || ++tries > 5) throw x;
          await new Promise(r => setTimeout(r, 1500 * tries));
          off = (await api('/api/uploads/' + up.id)).offset;
        }
      }
      const done = await api(`/api/uploads/${up.id}/finish`, { method: 'POST', body: {} });
      bar.value = f.size || 1;
      st.textContent = done.name !== f.name ? `Saved as ${done.name}` : 'Done';
    } catch (x) { st.textContent = x.message; st.className = 'err'; }
  }
  route();
}

// ---------- drag and drop ----------
// Anything with data-drop="<folder>" takes drops: the folder you're in, folder
// rows and the path bar. Files from the computer are uploaded there; rows
// dragged inside Burrow are moved there; a file dragged out of the window to
// the desktop is downloaded (Chrome and Edge).
const MOVE_TYPE = 'application/x-burrow-path';
let dragging = null;
let marked = null;

function dragOut(ev, e) {
  dragging = e;
  ev.currentTarget.classList.add('dragging');
  const dt = ev.dataTransfer;
  dt.effectAllowed = 'copyMove';
  dt.setData(MOVE_TYPE, e.path);
  dt.setData('text/plain', e.name);
  if (!e.dir) dt.setData('DownloadURL', `application/octet-stream:${e.name.replaceAll(':', '_')}:${location.origin}/api/raw?dl=1&path=${encodeURIComponent(e.path)}`);
}

function dropKind(dt) {
  const types = [...(dt?.types || [])];
  if (types.includes(MOVE_TYPE)) return 'move'; // checked first: a dragged thumbnail also carries Files
  if (types.includes('Files')) return 'upload';
  return null;
}

function canMove(src, dest) { return !src || (dest !== parentOf(src) && dest !== src && !dest.startsWith(src + '/')); }

function markDrop(el, text) {
  if (marked !== el) {
    clearDropMark();
    marked = el;
    el.classList.add('drop-on');
  }
  let hint = $('#drophint');
  if (!hint) document.body.append(hint = h('div', { id: 'drophint', class: 'drophint', role: 'status' }));
  hint.textContent = text;
}

function clearDropMark() {
  marked?.classList.remove('drop-on');
  marked = null;
  $('#drophint')?.remove();
}

document.addEventListener('dragover', ev => {
  const kind = dropKind(ev.dataTransfer);
  if (!kind) return;
  ev.preventDefault(); // otherwise the browser opens a file dropped next to a target
  const t = ev.target.closest?.('[data-drop]');
  const dest = t?.dataset.drop;
  const ok = !!dest && (kind === 'upload' || canMove(dragging?.path, dest));
  ev.dataTransfer.dropEffect = ok ? (kind === 'move' ? 'move' : 'copy') : 'none';
  if (ok) markDrop(t, `${kind === 'move' ? 'Move' : 'Upload'} to ${dest.split('/').pop()}`);
  else clearDropMark();
});
document.addEventListener('dragleave', ev => { if (!ev.relatedTarget) clearDropMark(); });
document.addEventListener('dragend', () => { dragging = null; clearDropMark(); document.querySelectorAll('.dragging').forEach(x => x.classList.remove('dragging')); });
document.addEventListener('drop', ev => {
  const kind = dropKind(ev.dataTransfer);
  if (!kind) return;
  ev.preventDefault();
  const dest = ev.target.closest?.('[data-drop]')?.dataset.drop;
  clearDropMark();
  if (!dest) return;
  if (kind === 'move') {
    const src = ev.dataTransfer.getData(MOVE_TYPE);
    dragging = null;
    if (src && canMove(src, dest)) moveInto(src, dest);
    return;
  }
  // folder entries have to be taken before this handler returns
  const items = [...ev.dataTransfer.items].filter(i => i.kind === 'file').map(i => i.webkitGetAsEntry?.() || i.getAsFile());
  uploadDropped(dest, items);
});

async function uploadDropped(dest, items) {
  const files = [], dirs = [];
  const read = r => new Promise((res, rej) => r.readEntries(res, rej));
  const walk = async (entry, dir) => {
    if (entry instanceof File) { files.push({ f: entry, dir }); return; }
    if (entry.isFile) { files.push({ f: await new Promise((res, rej) => entry.file(res, rej)), dir }); return; }
    if (!entry.isDirectory) return;
    const sub = dir + '/' + entry.name;
    dirs.push(sub);
    const r = entry.createReader();
    for (let batch; (batch = await read(r)).length;) for (const c of batch) await walk(c, sub);
  };
  try {
    for (const it of items) if (it) await walk(it, dest);
    for (const d of dirs) await api('/api/fs/mkdir', { method: 'POST', body: { path: d, parents: true } });
  } catch (x) {
    uploadTray().append(h('li', {}, h('span', { class: 'label' }, 'Couldn’t read the dropped folders'), h('span', { class: 'err' }, x.message)));
    return;
  }
  if (files.length) uploadFiles(dest, files); else route();
}

async function moveInto(src, dest) {
  const name = src.split('/').pop();
  const st = h('span', { class: 'muted' }, 'Moving');
  uploadTray().append(h('li', {}, h('span', { class: 'label' }, `${name} to ${dest.split('/').pop()}`), st));
  try {
    const r = await api('/api/fs/move', { method: 'POST', body: { from: src, to: dest + '/' + name } });
    if (r.job) await followJob(r.job, st);
    else st.textContent = 'Moved';
  } catch (x) { st.textContent = x.message; st.className = 'err'; }
  if (getState().sel === src) nav({ sel: '' }, true); else route();
}

// followJob shows a copy between libraries until it ends.
async function followJob(id, st) {
  const bar = h('progress', { max: 1, value: 0 });
  st.before(bar);
  for (;;) {
    const j = await api('/api/jobs/' + id);
    bar.max = j.total || 1; bar.value = j.done;
    st.textContent = j.state === 'counting' ? 'Counting' : j.state === 'cleaning' ? 'Deleting the original'
      : j.state === 'copying' ? `${fmtSize(j.done)} of ${fmtSize(j.total)}` : j.state === 'done' ? 'Moved' : '';
    if (j.state === 'failed') throw new Error(j.error);
    if (j.state === 'done') { bar.value = bar.max; return; }
    await new Promise(r => setTimeout(r, 600));
  }
}

// ---------- connected NAS ----------
let tetherLine = null;
function tetherStatus() {
  tetherLine = h('p', { class: 'muted' });
  viewSettingsTether();
  return tetherLine;
}
async function viewSettingsTether() {
  if (!tetherLine) return;
  let s;
  try { s = await api('/api/tether'); } catch (x) { put(tetherLine, h('span', { class: 'err' }, x.message)); return; }
  const phones = n => n === 1 ? '1 phone' : `${n} phones`;
  put(tetherLine, !s.available ? h('span', { class: 'err' }, s.error)
    : !s.on ? 'Off'
    : !s.running ? h('span', { class: s.error ? 'err' : '' }, s.error ? 'Restarting: ' + s.error : 'Starting')
    : [h('b', {}, s.online ? 'Reachable from anywhere' : 'On, looking for a relay'), ` · ${phones(s.devices)} · `, h('span', { class: 'mono' }, s.id.slice(0, 10))]);
}

function remotesEditor() {
  const list = h('div', { class: 'stack' });
  const load = async () => {
    let rs;
    try { rs = await api('/api/remotes'); } catch (x) { put(list, h('p', { class: 'err' }, x.message)); return; }
    put(list, rs.length ? h('table', { class: 'files' }, h('tbody', {}, rs.map(r => h('tr', {},
      h('td', { class: 'name' }, h('span', {}, icon('nas', 'k-folder'), h('span', { class: 'label' }, r.name)),
        h('span', { class: 'where' }, r.config.kind === 'smb' ? `SMB ${r.config.host}/${r.config.share}${r.config.folder ? '/' + r.config.folder : ''}` : `Burrow ${r.config.url}${r.config.folder ? ' / ' + r.config.folder : ''}`)),
      h('td', { class: r.error ? 'err' : 'muted' }, r.error || 'Connected'),
      h('td', { class: 'r' }, h('button', { class: 'btn danger', type: 'button', onclick: async ev => {
        const b = ev.currentTarget;
        if (!b.dataset.armed) { b.dataset.armed = '1'; b.textContent = 'Remove ' + r.name; return; }
        await api('/api/remotes/' + r.id, { method: 'DELETE' }); load(); loadNav();
      } }, 'Remove')))))) : h('p', { class: 'muted' }, 'Nothing connected.'));
  };
  load();
  const kind = h('select', { name: 'kind', 'aria-label': 'Kind' }, h('option', { value: 'smb' }, 'SMB share'), h('option', { value: 'burrow' }, 'Another Burrow'));
  const field = (label, attrs, help) => h('label', { class: 'field' }, h('span', {}, label, help ? [' ', info(help)] : null), h('input', { autocomplete: 'off', spellcheck: 'false', ...attrs }));
  const shares = h('datalist', { id: 'smb-shares' });
  const smb = h('div', { class: 'grid2' },
    field('Address', { name: 'host', placeholder: '192.168.1.20', class: 'mono' }),
    field('Port', { name: 'port', type: 'number', min: 1, max: 65535, placeholder: '445' }),
    field('User name', { name: 'user' }),
    field('Password', { name: 'password', type: 'password', autocomplete: 'new-password' }),
    h('label', { class: 'field' }, h('span', {}, 'Share'), h('span', { class: 'row' }, h('input', { name: 'share', list: 'smb-shares', autocomplete: 'off', placeholder: 'Media' }),
      h('button', { class: 'btn', type: 'button', onclick: async () => {
        err.textContent = '';
        try { const names = await api('/api/remotes/shares', { method: 'POST', body: smbBody() }); shares.replaceChildren(...names.map(n => h('option', { value: n }))); if (names.length && !f.share.value) f.share.value = names[0]; } catch (x) { err.textContent = x.message; }
      } }, 'Find shares')), shares),
    field('Domain', { name: 'domain', placeholder: 'Optional' }));
  const bur = h('div', { class: 'grid2', hidden: true },
    field('Address', { name: 'url', placeholder: 'https://burrow.example.com', inputmode: 'url', class: 'mono' }),
    field('API token', { name: 'token', type: 'password', autocomplete: 'off' }, 'Make one on that Burrow in Settings, MCP and API tokens, with API access on. It decides which of its folders this one reaches.'),
    field('Certificate fingerprint', { name: 'pin', class: 'mono', placeholder: 'Only for a self-signed certificate' }, 'The SHA-256 fingerprint that Burrow prints in its log at startup.'));
  const err = h('span', { class: 'err' });
  const f = h('form', { class: 'stack' },
    h('div', { class: 'grid2' }, field('Name', { name: 'name', required: true, placeholder: 'Office NAS' }, 'The library name people see. It can\u2019t be changed later.'), h('label', { class: 'field' }, h('span', {}, 'Kind'), kind)),
    smb, bur, field('Start in folder', { name: 'folder', placeholder: 'Optional, e.g. Projects/2026', class: 'mono' }),
    h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'submit' }, 'Connect'), err));
  const smbBody = () => ({ host: f.host.value.trim(), port: Number(f.port.value) || 0, user: f.user.value, password: f.password.value, domain: f.domain.value });
  kind.addEventListener('change', () => { smb.hidden = kind.value !== 'smb'; bur.hidden = kind.value !== 'burrow'; });
  f.addEventListener('submit', async e => {
    e.preventDefault(); err.textContent = '';
    const body = { name: f.name.value.trim(), kind: kind.value, folder: f.folder.value.trim(),
      ...(kind.value === 'smb' ? { ...smbBody(), share: f.share.value.trim() } : { url: f.url.value.trim(), token: f.token.value.trim(), pin: f.pin.value.trim() }) };
    const b = f.querySelector('button[type=submit]');
    b.disabled = true; b.textContent = 'Connecting';
    try { await api('/api/remotes', { method: 'POST', body }); f.reset(); kind.dispatchEvent(new Event('change')); load(); loadNav(); } catch (x) { err.textContent = x.message; }
    b.disabled = false; b.textContent = 'Connect';
  });
  return [list, f];
}

function newFolderControl(dir) {
  const wrap = h('span', { class: 'row' });
  const btn = h('button', { class: 'btn', type: 'button', onclick: () => {
    const input = h('input', { placeholder: 'Folder name', 'aria-label': 'New folder name', autocomplete: 'off' });
    const err = h('span', { class: 'err' });
    const go = async () => {
      if (!input.value.trim()) return;
      try { const e = await api('/api/fs/mkdir', { method: 'POST', body: { path: dir + '/' + input.value.trim() } }); nav({ path: e.path, sel: '' }); } catch (x) { err.textContent = x.message; }
    };
    input.addEventListener('keydown', e => { if (e.key === 'Enter') go(); if (e.key === 'Escape') wrap.replaceChildren(btn); });
    wrap.replaceChildren(input, h('button', { class: 'btn primary', type: 'button', onclick: go }, 'Create'), h('button', { class: 'btn', type: 'button', onclick: () => wrap.replaceChildren(btn) }, 'Cancel'), err);
    input.focus();
  } }, icon('plus'), 'New folder');
  wrap.append(btn);
  return wrap;
}

function renameControl(e) {
  const wrap = h('span', { class: 'row' });
  const btn = h('button', { class: 'btn', type: 'button', onclick: () => {
    const input = h('input', { value: e.name, 'aria-label': 'New name', autocomplete: 'off', spellcheck: 'false' });
    const err = h('span', { class: 'err' });
    const go = async () => {
      const name = input.value.trim();
      if (!name || name === e.name) { wrap.replaceChildren(btn); return; }
      try {
        const r = await api('/api/fs/move', { method: 'POST', body: { from: e.path, to: parentOf(e.path) + '/' + name } });
        panelFor = ''; nav({ sel: r.path }, true);
      } catch (x) { err.textContent = x.message; }
    };
    input.addEventListener('keydown', ev => { if (ev.key === 'Enter') go(); if (ev.key === 'Escape') wrap.replaceChildren(btn); });
    wrap.replaceChildren(input, h('button', { class: 'btn primary', type: 'button', onclick: go }, 'Rename'), h('button', { class: 'btn', type: 'button', onclick: () => wrap.replaceChildren(btn) }, 'Cancel'), err);
    input.focus();
    const dot = e.dir ? -1 : e.name.lastIndexOf('.');
    input.setSelectionRange(0, dot > 0 ? dot : e.name.length);
  } }, icon('edit'), 'Rename');
  wrap.append(btn);
  return wrap;
}

function deleteControl(e) {
  const err = h('span', { class: 'err' });
  return h('span', { class: 'row' }, h('button', { class: 'btn danger', type: 'button', onclick: async ev => {
    const b = ev.currentTarget;
    if (!b.dataset.armed) { b.dataset.armed = '1'; b.lastChild.textContent = 'Move to trash'; return; }
    try {
      const [r] = await api('/api/fs/delete', { method: 'POST', body: { paths: [e.path] } });
      if (!r.ok) throw new Error(r.error);
      panelFor = ''; nav({ sel: '' }, true);
    } catch (x) { err.textContent = x.message; }
  } }, icon('trash'), h('span', {}, 'Delete')), err);
}

async function viewTrash(s, seq) {
  const all = ME.admin && s.tab === 'all';
  const items = await api('/api/trash' + (all ? '?all=1' : ''));
  if (seq !== routeSeq) return;
  CURRENT = [];
  const seg = ME.admin ? h('div', { class: 'seg', role: 'group', 'aria-label': 'Whose' },
    [['', 'Mine'], ['all', 'Everyone’s']].map(([k, l]) => h('button', { type: 'button', 'aria-pressed': (s.tab || '') === k ? 'true' : 'false', onclick: () => nav({ tab: k }, true) }, l))) : null;
  put($('#sub'), h('b', {}, 'Recently deleted'), seg, h('span', { class: 'grow' }), h('span', { class: 'muted hide-s' }, 'Kept for 30 days'));
  if (!items.length) { put($('#content'), emptyState('Nothing deleted in the last 30 days.', 'Deleted files and folders, and older versions replaced by uploads, wait here for 30 days.')); return; }
  const err = h('p', { class: 'err' });
  put($('#content'), err, h('table', { class: 'files' },
    h('thead', {}, h('tr', {}, h('th', {}, 'Name'), h('th', { class: 'r hide-s' }, 'Size'), h('th', {}, 'Deleted'), all ? h('th', { class: 'hide-s' }, 'By') : null, h('th', {}))),
    h('tbody', {}, items.map(t => h('tr', {},
      h('td', { class: 'name' }, h('span', {}, icon(t.dir ? 'folder' : 'file', t.dir ? 'k-folder' : ''), h('span', { class: 'label' }, t.name)), h('span', { class: 'where' }, parentOf(t.path))),
      h('td', { class: 'r num muted hide-s' }, fmtSize(t.size)),
      h('td', { class: 'muted' }, fmtWhen(t.deleted)),
      all ? h('td', { class: 'muted hide-s' }, t.user) : null,
      h('td', { class: 'r' }, h('div', { class: 'row' },
        h('button', { class: 'btn', type: 'button', onclick: async () => {
          try { const r = await api(`/api/trash/${t.id}/restore`, { method: 'POST' }); nav({ view: 'browse', path: parentOf(r.path), sel: r.path, tab: '' }); } catch (x) { err.textContent = x.message; }
        } }, 'Restore'),
        h('button', { class: 'btn danger', type: 'button', onclick: async ev => {
          const b = ev.currentTarget;
          if (!b.dataset.armed) { b.dataset.armed = '1'; b.textContent = 'Delete forever'; return; }
          try { await api('/api/trash/' + t.id, { method: 'DELETE' }); route(); } catch (x) { err.textContent = x.message; }
        } }, 'Delete'))))))));
}
