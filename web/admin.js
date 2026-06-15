'use strict';

const STORAGE_KEY = 'admin_token';
const POLL_INTERVAL_MS = 1000;
const API_PATH = '/api/admin/synthetic-stats';

let pollTimer = null;

// ── DOM refs ──────────────────────────────────────────────────────────────────
const authSection   = document.getElementById('auth-section');
const statsSection  = document.getElementById('stats-section');
const tokenForm     = document.getElementById('token-form');
const tokenInput    = document.getElementById('token-input');
const authError     = document.getElementById('auth-error');
const disconnectBtn = document.getElementById('disconnect-btn');
const statusDot     = document.getElementById('status-dot');
const sampledAt     = document.getElementById('sampled-at');
const synPanel      = document.getElementById('syn-panel');
const synUnavail    = document.getElementById('syn-unavailable');
const synCards      = document.getElementById('syn-cards');

// ── Token management (sessionStorage only) ────────────────────────────────────
function getToken()       { return sessionStorage.getItem(STORAGE_KEY) || ''; }
function saveToken(t)     { sessionStorage.setItem(STORAGE_KEY, t); }
function clearToken()     { sessionStorage.removeItem(STORAGE_KEY); }

// ── Polling ───────────────────────────────────────────────────────────────────
function startPolling() {
  if (pollTimer !== null) return;
  poll();
  pollTimer = setInterval(poll, POLL_INTERVAL_MS);
}

function stopPolling() {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

async function poll() {
  const token = getToken();
  if (!token) { disconnect(); return; }

  let resp;
  try {
    resp = await fetch(API_PATH, {
      headers: { Authorization: 'Bearer ' + token },
    });
  } catch {
    setDot('red');
    setSampledAt('Connection error');
    return;
  }

  if (resp.status === 404) {
    stopPolling();
    setDot('red');
    showAuth('Admin API not enabled on this server.');
    return;
  }
  if (resp.status === 401) {
    stopPolling();
    setDot('red');
    clearToken();
    showAuth('Invalid token — please re-enter.');
    return;
  }
  if (!resp.ok) {
    setDot('red');
    setSampledAt('Server error ' + resp.status);
    return;
  }

  let data;
  try { data = await resp.json(); } catch {
    setDot('red');
    setSampledAt('JSON parse error');
    return;
  }

  renderStats(data);
}

// ── Render ────────────────────────────────────────────────────────────────────
function renderStats(data) {
  setDot('green');

  const served = data.served_at ? new Date(data.served_at) : null;
  setSampledAt(served ? 'as of ' + served.toLocaleTimeString() : '');

  const hub = data.hub || null;
  setValue('c-connected',       hub ? hub.ConnectedClients     : null);
  setValue('c-sim-ticks',       hub ? hub.SimulationTicks      : null);
  setValue('c-moved',           hub ? hub.MovedPlayers         : null);
  setValue('c-aoi-pairs',       hub ? hub.AOICandidatePairs    : null);
  setValue('c-aoi-checks',      hub ? hub.AOIDistanceChecks    : null);
  setValue('c-entered',         hub ? hub.RelationshipsEntered : null);
  setValue('c-left',            hub ? hub.RelationshipsLeft    : null);
  setValue('c-repl-msgs',       hub ? hub.ReplicationMessages  : null);
  setValue('c-repl-recipients', hub ? hub.ReplicationRecipients: null);
  setValue('c-repl-bytes',      hub ? fmtBytes(hub.ReplicationBytes) : null, true);

  const syn = data.synthetic || null;
  if (syn === null) {
    synCards.classList.add('hidden');
    synUnavail.classList.remove('hidden');
  } else {
    synCards.classList.remove('hidden');
    synUnavail.classList.add('hidden');
    setValue('c-syn-target',        syn.TargetCount);
    setValue('c-syn-active',        syn.ActiveCount);
    setValue('c-syn-activating',    syn.ActivatingCount);
    setValue('c-syn-moving',        syn.MovingCount);
    setValue('c-syn-idle',          syn.IdleCount);
    setValue('c-syn-failed',        syn.FailedCount);
    setValue('c-syn-inputs-rate',   fmtRate(syn.InputsPerSecond),     true);
    setValue('c-syn-msgs-rate',     fmtRate(syn.MessagesPerSecond),   true);
    setValue('c-syn-bytes-rate',    fmtBytes(syn.BytesPerSecond) + '/s', true);
    setValue('c-syn-total-activated', syn.TotalActivated);
    setValue('c-syn-total-msgs',    syn.TotalMessages);
  }

  showStats();
}

function setValue(id, v, raw) {
  const el = document.getElementById(id);
  if (!el) return;
  const vEl = el.querySelector('.card-value');
  if (v === null || v === undefined) { vEl.textContent = '—'; return; }
  vEl.textContent = raw ? v : fmtNum(v);
}

// ── Formatting helpers ────────────────────────────────────────────────────────
function fmtNum(n) {
  if (typeof n !== 'number') return String(n);
  if (n >= 1e9) return (n / 1e9).toFixed(1) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return n.toFixed(0);
}

function fmtRate(r) {
  if (r === null || r === undefined) return '—';
  return fmtNum(r) + '/s';
}

function fmtBytes(b) {
  if (b === null || b === undefined) return '—';
  if (b >= 1 << 30) return (b / (1 << 30)).toFixed(1) + ' GiB';
  if (b >= 1 << 20) return (b / (1 << 20)).toFixed(1) + ' MiB';
  if (b >= 1 << 10) return (b / (1 << 10)).toFixed(1) + ' KiB';
  return b + ' B';
}

// ── UI state helpers ──────────────────────────────────────────────────────────
function setDot(color) {
  statusDot.className = 'dot dot-' + color;
}

function setSampledAt(text) {
  sampledAt.textContent = text;
}

function showAuth(errorMsg) {
  authSection.classList.remove('hidden');
  statsSection.classList.add('hidden');
  tokenInput.value = '';
  if (errorMsg) {
    authError.textContent = errorMsg;
    authError.classList.remove('hidden');
  } else {
    authError.classList.add('hidden');
  }
  setDot('gray');
  setSampledAt('');
}

function showStats() {
  authSection.classList.add('hidden');
  statsSection.classList.remove('hidden');
  authError.classList.add('hidden');
}

function disconnect() {
  stopPolling();
  clearToken();
  showAuth('');
}

// ── Event wiring ──────────────────────────────────────────────────────────────
tokenForm.addEventListener('submit', (e) => {
  e.preventDefault();
  const t = tokenInput.value.trim();
  if (!t) return;
  saveToken(t);
  authError.classList.add('hidden');
  startPolling();
});

disconnectBtn.addEventListener('click', disconnect);

// ── Bootstrap ─────────────────────────────────────────────────────────────────
(function init() {
  if (getToken()) {
    showStats();
    startPolling();
  } else {
    showAuth('');
  }
})();
