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
  folder: 'M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2h8.5A1.5 1.5 0 0 1 21 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5z',
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
    LIBS.map(l => h('div', { class: 'lib' }, link(l.name, 'folder', { view: 'browse', path: l.path }, inLib && (s.path === l.path || s.path.startsWith(l.path + '/'))))),
    favs.length ? h('h3', { class: 'lib' }, 'Favorites') : null,
    favs.map(f => h('div', { class: 'lib' }, link(f.name, f.dir ? 'folder' : f.kind || 'file', f.dir ? { view: 'browse', path: f.path } : { view: 'browse', path: parentOf(f.path), sel: f.path }, inLib && s.path === f.path))),
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
  loadNav();
  try {
    if (s.view === 'search') await viewSearch(s, seq);
    else if (s.view === 'shares') await viewShares(s, seq);
    else if (s.view === 'activity') await viewActivity(s, seq);
    else if (s.view === 'overview') await viewOverview(s, seq);
    else if (s.view === 'settings') await viewSettings(s, seq);
    else if (s.view === 'users' && ME.admin) await viewUsers(s, seq);
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
  crumbs.append(s.path ? h('a', { href: '?', onclick: e => { e.preventDefault(); nav({ path: '', sel: '' }); } }, 'Libraries') : h('b', {}, 'Libraries'));
  let acc = '';
  const segs = s.path ? s.path.split('/') : [];
  segs.forEach((seg, i) => {
    acc = acc ? acc + '/' + seg : seg;
    const p = acc;
    crumbs.append(h('span', { class: 'sep' }, '/'));
    crumbs.append(i === segs.length - 1 ? h('b', {}, seg) : h('a', { href: '?path=' + encodeURIComponent(p), onclick: e => { e.preventDefault(); nav({ path: p, sel: '' }); } }, seg));
  });
  const actions = s.path && data.can_share ? h('div', { class: 'row' },
    h('button', { class: 'btn', onclick: () => nav({ sel: s.path }) }, icon('link'), 'Share this folder')) : null;
  put($('#sub'), crumbs, h('span', { class: 'grow' }), actions);
  CURRENT = data.entries;
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
      onclick: ev => { if (ev.target.closest('a')) return; nav({ sel: e.path }, true); },
      ondblclick: () => openEntry(e) },
      h('td', { class: 'name' },
        h('span', {}, kindIcon(e),
          e.dir ? h('a', { class: 'label', href: '?path=' + encodeURIComponent(e.path), onclick: ev => { ev.preventDefault(); openEntry(e); } }, name) : h('span', { class: 'label' }, name),
          h('span', { class: 'flags' }, e.shared ? h('span', { title: `${e.shared} active link${e.shared > 1 ? 's' : ''}` }, icon('link')) : null, e.fav ? h('span', { title: 'Favorite' }, icon('star')) : null)),
        search ? h('span', { class: 'where' }, highlight(parentOf(e.path) || '/', terms)) : null),
      h('td', { class: 'r num muted' }, e.dir ? '' : fmtSize(e.size)),
      h('td', { class: 'r num muted hide-s' }, fmtDate(e.mtime)));
    tb.append(tr);
  }
  return h('table', { class: 'files' },
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
        b.dataset.armed = '1'; b.textContent = `Delete “${x.label || x.id}” and its history`;
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
const ACCESS = [['read', 'View'], ['share', 'View and share'], ['none', 'No access']];
const grantAccess = g => !g.read ? 'none' : g.share ? 'share' : 'read';
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

// grantsEditor edits a user's folder access: one row per folder, most specific wins.
function grantsEditor(grants) {
  const list = h('div', { class: 'grants' });
  const options = h('datalist', { id: 'folder-options' });
  let t = null;
  const suggest = async q => {
    try {
      const paths = q ? (await api('/api/search?' + new URLSearchParams({ q, kind: 'folder', limit: 20 }))).entries.map(e => e.path) : LIBS.map(l => l.path);
      options.replaceChildren(...paths.map(p => h('option', { value: p })));
    } catch { /* suggestions are optional */ }
  };
  const addRow = (g = { path: '', read: true, share: false }) => {
    const path = h('input', { value: g.path, list: 'folder-options', class: 'mono', placeholder: 'Folder, e.g. DOCUMENTS/Taxes', 'aria-label': 'Folder', autocomplete: 'off', spellcheck: 'false',
      oninput: e => { clearTimeout(t); t = setTimeout(() => suggest(e.target.value.trim()), 150); }, onfocus: e => suggest(e.target.value.trim()) });
    const access = h('select', { 'aria-label': 'Access' }, ACCESS.map(([v, l]) => h('option', { value: v }, l)));
    access.value = grantAccess(g);
    const row = h('div', { class: 'grant' }, path, access, h('button', { type: 'button', class: 'iconbtn', 'aria-label': 'Remove folder', title: 'Remove', onclick: () => row.remove() }, icon('x')));
    list.append(row);
    return path;
  };
  grants.forEach(g => addRow(g));
  const read = () => [...list.querySelectorAll('.grant')].map(r => {
    const a = r.querySelector('select').value;
    return { path: r.querySelector('input').value.trim(), read: a !== 'none', share: a === 'share' };
  }).filter(g => g.path);
  const el = h('div', { class: 'field' },
    h('span', {}, 'Folder access', info('Each folder includes everything inside it. The most specific folder wins, so No access on a subfolder hides it inside a folder they can view. Links a user made stop working if they lose share access.')),
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
    actErr];
  panelShell(isNew ? 'New user' : u.username, f, account);
  if (isNew) f.username.focus();
}

// ---------- settings ----------
async function viewSettings(s, seq) {
  SETTINGS = null;
  const st = await settings();
  const tokens = await api('/api/tokens');
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
  const tokenOut = h('div');
  const mcpURL = (st.public_url || location.origin) + '/mcp';
  const admin = st.admin;
  put($('#content'), h('div', { class: 'page' },
    admin ? [section('Public address', 'Share links and QR codes are built from this address.',
      h('input', { name: 'url', value: st.public_url, placeholder: location.origin, inputmode: 'url' }),
      f => api('/api/settings', { method: 'PUT', body: { public_url: f.url.value } })),
    section('Public port', `Port ${st.public_addr || '(off)'} is the one to forward from your reverse proxy. It always serves share links, and nothing else unless you turn it on here.`,
      h('div', { class: 'checks' },
        h('label', {}, h('input', { type: 'checkbox', name: 'pui', checked: st.public_ui }), 'Also serve the web UI and its API'),
        h('label', {}, h('input', { type: 'checkbox', name: 'pmcp', checked: st.public_mcp }), 'Also serve MCP (still needs an API token)')),
      f => api('/api/settings', { method: 'PUT', body: { public_ui: f.pui.checked, public_mcp: f.pmcp.checked } })),
    section('Libraries', `Top-level folders inside ${st.data_dir}. Only checked folders can be searched, browsed or shared.`,
      h('div', { class: 'checks' }, st.roots.map(r => h('label', {}, h('input', { type: 'checkbox', name: 'root', value: r.name, checked: r.enabled }), r.name))),
      f => api('/api/settings', { method: 'PUT', body: { roots: [...f.querySelectorAll('input[name=root]:checked')].map(i => i.value) } })),
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
    section('MCP and API tokens', 'Tokens let an LLM client (or a script) search, read and share through the MCP endpoint. A token can see and share only what your account can, and can\u2019t change account settings. Delete it to cut access.',
      [h('dl', { class: 'facts' }, h('dt', {}, 'MCP endpoint'), h('dd', { class: 'mono' }, mcpURL)),
        tokens.length ? h('table', { class: 'files' }, h('tbody', {}, tokens.map(t => h('tr', {},
          h('td', {}, t.name), h('td', { class: 'muted' }, t.last_used ? `Used ${fmtWhen(t.last_used)}${t.last_ip ? ' from ' + t.last_ip : ''}` : 'Never used'),
          h('td', { class: 'r' }, h('button', { class: 'btn danger', type: 'button', onclick: async () => { await api('/api/tokens/' + t.id, { method: 'DELETE' }); route(); } }, 'Delete')))))) : null,
        h('div', { class: 'row' }, h('input', { name: 'tname', placeholder: 'Token name, e.g. Claude' }), h('button', { class: 'btn', type: 'button', onclick: async e => {
          const f = e.currentTarget.form;
          const t = await api('/api/tokens', { method: 'POST', body: { name: f.tname.value } });
          const cfg = JSON.stringify({ mcpServers: { burrow: { type: 'http', url: t.mcp_url, headers: { Authorization: 'Bearer ' + t.token } } } }, null, 2);
          tokenOut.replaceChildren(
            h('p', {}, h('b', {}, 'Copy this token now. It won’t be shown again.')),
            h('div', { class: 'secret' }, t.token),
            h('p', { class: 'muted' }, 'MCP client config:'),
            h('pre', { class: 'code' }, cfg),
            h('div', { class: 'row' }, h('button', { class: 'btn', type: 'button', onclick: ev => copy(t.token, ev.currentTarget) }, 'Copy token'), h('button', { class: 'btn', type: 'button', onclick: ev => copy(cfg, ev.currentTarget) }, 'Copy config')));
        } }, 'Create token')),
        tokenOut], null),
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
        h('label', { class: 'field' }, h('span', {}, 'Code from the app'), h('input', { name: 'code', autocomplete: 'one-time-code', inputmode: 'numeric', class: 'mono', required: true, maxlength: 6 })),
        err, h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'submit' }, 'Turn on')));
      f.addEventListener('submit', async e => {
        e.preventDefault();
        try {
          const r = await api('/api/2fa/enable', { method: 'POST', body: { code: f.code.value } });
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
