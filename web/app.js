const startPosition = { lat: 31.2304, lng: 121.4737 };
const MARKER_SIZE = 20;
const MARKER_ANCHOR = MARKER_SIZE / 2;
const DEFAULT_APPEARANCE = { color: "#3388ff", shape: "circle" };
const APPEARANCE_SHAPES = ["circle", "square", "diamond", "triangle"];

let currentUserId = null;
let currentUsername = null;
let authoritativeAppearance = { ...DEFAULT_APPEARANCE };
let draftAppearance = { ...DEFAULT_APPEARANCE };
let menuOpen = false;
let editorOpen = false;
let savingAppearance = false;
let authMode = "login";
const markers = new Map();
const input = {
  up: false,
  down: false,
  left: false,
  right: false,
};

const retryDelays = [1000, 2000, 4000, 8000, 10000];

let inputSequence = 0;
let socket = null;
let retryTimer = null;
let retryAttempt = 0;
let shouldReconnect = true;

const map = L.map("map", { zoomControl: true }).setView(
  [startPosition.lat, startPosition.lng],
  16
);

L.tileLayer(
  "https://webrd0{s}.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=8&x={x}&y={y}&z={z}",
  {
    maxZoom: 18,
    subdomains: "1234",
    attribution: "&copy; 高德地图",
  }
).addTo(map);

let resetJoystick = () => { };

bootstrap();
bindKeyboardControls();
bindJoystickControls();
bindInputSafetyControls();
bindAuthControls();
bindAccountControls();

async function bootstrap() {
  const session = await fetchSession();
  if (session) {
    currentUserId = session.userId;
    currentUsername = session.username;
    setAuthoritativeAppearance(session.appearance || DEFAULT_APPEARANCE);
    hideAuthCard();
    showAccountControl();
    connect();
    return;
  }
  showAuthCard();
}

async function fetchSession() {
  const response = await fetch("/api/session");
  if (!response.ok) {
    return null;
  }
  return response.json();
}

function showAuthCard() {
  shouldReconnect = false;
  clearRetryTimer();
  if (socket) {
    socket.close();
    socket = null;
  }
  currentUserId = null;
  currentUsername = null;
  resetAccountUI();
  hideAccountControl();
  resetAuthMode();
  document.getElementById("auth-card").style.display = "";
  document.getElementById("status").style.display = "none";
  document.querySelector(".joystick").style.display = "none";
  document.getElementById("auth-error").textContent = "";
}

function resetAuthMode() {
  authMode = "login";
  document.getElementById("auth-title").textContent = "登录";
  document.getElementById("auth-submit").textContent = "登录";
  document.getElementById("auth-toggle-text").textContent = "没有账号？";
  document.getElementById("auth-toggle-link").textContent = "注册";
}

function hideAuthCard() {
  document.getElementById("auth-card").style.display = "none";
  document.getElementById("status").style.display = "";
  document.querySelector(".joystick").style.display = "";
}

function showAccountControl() {
  document.getElementById("account-ctrl").style.display = "";
  document.getElementById("account-username").textContent = currentUsername || "";
  renderAppearancePreview(
    document.getElementById("account-trigger-preview"),
    authoritativeAppearance
  );
}

function hideAccountControl() {
  document.getElementById("account-ctrl").style.display = "none";
}

function resetAccountUI() {
  menuOpen = false;
  editorOpen = false;
  savingAppearance = false;
  authoritativeAppearance = { ...DEFAULT_APPEARANCE };
  draftAppearance = { ...DEFAULT_APPEARANCE };
  closeAccountMenu();
  closeAppearanceEditor();
  setSaveAppearanceEnabled(true);
  document.getElementById("appearance-error").textContent = "";
}

function setAuthoritativeAppearance(appearance) {
  authoritativeAppearance = {
    color: appearance.color,
    shape: appearance.shape,
  };
  const preview = document.getElementById("account-trigger-preview");
  if (preview) {
    renderAppearancePreview(preview, authoritativeAppearance);
  }
}

function renderAppearancePreview(element, appearance) {
  element.style.setProperty("--marker-color", appearance.color);
  for (const shape of APPEARANCE_SHAPES) {
    element.classList.toggle(`appearance-preview--${shape}`, appearance.shape === shape);
  }
}

function applyAppearanceToCurrentUserMarker(appearance) {
  if (!currentUserId) {
    return;
  }
  renderAppearanceChanged(currentUserId, appearance);
}

function openAccountMenu() {
  closeAppearanceEditor();
  menuOpen = true;
  const menu = document.getElementById("account-menu");
  menu.hidden = false;
  document.getElementById("account-menu-trigger").setAttribute("aria-expanded", "true");
}

function closeAccountMenu() {
  menuOpen = false;
  const menu = document.getElementById("account-menu");
  menu.hidden = true;
  document.getElementById("account-menu-trigger").setAttribute("aria-expanded", "false");
}

function toggleAccountMenu() {
  if (menuOpen) {
    closeAccountMenu();
    return;
  }
  openAccountMenu();
}

function openAppearanceEditor() {
  closeAccountMenu();
  editorOpen = true;
  draftAppearance = {
    color: authoritativeAppearance.color,
    shape: authoritativeAppearance.shape,
  };
  document.getElementById("appearance-editor").hidden = false;
  document.getElementById("appearance-error").textContent = "";
  syncAppearanceEditorControls();
  renderAppearancePreview(
    document.getElementById("appearance-editor-preview"),
    draftAppearance
  );
}

function closeAppearanceEditor() {
  editorOpen = false;
  document.getElementById("appearance-editor").hidden = true;
  document.getElementById("appearance-error").textContent = "";
  setSaveAppearanceEnabled(true);
}

function syncAppearanceEditorControls() {
  document.getElementById("appearance-color").value = draftAppearance.color;
  for (const button of document.querySelectorAll(".appearance-editor__shape")) {
    const selected = button.dataset.shape === draftAppearance.shape;
    button.setAttribute("aria-pressed", selected ? "true" : "false");
  }
}

function updateDraftAppearance(nextAppearance) {
  draftAppearance = {
    color: nextAppearance.color,
    shape: nextAppearance.shape,
  };
  renderAppearancePreview(
    document.getElementById("appearance-editor-preview"),
    draftAppearance
  );
  syncAppearanceEditorControls();
}

function setSaveAppearanceEnabled(enabled) {
  savingAppearance = !enabled;
  document.getElementById("appearance-save").disabled = !enabled;
}

async function saveAppearance() {
  if (savingAppearance) {
    return;
  }
  document.getElementById("appearance-error").textContent = "";
  setSaveAppearanceEnabled(false);

  let resp;
  try {
    resp = await fetch("/api/appearance", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        color: draftAppearance.color,
        shape: draftAppearance.shape,
      }),
    });
  } catch {
    document.getElementById("appearance-error").textContent = "网络错误，请稍后重试";
    setSaveAppearanceEnabled(true);
    return;
  }

  let data = {};
  try {
    data = await resp.json();
  } catch {
    data = {};
  }

  if (!resp.ok) {
    document.getElementById("appearance-error").textContent =
      data.error || "保存失败，请重试";
    setSaveAppearanceEnabled(true);
    return;
  }

  const saved = { color: data.color, shape: data.shape };
  setAuthoritativeAppearance(saved);
  applyAppearanceToCurrentUserMarker(saved);
  closeAppearanceEditor();
  setSaveAppearanceEnabled(true);
}

function cancelAppearanceEdit() {
  closeAppearanceEditor();
}

async function handleAuthSubmit(event) {
  event.preventDefault();
  const username = document.getElementById("auth-username").value;
  const password = document.getElementById("auth-password").value;
  const errorEl = document.getElementById("auth-error");
  errorEl.textContent = "";

  const endpoint = authMode === "login" ? "/api/login" : "/api/register";
  let resp;
  try {
    resp = await fetch(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
  } catch {
    errorEl.textContent = "网络错误，请稍后重试";
    return;
  }

  const data = await resp.json();
  if (!resp.ok) {
    errorEl.textContent = data.error || "未知错误";
    return;
  }

  currentUserId = data.userId;
  currentUsername = data.username;
  setAuthoritativeAppearance(data.appearance || DEFAULT_APPEARANCE);
  hideAuthCard();
  showAccountControl();
  shouldReconnect = true;
  retryAttempt = 0;
  connect();
}

function toggleAuthMode(event) {
  event.preventDefault();
  authMode = authMode === "login" ? "register" : "login";
  document.getElementById("auth-title").textContent = authMode === "login" ? "登录" : "注册";
  document.getElementById("auth-submit").textContent = authMode === "login" ? "登录" : "注册";
  document.getElementById("auth-toggle-text").textContent = authMode === "login" ? "没有账号？" : "已有账号？";
  document.getElementById("auth-toggle-link").textContent = authMode === "login" ? "注册" : "登录";
  document.getElementById("auth-error").textContent = "";
}

function connect() {
  clearRetryTimer();

  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${protocol}//${window.location.host}/ws`;
  const currentSocket = new WebSocket(url);
  socket = currentSocket;

  if (retryAttempt === 0) {
    setStatus("connecting");
  } else {
    setStatus("reconnecting", retryAttempt);
  }

  currentSocket.addEventListener("open", () => {
    if (socket !== currentSocket) {
      return;
    }
    retryAttempt = 0;
    setStatus("connected");
    sendInput();
  });

  currentSocket.addEventListener("message", (event) => {
    if (socket !== currentSocket) {
      return;
    }
    const message = JSON.parse(event.data);
    if (message.type === "world_snapshot") {
      renderSnapshot(message.players);
    } else if (message.type === "players_delta") {
      renderDelta(message.players, message.removedPlayerIds);
    } else if (message.type === "appearance_changed") {
      renderAppearanceChanged(message.playerId, message.appearance);
    }
  });

  currentSocket.addEventListener("close", () => {
    if (socket !== currentSocket) {
      return;
    }
    socket = null;
    scheduleReconnect();
  });
}

async function logout() {
  shouldReconnect = false;
  clearRetryTimer();
  if (socket) {
    socket.close();
    socket = null;
  }
  try {
    await fetch("/api/logout", { method: "POST" });
  } catch {
    // 服务器可能在响应前就断开 WebSocket，忽略网络错误
  }
  currentUserId = null;
  currentUsername = null;
  resetAccountUI();
  markers.forEach((entry) => entry.marker.remove());
  markers.clear();
  setStatus("disconnected");
  hideAccountControl();
  showAuthCard();
}

function scheduleReconnect() {
  if (!shouldReconnect) {
    return;
  }
  clearRetryTimer();
  retryAttempt += 1;

  if (retryAttempt > retryDelays.length) {
    fetchSession().then((session) => {
      if (!session) {
        showAuthCard();
        return;
      }
      currentUserId = session.userId;
      currentUsername = session.username;
      setAuthoritativeAppearance(session.appearance || DEFAULT_APPEARANCE);
      showAccountControl();
      doReconnect();
    });
    return;
  }
  doReconnect();
}

function doReconnect() {
  setStatus("reconnecting", retryAttempt);
  const delay = retryDelays[Math.min(retryAttempt - 1, retryDelays.length - 1)];
  retryTimer = window.setTimeout(connect, delay);
}

function clearRetryTimer() {
  if (retryTimer !== null) {
    window.clearTimeout(retryTimer);
    retryTimer = null;
  }
}

function setDirection(direction, pressed) {
  if (input[direction] === pressed) {
    return;
  }
  input[direction] = pressed;
  sendInput();
}

function clearInput() {
  const changed = input.up || input.down || input.left || input.right;
  input.up = false;
  input.down = false;
  input.left = false;
  input.right = false;
  resetJoystick();
  if (changed) {
    sendInput();
  }
}

function sendInput() {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }

  inputSequence += 1;
  socket.send(
    JSON.stringify({
      type: "input",
      sequence: inputSequence,
      up: input.up,
      down: input.down,
      left: input.left,
      right: input.right,
    })
  );
}

function renderSnapshot(players) {
  const liveIds = new Set(players.map((player) => player.id));
  for (const [id, entry] of markers.entries()) {
    if (!liveIds.has(id)) {
      entry.marker.remove();
      markers.delete(id);
    }
  }
  for (const player of players) {
    upsertPlayerFromSnapshot(player);
  }
}

function renderDelta(players, removedPlayerIds) {
  for (const playerIdToRemove of removedPlayerIds) {
    removePlayerMarker(playerIdToRemove);
  }
  for (const player of players) {
    if (markers.has(player.id)) {
      updatePlayerPosition(player);
    } else {
      upsertPlayerFromSnapshot(player);
    }
  }
}

function renderAppearanceChanged(playerId, appearance) {
  const entry = markers.get(playerId);
  if (!entry || sameAppearance(entry.appearance, appearance)) {
    if (playerId === currentUserId && !editorOpen) {
      setAuthoritativeAppearance(appearance);
    }
    return;
  }
  entry.appearance = { color: appearance.color, shape: appearance.shape };
  entry.marker.setIcon(playerMarkerIcon(appearance));
  if (playerId === currentUserId && !editorOpen) {
    setAuthoritativeAppearance(appearance);
  }
}

function upsertPlayerFromSnapshot(player) {
  const latLng = [player.lat, player.lng];
  const label = markerLabel(player);
  const entry = markers.get(player.id);
  if (entry) {
    entry.marker.setLatLng(latLng);
    if (entry.username !== player.username) {
      entry.username = player.username;
      entry.marker.setTooltipContent(label);
    }
    if (!sameAppearance(entry.appearance, player.appearance)) {
      entry.appearance = { color: player.appearance.color, shape: player.appearance.shape };
      entry.marker.setIcon(playerMarkerIcon(player.appearance));
    }
    if (player.id === currentUserId && !editorOpen) {
      setAuthoritativeAppearance(player.appearance);
    }
  } else {
    const marker = L.marker(latLng, { icon: playerMarkerIcon(player.appearance) })
      .addTo(map)
      .bindTooltip(label);
    markers.set(player.id, {
      marker,
      username: player.username,
      appearance: { color: player.appearance.color, shape: player.appearance.shape },
    });
  }

  if (player.id === currentUserId) {
    map.panTo(latLng, { animate: true });
  }
}

function markerLabel(player) {
  if (player.id === currentUserId) {
    return "You";
  }
  return player.username || "Player";
}

function updatePlayerPosition(player) {
  const entry = markers.get(player.id);
  if (!entry) {
    return;
  }

  const latLng = [player.lat, player.lng];
  entry.marker.setLatLng(latLng);
  if (player.id === currentUserId) {
    map.panTo(latLng, { animate: true });
  }
}

function removePlayerMarker(playerId) {
  const entry = markers.get(playerId);
  if (!entry) {
    return;
  }
  entry.marker.remove();
  markers.delete(playerId);
}

function sameAppearance(left, right) {
  return left.color === right.color && left.shape === right.shape;
}

function playerMarkerIcon(appearance) {
  return L.divIcon({
    className: `player-marker player-marker--${appearance.shape}`,
    html: `<span class="player-marker__shape" style="--marker-color:${appearance.color}"></span>`,
    iconSize: [MARKER_SIZE, MARKER_SIZE],
    iconAnchor: [MARKER_ANCHOR, MARKER_ANCHOR],
    tooltipAnchor: [0, -MARKER_ANCHOR],
  });
}

function bindKeyboardControls() {
  const directions = {
    ArrowUp: "up",
    w: "up",
    ArrowDown: "down",
    s: "down",
    ArrowLeft: "left",
    a: "left",
    ArrowRight: "right",
    d: "right",
  };

  window.addEventListener("keydown", (event) => {
    if (isEditingInput()) {
      return;
    }
    const key = event.key;
    if (!key) { return; }
    const direction = directions[key] || directions[key.toLowerCase()];
    if (!direction) {
      return;
    }
    event.preventDefault();
    setDirection(direction, true);
  });

  window.addEventListener("keyup", (event) => {
    if (isEditingInput()) {
      return;
    }
    const key = event.key;
    if (!key) { return; }
    const direction = directions[key] || directions[key.toLowerCase()];
    if (!direction) {
      return;
    }
    event.preventDefault();
    setDirection(direction, false);
  });
}

function isEditingInput() {
  const el = document.activeElement;
  if (!el) {
    return false;
  }
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || el.isContentEditable;
}

function setJoystickDirections(up, down, left, right) {
  if (
    input.up === up &&
    input.down === down &&
    input.left === left &&
    input.right === right
  ) {
    return;
  }
  input.up = up;
  input.down = down;
  input.left = left;
  input.right = right;
  sendInput();
}

function bindJoystickControls() {
  const base = document.querySelector(".joystick__base");
  const knob = document.querySelector(".joystick__knob");
  const deadZone = 0.18;
  let activePointer = null;

  function geometry() {
    const rect = base.getBoundingClientRect();
    const maxRadius = rect.width / 2 - knob.offsetWidth / 2;
    return {
      centerX: rect.left + rect.width / 2,
      centerY: rect.top + rect.height / 2,
      maxRadius,
    };
  }

  function resetKnob() {
    knob.style.transform = "translate(-50%, -50%)";
    activePointer = null;
  }

  resetJoystick = resetKnob;

  function updateFromPointer(clientX, clientY) {
    const { centerX, centerY, maxRadius } = geometry();
    let dx = clientX - centerX;
    let dy = clientY - centerY;
    const distance = Math.hypot(dx, dy);
    if (distance > maxRadius) {
      dx = (dx / distance) * maxRadius;
      dy = (dy / distance) * maxRadius;
    }

    knob.style.transform = `translate(calc(-50% + ${dx}px), calc(-50% + ${dy}px))`;

    if (distance / maxRadius < deadZone) {
      setJoystickDirections(false, false, false, false);
      return;
    }

    const axisDead = maxRadius * 0.22;
    setJoystickDirections(
      dy < -axisDead,
      dy > axisDead,
      dx < -axisDead,
      dx > axisDead
    );
  }

  function endPointer(event) {
    if (activePointer !== null && event.pointerId !== activePointer) {
      return;
    }
    resetKnob();
    setJoystickDirections(false, false, false, false);
  }

  base.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    activePointer = event.pointerId;
    base.setPointerCapture(event.pointerId);
    updateFromPointer(event.clientX, event.clientY);
  });

  base.addEventListener("pointermove", (event) => {
    if (event.pointerId !== activePointer) {
      return;
    }
    event.preventDefault();
    updateFromPointer(event.clientX, event.clientY);
  });

  base.addEventListener("pointerup", endPointer);
  base.addEventListener("pointercancel", endPointer);
  base.addEventListener("lostpointercapture", endPointer);
}

function bindInputSafetyControls() {
  window.addEventListener("blur", clearInput);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      clearInput();
    }
  });
}

function setStatus(status, attempt = 0) {
  const element = document.getElementById("status");
  if (status === "connecting") {
    element.textContent = "连接中";
  } else if (status === "connected") {
    element.textContent = "已连接";
  } else if (status === "reconnecting") {
    element.textContent = `连接已断开，正在重连（第 ${attempt} 次）`;
  } else if (status === "disconnected") {
    element.textContent = "已登出";
  }
  element.className = `status status--${status}`;
}

function bindAuthControls() {
  document.getElementById("auth-form").addEventListener("submit", handleAuthSubmit);
  document.getElementById("auth-toggle-link").addEventListener("click", toggleAuthMode);
}

function bindAccountControls() {
  const trigger = document.getElementById("account-menu-trigger");
  trigger.addEventListener("click", (event) => {
    event.stopPropagation();
    toggleAccountMenu();
  });

  document.getElementById("account-edit-appearance").addEventListener("click", () => {
    openAppearanceEditor();
  });
  document.getElementById("account-logout").addEventListener("click", logout);
  document.getElementById("appearance-save").addEventListener("click", saveAppearance);
  document.getElementById("appearance-cancel").addEventListener("click", cancelAppearanceEdit);

  document.getElementById("appearance-color").addEventListener("input", (event) => {
    updateDraftAppearance({
      color: event.target.value,
      shape: draftAppearance.shape,
    });
  });

  for (const button of document.querySelectorAll(".appearance-editor__shape")) {
    button.addEventListener("click", () => {
      updateDraftAppearance({
        color: draftAppearance.color,
        shape: button.dataset.shape,
      });
    });
  }

  document.addEventListener("click", (event) => {
    const account = document.getElementById("account-ctrl");
    if (!account.contains(event.target)) {
      closeAccountMenu();
      if (editorOpen) {
        closeAppearanceEditor();
      }
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closeAccountMenu();
      if (editorOpen) {
        cancelAppearanceEdit();
      }
    }
  });
}

window.mapWalker = { logout };
