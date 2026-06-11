const startPosition = { lat: 31.2304, lng: 121.4737 };
const playerId = getOrCreatePlayerId();
const markers = new Map();
let currentPosition = { ...startPosition };
let socket = null;

const map = L.map("map", { zoomControl: true }).setView(
  [startPosition.lat, startPosition.lng],
  16
);

L.tileLayer("https://webrd0{s}.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=8&x={x}&y={y}&z={z}", {
  maxZoom: 18,
  subdomains: "1234",
  attribution: "&copy; 高德地图",
}).addTo(map);

L.Icon.Default.imagePath = "/images/";

connect();
bindKeyboardControls();
bindDpadControls();

function getOrCreatePlayerId() {
  const key = "map-walker-player-id";
  const existing = sessionStorage.getItem(key);
  if (existing) {
    return existing;
  }
  const created = `p-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
  sessionStorage.setItem(key, created);
  return created;
}

function connect() {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${protocol}//${window.location.host}/ws?playerId=${encodeURIComponent(playerId)}`;
  socket = new WebSocket(url);
  setStatus("connecting");

  socket.addEventListener("open", () => {
    setStatus("connected");
    sendPosition();
  });

  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (message.type === "players_snapshot") {
      renderPlayers(message.players);
    }
  });

  socket.addEventListener("close", () => {
    setStatus("disconnected");
  });

  socket.addEventListener("error", () => {
    setStatus("disconnected");
  });
}

function movePlayer(deltaLat, deltaLng) {
  currentPosition = {
    lat: currentPosition.lat + deltaLat,
    lng: currentPosition.lng + deltaLng,
  };
  map.panTo([currentPosition.lat, currentPosition.lng], { animate: true });
  sendPosition();
}

function sendPosition() {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  socket.send(
    JSON.stringify({
      type: "position_update",
      playerId,
      lat: currentPosition.lat,
      lng: currentPosition.lng,
    })
  );
}

function renderPlayers(players) {
  const liveIds = new Set(players.map((player) => player.id));

  for (const [id, marker] of markers.entries()) {
    if (!liveIds.has(id)) {
      marker.remove();
      markers.delete(id);
    }
  }

  for (const player of players) {
    const marker = markers.get(player.id);
    const label = player.id === playerId ? "You" : "Player";
    if (marker) {
      marker.setLatLng([player.lat, player.lng]);
    } else {
      markers.set(
        player.id,
        L.marker([player.lat, player.lng]).addTo(map).bindTooltip(label)
      );
    }
  }
}

function bindKeyboardControls() {
  window.addEventListener("keydown", (event) => {
    const step = 0.00015;
    if (event.key === "ArrowUp" || event.key.toLowerCase() === "w") {
      movePlayer(step, 0);
    } else if (event.key === "ArrowDown" || event.key.toLowerCase() === "s") {
      movePlayer(-step, 0);
    } else if (event.key === "ArrowLeft" || event.key.toLowerCase() === "a") {
      movePlayer(0, -step);
    } else if (event.key === "ArrowRight" || event.key.toLowerCase() === "d") {
      movePlayer(0, step);
    }
  });
}

function bindDpadControls() {
  // Input-normalization lesson: keyboard and mobile buttons both become calls
  // to movePlayer(). The server never needs to know which device created the
  // movement, similar to normalizing HTTP clients before business logic in a
  // Python backend.
  const step = 0.00015;
  const moves = {
    up: [step, 0],
    down: [-step, 0],
    left: [0, -step],
    right: [0, step],
  };

  for (const button of document.querySelectorAll("[data-move]")) {
    let timer = null;
    const direction = button.dataset.move;
    const [deltaLat, deltaLng] = moves[direction];

    const start = (event) => {
      event.preventDefault();
      movePlayer(deltaLat, deltaLng);
      timer = window.setInterval(() => movePlayer(deltaLat, deltaLng), 120);
    };
    const stop = () => {
      if (timer !== null) {
        window.clearInterval(timer);
        timer = null;
      }
    };

    button.addEventListener("pointerdown", start);
    button.addEventListener("pointerup", stop);
    button.addEventListener("pointercancel", stop);
    button.addEventListener("pointerleave", stop);
  }
}

function setStatus(status) {
  const element = document.getElementById("status");
  element.textContent = status.charAt(0).toUpperCase() + status.slice(1);
  element.className = `status status--${status}`;
}
