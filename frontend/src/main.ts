/** Fernsicht frontend entry point — WebRTC P2P progress tracking (V2 HTTP signaling). */

import { ViewerPeer, SenderPeer } from "./peer";
import { createSession } from "./signaling";
import {
  parseMessage,
  serializeEnd,
  serializeProgress,
  serializeStart,
} from "./protocol";
import {
  appendBroadcasterLog,
  completeProgressBar,
  createProgressBar,
  setConnectionDetail,
  setConnectionStatus,
  setPeerId,
  setRoomId,
  showBroadcasterView,
  showLanding,
  showViewerView,
  updateProgressBar,
} from "./ui";

interface FragmentParams {
  room: string | null;
  role: "broadcaster" | "viewer";
}

function parseFragment(): FragmentParams | null {
  const hash = window.location.hash.slice(1);
  if (!hash) return null;

  const params = new URLSearchParams(hash);
  const roleParam = params.get("role");
  if (!roleParam) return null;

  const role: "broadcaster" | "viewer" =
    roleParam === "broadcaster" ? "broadcaster" : "viewer";
  const room = params.get("room") || null;

  // Viewer requires a room ID; broadcaster creates its own
  if (role === "viewer" && !room) return null;

  return { room, role };
}

function getServerUrl(): string {
  const url = import.meta.env.VITE_SERVER_URL;
  if (!url) {
    throw new Error(
      "VITE_SERVER_URL is not set. Copy .env.example to .env and configure it.",
    );
  }
  return url as string;
}

// --- Viewer ---

function startViewer(serverUrl: string, roomId: string): void {
  showViewerView();
  setRoomId(roomId);
  setConnectionStatus("connecting");
  setConnectionDetail("Creating offer and contacting server...", "info");

  const peer = new ViewerPeer(serverUrl, roomId, {
    onOpen: () => {
      setConnectionStatus("connected");
      setConnectionDetail("Connected. Waiting for live updates...", "info");
    },
    onSignalingError: (_code, message, fatal) => {
      setConnectionStatus("signaling-error");
      setConnectionDetail(message, fatal ? "error" : "warning");
    },
    onMessage: (raw) => {
      try {
        const msg = parseMessage(raw);
        switch (msg.kind) {
          case "identity":
            setPeerId(msg.id);
            break;
          case "start":
            createProgressBar(msg.taskId, msg.label);
            break;
          case "progress":
            updateProgressBar(msg.taskId, msg.value, {
              elapsed: msg.elapsed,
              eta: msg.eta,
              n: msg.n,
              total: msg.total,
              rate: msg.rate,
              unit: msg.unit,
            });
            break;
          case "end":
            completeProgressBar(msg.taskId);
            break;
          case "keepalive":
          case "ready":
            break;
        }
      } catch (err) {
        console.error("Failed to parse message:", err, raw);
      }
    },
    onClose: () => {
      setConnectionStatus("disconnected");
      setConnectionDetail("Disconnected.", "warning");
    },
    onStateChange: (state) => {
      if (state === "connecting") {
        setConnectionStatus("connecting");
        setConnectionDetail("Creating offer and contacting server...", "info");
      } else if (state === "queued") {
        setConnectionStatus("connecting");
        setConnectionDetail("Waiting for sender to pick up handshake...", "info");
      } else if (state === "connected") {
        setConnectionStatus("connected");
        setConnectionDetail("Connected. Waiting for live updates...", "info");
      }
    },
  });

  peer.start();
}

// --- Broadcaster ---

async function startBroadcaster(serverUrl: string): Promise<void> {
  showBroadcasterView();
  setConnectionStatus("connecting");
  setConnectionDetail("Creating session...", "info");

  const mockBtn = document.getElementById("mock-btn") as HTMLButtonElement | null;
  if (mockBtn) mockBtn.disabled = true;

  let session;
  try {
    session = await createSession(serverUrl);
  } catch (err) {
    setConnectionStatus("signaling-error");
    setConnectionDetail(`Failed to create session: ${err}`, "error");
    return;
  }

  setRoomId(session.room_id);
  setConnectionDetail("Polling for viewers...", "info");
  appendBroadcasterLog(`Room: ${session.room_id}`);
  appendBroadcasterLog(`Viewer URL: ${session.viewer_url}`);

  const peer = new SenderPeer(
    serverUrl,
    session.room_id,
    session.sender_secret,
    session.poll_interval_hint * 1000,
    {
      onOpen: () => {
        setConnectionStatus("connected");
        setConnectionDetail("Viewer connected. DataChannel open.", "info");
        appendBroadcasterLog("DataChannel open — ready to send");
        if (mockBtn) mockBtn.disabled = false;
      },
      onSignalingError: (_code, message, fatal) => {
        setConnectionStatus("signaling-error");
        setConnectionDetail(message, fatal ? "error" : "warning");
      },
      onMessage: (raw) => {
        appendBroadcasterLog(`< ${raw}`);
      },
      onClose: () => {
        setConnectionStatus("disconnected");
        setConnectionDetail("Viewer disconnected.", "warning");
        if (mockBtn) mockBtn.disabled = true;
      },
      onStateChange: (state) => {
        appendBroadcasterLog(`State: ${state}`);
        if (state === "connecting") {
          setConnectionStatus("connecting");
          setConnectionDetail("Handshaking with viewer...", "info");
        } else if (state === "signaling-joined") {
          setConnectionStatus("connecting");
          setConnectionDetail("Polling for viewers...", "info");
        }
      },
    },
  );

  peer.start();

  if (mockBtn) {
    mockBtn.addEventListener("click", () => runMockSimulation(peer));
  }
}

function runMockSimulation(peer: SenderPeer): void {
  const taskCount = 1 + Math.floor(Math.random() * 3);
  const labels = [
    "Training model",
    "Downloading dataset",
    "Processing images",
    "Compiling shaders",
  ];

  const tasks: Array<{
    id: string;
    label: string;
    progress: number;
    rate: number;
    total: number;
    n: number;
    startTime: number;
  }> = [];

  const now = performance.now() / 1000;
  for (let i = 0; i < taskCount; i++) {
    const id = crypto.randomUUID().slice(0, 8);
    const label = labels[i % labels.length];
    const total = 200 + Math.floor(Math.random() * 800); // 200-1000 items
    const rate = 0.01 + Math.random() * 0.04;

    tasks.push({ id, label, progress: 0, rate, total, n: 0, startTime: now });

    const startMsg = serializeStart(id, label);
    peer.send(startMsg);
    appendBroadcasterLog(`> ${startMsg}`);
  }

  const interval = setInterval(() => {
    let allDone = true;
    const elapsed = performance.now() / 1000;

    for (const task of tasks) {
      if (task.progress >= 1) continue;
      allDone = false;

      task.progress = Math.min(1, task.progress + task.rate);
      task.n = Math.round(task.progress * task.total);

      if (task.progress >= 1) {
        task.n = task.total;
        const endMsg = serializeEnd(task.id);
        peer.send(endMsg);
        appendBroadcasterLog(`> ${endMsg}`);
      } else {
        const taskElapsed = elapsed - task.startTime;
        const itemRate = taskElapsed > 0 ? task.n / taskElapsed : 0;
        const remaining = task.total - task.n;
        const eta = itemRate > 0 ? remaining / itemRate : 0;

        const pMsg = serializeProgress(task.id, task.progress, {
          elapsed: taskElapsed,
          eta,
          n: task.n,
          total: task.total,
          rate: itemRate,
          unit: "it",
        });
        peer.send(pMsg);
        appendBroadcasterLog(`> ${pMsg}`);
      }
    }

    if (allDone) clearInterval(interval);
  }, 200);
}

// --- Demo animation for landing page ---

function startDemoAnimation(): void {
  const demos = [
    { idx: 1, total: 1200, unit: "epochs", rate: 18.3, delay: 0 },
    { idx: 2, total: 8400, unit: "files", rate: 142.0, delay: 2 },
    { idx: 3, total: 3600, unit: "it", rate: 55.2, delay: 4 },
  ];

  const CYCLE_SEC = 10;
  const FILL_FRAC = 0.85; // bar fills in 85% of cycle, rest is pause

  function update() {
    const now = performance.now() / 1000;

    for (const d of demos) {
      const t = ((now - d.delay) % CYCLE_SEC) / CYCLE_SEC;
      const progress = Math.min(1, t / FILL_FRAC);

      const pctEl = document.querySelector(`.demo-pct-${d.idx}`) as HTMLElement | null;
      const statsEl = document.querySelector(`.demo-stats-${d.idx}`) as HTMLElement | null;
      if (!pctEl || !statsEl) continue;

      const pct = Math.round(progress * 100);
      pctEl.textContent = `${pct}%`;

      const n = Math.round(progress * d.total);
      const elapsed = progress * (d.total / d.rate);
      const remaining = d.total - n;
      const eta = d.rate > 0 ? remaining / d.rate : 0;

      const spans = statsEl.querySelectorAll("span");
      if (spans.length >= 4) {
        spans[0].textContent = `${n.toLocaleString()} / ${d.total.toLocaleString()} ${d.unit}`;
        spans[1].textContent = `${d.rate.toFixed(1)} ${d.unit}/s`;
        spans[2].textContent = formatDemoTime(elapsed);
        spans[3].textContent = progress >= 1 ? "" : `~${formatDemoTime(eta)} left`;
      }
    }

    requestAnimationFrame(update);
  }

  requestAnimationFrame(update);
}

function formatDemoTime(seconds: number): string {
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${s % 60}s`;
}

// --- Entry ---

function main(): void {
  const params = parseFragment();
  if (!params) {
    showLanding();
    startDemoAnimation();
    return;
  }

  const serverUrl = getServerUrl();
  if (params.role === "broadcaster") {
    startBroadcaster(serverUrl);
  } else {
    startViewer(serverUrl, params.room!);
  }
}

main();
