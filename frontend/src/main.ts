/** Fernsicht app.html entry — viewer-only runtime. */

import { ViewerPeer } from "./peer";
import { parseMessage, serializeHello } from "./protocol";
import {
  completeProgressBar,
  createProgressBar,
  getLocalViewerName,
  initStatsPolling,
  logEvent,
  markKeepalive,
  maybeShowCompletionOnClose,
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

function startViewer(serverUrl: string, roomId: string): void {
  showViewerView();
  setRoomId(roomId);
  setConnectionStatus("connecting");
  setConnectionDetail("Reaching the rendezvous server… this usually takes a few seconds.", "info");
  setConnectionPhase("contacting-server");
  logEvent(`session <b>opened</b> · room ${roomId.slice(0, 8)}`);

  const peer = new ViewerPeer(serverUrl, roomId, {
    onOpen: () => {
      setConnectionStatus("connected");
      setConnectionDetail("Connected. Waiting for live updates…", "info");
      peer.send(serializeHello(getLocalViewerName()));
      logEvent(`handshake <em>completed</em> · direct p2p`);
      initStatsPolling(peer.connection);
    },
    onSignalingError: (_code, message, fatal) => {
      setConnectionStatus("signaling-error");
      setConnectionDetail(message, fatal ? "error" : "warning");
      logEvent(`signalling <b>error</b> · ${escapeForLog(message)}`);
    },
    onPhase: (phase) => {
      setConnectionPhase(phase);
      switch (phase) {
        case "contacting-server":
          setConnectionDetail("Reaching the rendezvous server… this usually takes a few seconds.", "info");
          break;
        case "queued":
          setConnectionDetail(
            "Waiting for sender check-in… auto-retrying in the background. No need to refresh.", "info");
          logEvent("ticket <b>queued</b> · waiting for sender poll");
          break;
        case "negotiating":
          setConnectionDetail("Negotiating peer-to-peer path… keep this tab open while ICE completes.", "info");
          logEvent("offer / answer <b>exchanged</b>");
          break;
        case "connected":
          setConnectionDetail("Connected. Waiting for first progress frame…", "info");
          logEvent("direct channel <b>open</b>");
          break;
        case "failed":
          setConnectionDetail("Connection setup failed after retries. Refresh once to request a new ticket.", "error");
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
            markKeepalive();
            break;
          case "ready":
            break;
        }
      } catch (err) {
        console.error("Failed to parse message:", err, raw);
      }
    },
    onClose: () => {
      setConnectionStatus("disconnected");
      setConnectionDetail("Disconnected. If it does not recover in a few seconds, refresh once.", "warning");
      logEvent("connection <b>closed</b>");
      // Safety net: if the sender closed the DataChannel without an
      // explicit END frame but the task had effectively completed,
      // show the Ko-fi prompt anyway.
      maybeShowCompletionOnClose();
    },
    onStateChange: (state) => {
      if (state === "connecting" || state === "queued") {
        setConnectionStatus("connecting");
      } else if (state === "connected") {
        setConnectionStatus("connected");
        setConnectionDetail("Connected. Receiving live updates.", "info");
      } else if (state === "disconnected") {
        setConnectionStatus("connecting");
        setConnectionDetail("Connection degraded — auto-recovering (up to ~30s)…", "warning");
        logEvent("connection <em>degraded</em> · waiting on recovery");
      }
    },
  });

  peer.start();
}

function escapeForLog(s: string): string {
  const div = document.createElement("div");
  div.textContent = s;
  return div.innerHTML;
}

function main(): void {
  const params = parseFragment();
  if (!params) {
    // No room fragment on app.html — the inline script in head should
    // already have bounced, but guard anyway.
    showLanding();
    return;
  }

  const serverUrl = getServerUrl();
  startViewer(serverUrl, params.room);
}

main();
