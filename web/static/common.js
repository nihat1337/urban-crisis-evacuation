/* Shared helpers for the citizen and dispatcher apps. */
const API = '/api/v1';
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

let DICT = {};
let LANG = localStorage.getItem('lang') || document.documentElement.lang || 'az';

/* ---- theme: follows the OS (prefers-color-scheme) unless the user picks one ---- */
const THEME_KEY = 'theme'; // system | light | dark
// Default when the user hasn't picked one: Citizen opens light, Dispatcher opens dark.
const defaultTheme = () => document.body.classList.contains('dispatch-page') ? 'dark' : 'light';
const themePref = () => { try { return localStorage.getItem(THEME_KEY) || defaultTheme(); } catch (_) { return defaultTheme(); } };
const resolvedLight = (pref = themePref()) => pref === 'light' || (pref === 'system' && window.matchMedia('(prefers-color-scheme: light)').matches);
function applyTheme() {
  document.body.classList.toggle('light', resolvedLight());
  const b = $('#theme');
  if (!b) return;
  const p = themePref();
  b.innerHTML = icon(p === 'system' ? 'monitor' : p === 'light' ? 'sun' : 'moon', 17);
  b.title = t(p === 'system' ? 'theme_system' : p === 'light' ? 'theme_light' : 'theme_dark');
}
function initTheme() {
  applyTheme();
  const b = $('#theme');
  if (b) b.onclick = () => {
    const order = ['system', 'light', 'dark'];
    const next = order[(order.indexOf(themePref()) + 1) % order.length];
    try { localStorage.setItem(THEME_KEY, next); } catch (_) { /* ignore */ }
    location.reload(); // map tiles/styles are chosen at start-up
  };
  window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => { if (themePref() === 'system') location.reload(); });
  document.addEventListener('langchange', applyTheme);
}

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
  // Dispatcher auth: JWT from /auth/login (Bearer); legacy service key still accepted by the server.
  const tok = key ?? localStorage.getItem('dispatcherToken');
  if (tok) headers['Authorization'] = 'Bearer ' + tok;
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

// Line icons (inline SVG) used by the redesigned UI. Emoji above stay for
// plain-text toasts; HTML views use icon(name, size).
const ICON_PATHS = {
  fire: '<path d="M12 3c1 3 4.5 4.6 4.5 9.2a4.5 4.5 0 0 1-9 0c0-2.2 1.1-3.4 2.2-4.4 0 2 1 3 2.1 3.1C11.8 8 11 6 12 3z"/>',
  collapse: '<path d="M3 21h18"/><path d="M5 21V8l6-4v6"/><path d="M14 21v-7l5 2v5"/><path d="M11 13l-2 3 2 2-1 3"/>',
  flood: '<path d="M3 9c2 0 2-2 4.5-2S10 9 12 9s2-2 4.5-2S19 9 21 9"/><path d="M3 15c2 0 2-2 4.5-2s2.5 2 4.5 2 2-2 4.5-2 2.5 2 4.5 2"/>',
  medical: '<path d="M9.5 3.5h5v6h6v5h-6v6h-5v-6h-6v-5h6z"/>',
  wheelchair: '<circle cx="11" cy="4" r="1.6"/><path d="M11 7v6h5l2.5 5.5"/><path d="M11 10h5"/><path d="M8 11.3A5 5 0 1 0 14.6 18"/>',
  shield: '<path d="M12 2.8l7.5 2.8v5.9c0 4.7-3.2 8.2-7.5 9.7-4.3-1.5-7.5-5-7.5-9.7V5.6z"/><path d="M8.2 12h7.6"/><path d="M12.8 9l3 3-3 3"/>',
  bunker: '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/>',
  park: '<path d="M12 3l5 7h-3l4 6H6l4-6H7z"/><path d="M12 16v5"/>',
  stadium: '<ellipse cx="12" cy="12" rx="9" ry="6"/><ellipse cx="12" cy="12" rx="4" ry="2.5"/>',
  tent: '<path d="M3 20L12 4l9 16z"/><path d="M12 20v-6"/>',
  truck: '<path d="M3 16V8h11v8M14 11h4l3 3v2h-7"/><circle cx="7" cy="17" r="2"/><circle cx="17" cy="17" r="2"/>',
  check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
  gps: '<circle cx="12" cy="12" r="7"/><circle cx="12" cy="12" r="2"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3"/>',
  road: '<path d="M4 8h16v4H4z"/><path d="M6 12v7M18 12v7"/>',
  chevron: '<path d="M9 6l6 6-6 6"/>',
  ruler: '<path d="M3 17L17 3l4 4L7 21z"/><path d="M7 13l2 2M10 10l2 2M13 7l2 2"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  users: '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16 4.5a3.5 3.5 0 0 1 0 7M18 14a6 6 0 0 1 3.5 6"/>',
  sos: '<circle cx="12" cy="12" r="9"/><path d="M12 7v6M12 16.5h.01"/>',
  chat: '<path d="M4 5h16v11H9l-5 4z"/>',
  phone: '<path d="M5 4h4l2 5-2.5 1.5a11 11 0 0 0 5 5L15 13l5 2v4a2 2 0 0 1-2 2A16 16 0 0 1 3 6a2 2 0 0 1 2-2"/>',
  mail: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/>',
  monitor: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
  moon: '<path d="M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z"/>',
  wifi_off: '<path d="M2 8.5a15 15 0 0 1 4.5-2.8M10 5.1A15 15 0 0 1 22 8.5M5 12a10 10 0 0 1 3.4-2.1M15.6 10A10 10 0 0 1 19 12M8.5 15.5a5 5 0 0 1 7 0"/><path d="M12 19h.01M3 3l18 18"/>',
  logout: '<path d="M15 4h4v16h-4"/><path d="M10 8l-4 4 4 4M6 12h10"/>',
};
const SHELTER_ICON_NAME = { shelter: 'bunker', park: 'park', stadium: 'stadium', temporary: 'tent' };
function icon(name, size = 18) {
  const p = ICON_PATHS[name];
  return p ? `<svg class="ico" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${p}</svg>` : '';
}

function debounce(fn, ms) {
  let tm; return (...a) => { clearTimeout(tm); tm = setTimeout(() => fn(...a), ms); };
}

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const s = document.createElement('script');
    s.src = src; s.async = true;
    s.onload = resolve; s.onerror = () => reject(new Error('failed to load ' + src));
    document.head.appendChild(s);
  });
}

// Google "night mode" style so the base map matches the dark UI.
const GOOGLE_NIGHT_STYLE = [
  { elementType: 'geometry', stylers: [{ color: '#242f3e' }] },
  { elementType: 'labels.text.stroke', stylers: [{ color: '#242f3e' }] },
  { elementType: 'labels.text.fill', stylers: [{ color: '#746855' }] },
  { featureType: 'administrative.locality', elementType: 'labels.text.fill', stylers: [{ color: '#d59563' }] },
  { featureType: 'poi', elementType: 'labels.text.fill', stylers: [{ color: '#d59563' }] },
  { featureType: 'poi.park', elementType: 'geometry', stylers: [{ color: '#263c3f' }] },
  { featureType: 'poi.park', elementType: 'labels.text.fill', stylers: [{ color: '#6b9a76' }] },
  { featureType: 'road', elementType: 'geometry', stylers: [{ color: '#38414e' }] },
  { featureType: 'road', elementType: 'geometry.stroke', stylers: [{ color: '#212a37' }] },
  { featureType: 'road', elementType: 'labels.text.fill', stylers: [{ color: '#9ca5b3' }] },
  { featureType: 'road.highway', elementType: 'geometry', stylers: [{ color: '#746855' }] },
  { featureType: 'road.highway', elementType: 'geometry.stroke', stylers: [{ color: '#1f2835' }] },
  { featureType: 'road.highway', elementType: 'labels.text.fill', stylers: [{ color: '#f3d19c' }] },
  { featureType: 'transit', elementType: 'geometry', stylers: [{ color: '#2f3948' }] },
  { featureType: 'transit.station', elementType: 'labels.text.fill', stylers: [{ color: '#d59563' }] },
  { featureType: 'water', elementType: 'geometry', stylers: [{ color: '#17263c' }] },
  { featureType: 'water', elementType: 'labels.text.fill', stylers: [{ color: '#515c6d' }] },
  { featureType: 'water', elementType: 'labels.text.stroke', stylers: [{ color: '#17263c' }] },
];

// makeMap builds the Leaflet map. Base layer: Google Maps (via GoogleMutant)
// when the server has a Google Maps key, otherwise OpenStreetMap. All markers,
// routes and click handlers are Leaflet, so the provider swap is invisible to
// the rest of the app — and any Google failure falls back to OSM.
async function makeMap(id, { light = document.body.classList.contains('light') } = {}) {
  const map = L.map(id, { zoomControl: true }).setView([40.40, 49.86], 12);
  const osm = () => L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
    className: light ? '' : 'dark-tiles',
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
  });
  let cfg = {};
  try { cfg = await (await fetch(`${API}/config`)).json(); } catch (_) { /* offline */ }
  // If Google failed recently (quota / auth / network) skip it for 10 minutes so the
  // user never waits in front of a blank map.
  let skipGoogle = false;
  try { skipGoogle = Number(sessionStorage.getItem('gmapsFailedAt') || 0) > Date.now() - 10 * 60 * 1000; } catch (_) { /* ignore */ }
  if (cfg.google_maps_key && !skipGoogle) {
    let googleLayer = null, satellite = null, control = null, fellBack = false;
    const fallbackToOSM = (why) => {
      if (fellBack) return;
      fellBack = true;
      console.warn('Google Maps unavailable (' + why + ') — falling back to OpenStreetMap');
      try { sessionStorage.setItem('gmapsFailedAt', String(Date.now())); } catch (_) { /* ignore */ }
      try { if (control) map.removeControl(control); if (googleLayer) map.removeLayer(googleLayer); if (satellite && map.hasLayer(satellite)) map.removeLayer(satellite); } catch (_) { /* ignore */ }
      osm().addTo(map);
      map.provider = 'osm';
      if (typeof toast === 'function') toast('🗺️ Google Maps əlçatan deyil — OpenStreetMap', 'warn', 5000);
    };
    try {
      window.gm_authFailure = () => fallbackToOSM('auth');
      const ready = new Promise(res => { window.__gmapsReady = res; });
      await loadScript(`https://maps.googleapis.com/maps/api/js?key=${encodeURIComponent(cfg.google_maps_key)}&v=weekly&language=${LANG}&callback=__gmapsReady`);
      await ready;
      await loadScript('https://unpkg.com/leaflet.gridlayer.googlemutant@0.14.1/dist/Leaflet.GoogleMutant.js');
      googleLayer = L.gridLayer.googleMutant({ type: 'roadmap', styles: light ? [] : GOOGLE_NIGHT_STYLE, maxZoom: 20 });
      satellite = L.gridLayer.googleMutant({ type: 'hybrid', maxZoom: 20 });
      googleLayer.addTo(map);
      control = L.control.layers({ 'Google': googleLayer, 'Google Satellite': satellite }, null, { position: 'bottomleft', collapsed: true }).addTo(map);
      map.provider = 'google';
      // Quota exhaustion ("Maps Demo Key limit reached") renders a blank canvas
      // without any callback: detect "no tiles after 4 s" and fall back.
      setTimeout(() => {
        const tiles = document.querySelectorAll('.leaflet-google-mutant img').length;
        const blocked = !!document.querySelector('.gm-style .gm-err-container, .leaflet-google-mutant .gm-err-container');
        if (map.provider === 'google' && (tiles === 0 || blocked)) fallbackToOSM(blocked ? 'error overlay' : 'no tiles / quota');
      }, 4000);
      return map;
    } catch (e) {
      fallbackToOSM(e.message);
      return map;
    }
  }
  // Plain OSM tiles need no API key; the dark look comes from a CSS filter (.dark-tiles).
  osm().addTo(map);
  map.provider = 'osm';
  return map;
}

// Theme must be in place before any page script runs (scripts load at the end of <body>).
initTheme();
