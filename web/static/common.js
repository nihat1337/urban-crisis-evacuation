/* Shared helpers for the citizen and dispatcher apps. */
const API = '/api/v1';
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

let DICT = {};
let LANG = localStorage.getItem('lang') || document.documentElement.lang || 'az';

async function initI18n() {
  try {
    const info = await (await fetch(`${API}/i18n`)).json();
    const sel = $('#lang');
    if (sel) {
      sel.innerHTML = info.languages.map(l => `<option value="${l}">${l.toUpperCase()}</option>`).join('');
      sel.value = info.languages.includes(LANG) ? LANG : info.default;
      sel.onchange = () => setLang(sel.value);
      LANG = sel.value;
    }
  } catch (e) { /* offline: keep defaults */ }
  await setLang(LANG);
}

async function setLang(lang) {
  const r = await fetch(`${API}/i18n/${lang}`);
  if (!r.ok) return;
  DICT = await r.json();
  LANG = lang;
  localStorage.setItem('lang', lang);
  document.documentElement.lang = lang;
  applyI18n();
  document.dispatchEvent(new CustomEvent('langchange'));
}

const t = (k) => DICT['ui.' + k] ?? k;

function applyI18n() {
  $$('[data-i18n]').forEach(el => { el.textContent = t(el.dataset.i18n); });
  $$('[data-i18n-title]').forEach(el => { el.title = t(el.dataset.i18nTitle); });
  $$('[data-i18n-ph]').forEach(el => { el.placeholder = t(el.dataset.i18nPh); });
}

async function api(path, { method = 'GET', body, key } = {}) {
  const headers = { Accept: 'application/json', 'X-Lang': LANG };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const k = key ?? localStorage.getItem('dispatcherKey');
  if (k) headers['X-Dispatcher-Key'] = k;
  const res = await fetch(API + path, { method, headers, body: body !== undefined ? JSON.stringify(body) : undefined });
  if (!res.ok) {
    let msg = t('error_generic');
    try { const j = await res.json(); msg = j.error?.message || msg; } catch (_) { /* ignore */ }
    const err = new Error(msg); err.status = res.status; throw err;
  }
  return res.status === 204 ? null : res.json();
}

function toast(msg, kind = 'info', ms = 4500) {
  let box = $('#toasts');
  if (!box) { box = document.createElement('div'); box.id = 'toasts'; document.body.appendChild(box); }
  const el = document.createElement('div');
  el.className = `toast ${kind}`;
  el.textContent = msg;
  box.appendChild(el);
  setTimeout(() => el.remove(), ms);
}

function connectSSE(onEvent, onState) {
  const es = new EventSource(`${API}/events`);
  es.onopen = () => onState?.(true);
  es.onerror = () => onState?.(false);
  es.onmessage = (e) => { try { onEvent(JSON.parse(e.data)); } catch (_) { /* ignore */ } };
  return es;
}

function setLive(on) {
  const el = $('#live');
  if (!el) return;
  el.classList.toggle('on', on);
  $('span.txt', el).textContent = on ? t('live') : t('offline');
}

const fmtDist = (m) => m >= 1000 ? `${(m / 1000).toFixed(1)} km` : `${Math.round(m)} m`;
const fmtDur = (s) => `${Math.max(1, Math.round(s / 60))} ${t('min')}`;
const pctColor = (p) => p >= 95 ? '#ef4444' : p >= 90 ? '#f97316' : p >= 70 ? '#eab308' : '#22c55e';
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

function citizenId() {
  let id = localStorage.getItem('citizenId');
  if (!id) { id = 'c-' + Math.random().toString(36).slice(2, 10); localStorage.setItem('citizenId', id); }
  return id;
}

function timeAgo(iso) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${Math.round(s)}s`;
  if (s < 3600) return `${Math.round(s / 60)} ${t('min')}`;
  return `${Math.round(s / 3600)}h`;
}

const SOS_ICON = { fire: '🔥', collapse: '🏚️', flood: '🌊', medical: '🚑' };
const SHELTER_ICON = { shelter: '🛡️', park: '🌳', stadium: '🏟️', temporary: '⛺' };

function debounce(fn, ms) {
  let tm; return (...a) => { clearTimeout(tm); tm = setTimeout(() => fn(...a), ms); };
}

function makeMap(id) {
  const map = L.map(id, { zoomControl: true }).setView([40.40, 49.86], 12);
  // Plain OSM tiles need no API key; the dark look comes from a CSS filter (.dark-tiles).
  L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
    className: 'dark-tiles',
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
  }).addTo(map);
  return map;
}
