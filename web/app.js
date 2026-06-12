const startPosition = { lat: 31.2304, lng: 121.4737 };
let currentUserId = null;
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

L.Icon.Default.imagePath = "/images/";

let resetJoystick = () => {};

bootstrap();
bindKeyboardControls();
bindJoystickControls();
bindInputSafetyControls();

async function bootstrap() {
  const session = await fetchSession();
  if (!session) {
    return;
  }
  currentUserId = session.userId;
  connect();
}

async function fetchSession() {
  const response = await fetch("/api/session");
  if (!response.ok) {
    return null;
  }
  return response.json();
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
  markers.forEach((marker) => marker.remove());
  markers.clear();
  setStatus("disconnected");
}

function scheduleReconnect() {
  if (!shouldReconnect) {
    return;
  }
  clearRetryTimer();
  retryAttempt += 1;
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
  for (const [id, marker] of markers.entries()) {
    if (!liveIds.has(id)) {
      marker.remove();
      markers.delete(id);
    }
  }
  updatePlayers(players);
}

function renderDelta(players, removedPlayerIds) {
  for (const playerIdToRemove of removedPlayerIds) {
    const marker = markers.get(playerIdToRemove);
    if (marker) {
      marker.remove();
      markers.delete(playerIdToRemove);
    }
  }
  updatePlayers(players);
}

function updatePlayers(players) {
  for (const player of players) {
    const marker = markers.get(player.id);
    const latLng = [player.lat, player.lng];
    if (marker) {
      marker.setLatLng(latLng);
    } else {
      const label = player.id === currentUserId ? "You" : "Player";
      markers.set(player.id, L.marker(latLng).addTo(map).bindTooltip(label));
    }

    if (player.id === currentUserId) {
      map.panTo(latLng, { animate: true });
    }
  }
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
    const direction = directions[event.key] || directions[event.key.toLowerCase()];
    if (!direction) {
      return;
    }
    event.preventDefault();
    setDirection(direction, true);
  });

  window.addEventListener("keyup", (event) => {
    const direction = directions[event.key] || directions[event.key.toLowerCase()];
    if (!direction) {
      return;
    }
    event.preventDefault();
    setDirection(direction, false);
  });
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

window.mapWalker = { logout };
