'use strict';

const POLL_INTERVAL_MS = 1000;
const API_PATH = '/api/stats/synthetic';

let pollTimer = null;

// ── DOM refs ──────────────────────────────────────────────────────────
const statsSection  = document.getElementById('stats-section');
const statusDot     = document.getElementById('status-dot');
const sampledAt     = document.getElementById('sampled-at');
const synPanel      = document.getElementById('syn-panel');
const synUnavail    = document.getElementById('syn-unavailable');
const synCards      = document.getElementById('syn-cards');

// ── Polling ───────────────────────────────────────────────────────────
function startPolling() {
  if (pollTimer !== null) return;
  poll();
  pollTimer = setInterval(poll, POLL_INTERVAL_MS);
}

async function poll() {
  let resp;
  try {
    resp = await fetch(API_PATH);
  } catch {
    setDot('red');
    setSampledAt('Connection error');
    return;
  }

  if (resp.status === 404) {
    setDot('red');
    setSampledAt('Stats API not available');
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

// ── Render ────────────────────────────────────────────────────────────
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
    setValue('c-syn-target',        syn.targetCount);
    setValue('c-syn-active',        syn.activeCount);
    setValue('c-syn-activating',    syn.activatingCount);
    setValue('c-syn-moving',        syn.movingCount);
    setValue('c-syn-idle',          syn.idleCount);
    setValue('c-syn-failed',        syn.failedCount);
    setValue('c-syn-inputs-rate',   fmtRate(syn.inputsPerSecond),     true);
    setValue('c-syn-msgs-rate',     fmtRate(syn.messagesPerSecond),   true);
    setValue('c-syn-bytes-rate',    fmtBytes(syn.bytesPerSecond) + '/s', true);
    setValue('c-syn-total-activated', syn.totalActivated);
    setValue('c-syn-total-msgs',    syn.totalMessages);
  }
}

function setValue(id, v, raw) {
  const el = document.getElementById(id);
  if (!el) return;
  const vEl = el.querySelector('.card-value');
  if (v === null || v === undefined) { vEl.textContent = '—'; return; }
  vEl.textContent = raw ? v : fmtNum(v);
}

// ── Formatting helpers ────────────────────────────────────────────────
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

// ── UI state helpers ──────────────────────────────────────────────────
function setDot(color) {
  statusDot.className = 'dot dot-' + color;
}

function setSampledAt(text) {
  sampledAt.textContent = text;
}

// ── Bootstrap ─────────────────────────────────────────────────────────
startPolling();
