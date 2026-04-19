/** Fernsicht frontend entry point — WebRTC P2P progress viewer. */

import { ViewerPeer } from "./peer";
import { parseMessage, serializeHello } from "./protocol";
import {
  completeProgressBar,
  createProgressBar,
  getLocalViewerName,
  setConnectionDetail,
  setConnectionPhase,
  setConnectionStatus,
  setPeerId,
  setPresence,
  setRoomId,
  showLanding,
  showViewerView,
  updateProgressBar,
} from "./ui";

interface FragmentParams {
  room: string;
}

function parseFragment(): FragmentParams | null {
  const hash = window.location.hash.slice(1);
  if (!hash) return null;

  const params = new URLSearchParams(hash);
  const room = params.get("room");
  if (!room) return null;

  return { room };
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
  setConnectionDetail("Reaching the rendezvous server…", "info");
  setConnectionPhase("contacting-server");

  const peer = new ViewerPeer(serverUrl, roomId, {
    onOpen: () => {
      setConnectionStatus("connected");
      setConnectionDetail("Connected. Waiting for live updates…", "info");
      // Identify ourselves so the sender includes us in its presence
      // broadcasts. Safe to call before any other frames — the sender
      // handles HELLO on message receipt, not in a specific order.
      peer.send(serializeHello(getLocalViewerName()));
    },
    onSignalingError: (_code, message, fatal) => {
      setConnectionStatus("signaling-error");
      setConnectionDetail(message, fatal ? "error" : "warning");
    },
    onPhase: (phase) => {
      setConnectionPhase(phase);
      // Update the subtitle to mirror the stepper for screen readers
      // and small viewports where the stepper might wrap awkwardly.
      switch (phase) {
        case "contacting-server":
          setConnectionDetail("Reaching the rendezvous server…", "info");
          break;
        case "queued":
          setConnectionDetail(
            "Waiting for the sender to pick up your handshake…", "info");
          break;
        case "negotiating":
          setConnectionDetail("Negotiating peer-to-peer connection…", "info");
          break;
        case "connected":
          setConnectionDetail("Connected. Waiting for first frame…", "info");
          break;
        case "failed":
          setConnectionDetail("Connection failed. Try refreshing.", "error");
          break;
      }
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
          case "presence":
            setPresence(msg.names);
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
      // Header chip + footer signal indicator. The card stepper +
      // subtitle text are driven by onPhase above (more granular).
      if (state === "connecting" || state === "queued") {
        setConnectionStatus("connecting");
      } else if (state === "connected") {
        setConnectionStatus("connected");
      }
    },
  });

  peer.start();
}

// --- Demo animation for landing page ---

function startDemoAnimation(): void {
  const tasks = [
    { label: "Training model",      total: 1200, rate: 18.3,  unit: "ep" },
    { label: "Downloading dataset", total: 8400, rate: 142.0, unit: "files" },
    { label: "Processing images",   total: 3600, rate: 55.2,  unit: "it" },
  ];

  const CYCLE_SEC = 10;
  const FILL_FRAC = 0.88; // reach 100% at 88% of cycle, hold briefly

  const titleEl    = document.getElementById("demo-title");
  const subtitleEl = document.getElementById("demo-subtitle");
  const pctNumEl   = document.getElementById("demo-pct-num");
  const wrapEl     = document.getElementById("demo-percent-wrap");
  const fillEl     = document.getElementById("demo-fill") as HTMLElement | null;
  const dotEl      = document.getElementById("demo-dot") as HTMLElement | null;
  const rateEl     = document.getElementById("demo-rate");
  const unitEl     = document.getElementById("demo-unit");
  const elapsedEl  = document.getElementById("demo-elapsed");
  const etaEl      = document.getElementById("demo-eta");

  if (!titleEl || !fillEl || !dotEl) return;

  let taskIdx = 0;
  let cycleStart = performance.now() / 1000;

  function applyTask(idx: number) {
    const t = tasks[idx];
    if (titleEl) titleEl.textContent = t.label;
    if (unitEl) unitEl.textContent = `${t.unit}/s`;
    if (rateEl) rateEl.textContent = t.rate >= 10 ? t.rate.toFixed(0) : t.rate.toFixed(1);
  }
  applyTask(taskIdx);

  function tick() {
    const now = performance.now() / 1000;
    let cycleElapsed = now - cycleStart;
    if (cycleElapsed >= CYCLE_SEC) {
      cycleStart = now;
      taskIdx = (taskIdx + 1) % tasks.length;
      applyTask(taskIdx);
      cycleElapsed = 0;
    }

    const t = tasks[taskIdx];
    const progress = Math.min(1, cycleElapsed / CYCLE_SEC / FILL_FRAC);
    const pct = Math.round(progress * 100);
    const n = Math.round(progress * t.total);
    const totalElapsed = progress * (t.total / t.rate);
    const remaining = t.total - n;
    const etaSec = t.rate > 0 ? remaining / t.rate : 0;

    if (pctNumEl) pctNumEl.textContent = String(pct);
    if (wrapEl) wrapEl.dataset.progressTier = demoTier(pct);
    fillEl!.style.width = `${pct}%`;
    dotEl!.style.left = `${pct}%`;
    if (subtitleEl) subtitleEl.textContent = `${fmtNum(n)} / ${fmtNum(t.total)} ${t.unit}`;
    if (elapsedEl) elapsedEl.textContent = formatDemoTime(totalElapsed);
    if (etaEl) etaEl.textContent = progress >= 1 ? "done" : formatDemoTime(etaSec);

    requestAnimationFrame(tick);
  }

  requestAnimationFrame(tick);
}

function demoTier(pct: number): string {
  if (pct >= 100) return "done";
  if (pct >= 70) return "high";
  if (pct >= 35) return "mid";
  return "low";
}

function fmtNum(n: number): string {
  return n.toLocaleString("en-US").replace(/,/g, " ");
}

function formatDemoTime(seconds: number): string {
  const s = Math.round(seconds);
  if (s < 60) return `0:${String(s).padStart(2, "0")}`;
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
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
  startViewer(serverUrl, params.room);
}

main();
