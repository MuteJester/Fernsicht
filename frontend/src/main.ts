/** Fernsicht frontend entry point — WebRTC P2P progress tracking. */

import { FernsichtPeer } from "./peer";
import {
  parseMessage,
  serializeEnd,
  serializeProgress,
  serializeStart,
} from "./protocol";
import type { Role } from "./signaling";
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
  room: string;
  role: "broadcaster" | "viewer";
  senderToken?: string;
}

function parseFragment(): FragmentParams | null {
  const hash = window.location.hash.slice(1);
  if (!hash) return null;

  const params = new URLSearchParams(hash);
  const room = params.get("room");
  if (!room) return null;

  const roleParam = params.get("role");
  const role: "broadcaster" | "viewer" =
    roleParam === "broadcaster" ? "broadcaster" : "viewer";

  const senderToken = params.get("token")?.trim() || undefined;

  return { room, role, senderToken };
}

function toSignalingRole(role: "broadcaster" | "viewer"): Role {
  return role === "broadcaster" ? "SENDER" : "VIEWER";
}

function viewerErrorMessage(code: string): string {
  if (code === "ROLE_TAKEN") {
    return "This room already has an active viewer. Close the other viewer tab and try again.";
  }
  if (code === "SERVER_BUSY") {
    return "The signaling node is currently busy. Retry in a few seconds.";
  }
  if (code === "VIEWER_CAPACITY") {
    return "This room reached its viewer limit. Try again when a viewer disconnects.";
  }
  if (code === "POLICY_VIOLATION") {
    return "The join request was rejected. Check that the link is complete and not expired.";
  }
  if (code === "WS_ERROR") {
    return "Network error while connecting to signaling. Retrying automatically.";
  }
  return "Connection to signaling failed.";
}

function broadcasterErrorMessage(code: string): string {
  if (code === "ROLE_TAKEN") {
    return "Another broadcaster is already active in this room.";
  }
  if (code === "SERVER_BUSY") {
    return "The signaling node is currently busy. Retry shortly.";
  }
  if (code === "POLICY_VIOLATION") {
    return "The broadcaster join was rejected. Check sender token/session settings.";
  }
  if (code === "WS_ERROR") {
    return "Network error while connecting to signaling. Retrying automatically.";
  }
  return "Signaling connection failed.";
}

function getSignalingUrl(): string {
  const url = import.meta.env.VITE_SIGNALING_URL;
  if (!url) {
    throw new Error(
      "VITE_SIGNALING_URL is not set. Copy .env.example to .env and configure it.",
    );
  }
  return url as string;
}

function getSenderJoinToken(fragmentToken?: string): string | undefined {
  if (fragmentToken && fragmentToken.length > 0) {
    return fragmentToken;
  }
  const token = import.meta.env.VITE_SIGNALING_SENDER_TOKEN as string | undefined;
  const trimmed = token?.trim();
  return trimmed && trimmed.length > 0 ? trimmed : undefined;
}

// --- Viewer ---

function startViewer(signalingUrl: string, roomId: string): void {
  showViewerView();
  setRoomId(roomId);
  setConnectionStatus("connecting");
  setConnectionDetail("Connecting to signaling...", "info");
  let signalingFatal = false;

  const peer = new FernsichtPeer(
    signalingUrl,
    roomId,
    toSignalingRole("viewer"),
    {
      onOpen: () => {
        signalingFatal = false;
        setConnectionStatus("connected");
        setConnectionDetail("Connected. Waiting for live updates...", "info");
      },
      onSignalingError: (code, _message, fatal) => {
        signalingFatal = fatal;
        setConnectionStatus("signaling-error");
        const tone =
          code === "WS_ERROR" || code === "SERVER_BUSY" ? "warning" : "error";
        setConnectionDetail(viewerErrorMessage(code), tone);
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
              updateProgressBar(msg.taskId, msg.value);
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
        if (signalingFatal) return;
        setConnectionStatus("disconnected");
        setConnectionDetail("Disconnected. Attempting reconnect...", "warning");
      },
      onStateChange: (state) => {
        if (signalingFatal && state === "signaling-closed") {
          return;
        }
        if (state === "connecting") {
          setConnectionStatus("connecting");
          setConnectionDetail("Connecting to signaling...", "info");
          return;
        }
        if (state === "signaling-joined") {
          setConnectionStatus("connecting");
          setConnectionDetail(
            "Signaling connected. Waiting for sender handshake...",
            "info",
          );
          return;
        }
        if (state === "connected") {
          setConnectionStatus("connected");
          setConnectionDetail("Connected. Waiting for live updates...", "info");
          return;
        }
        if (state === "signaling-closed") {
          setConnectionStatus("disconnected");
          setConnectionDetail("Signaling closed. Reconnecting...", "warning");
        }
      },
    },
  );

  peer.start();
}

// --- Broadcaster ---

function startBroadcaster(
  signalingUrl: string,
  roomId: string,
  senderJoinToken?: string,
): void {
  showBroadcasterView();
  setRoomId(roomId);
  setConnectionStatus("connecting");
  setConnectionDetail("Connecting to signaling...", "info");
  let signalingFatal = false;

  const mockBtn = document.getElementById("mock-btn") as HTMLButtonElement | null;
  if (mockBtn) mockBtn.disabled = true;

  const peer = new FernsichtPeer(
    signalingUrl,
    roomId,
    toSignalingRole("broadcaster"),
    {
      onOpen: () => {
        signalingFatal = false;
        setConnectionStatus("connected");
        setConnectionDetail("Connected. Viewer can now join this room.", "info");
        appendBroadcasterLog("DataChannel open — ready to send");
        if (mockBtn) mockBtn.disabled = false;
      },
      onSignalingError: (code, _message, fatal) => {
        signalingFatal = fatal;
        setConnectionStatus("signaling-error");
        const tone =
          code === "WS_ERROR" || code === "SERVER_BUSY" ? "warning" : "error";
        setConnectionDetail(broadcasterErrorMessage(code), tone);
      },
      onMessage: (raw) => {
        appendBroadcasterLog(`< ${raw}`);
      },
      onClose: () => {
        if (signalingFatal) return;
        setConnectionStatus("disconnected");
        setConnectionDetail("Disconnected. Attempting reconnect...", "warning");
        if (mockBtn) mockBtn.disabled = true;
      },
      onStateChange: (state) => {
        appendBroadcasterLog(`State: ${state}`);
        if (signalingFatal && state === "signaling-closed") {
          return;
        }
        if (state === "connecting") {
          setConnectionStatus("connecting");
          setConnectionDetail("Connecting to signaling...", "info");
          return;
        }
        if (state === "signaling-joined") {
          setConnectionStatus("connecting");
          setConnectionDetail("Waiting for viewer to connect...", "info");
          return;
        }
        if (state === "signaling-closed") {
          setConnectionStatus("disconnected");
          setConnectionDetail("Signaling closed. Reconnecting...", "warning");
        }
      },
    },
    { senderJoinToken },
  );

  peer.start();

  if (mockBtn) {
    mockBtn.addEventListener("click", () => runMockSimulation(peer));
  }
}

function runMockSimulation(peer: FernsichtPeer): void {
  const taskCount = 1 + Math.floor(Math.random() * 3); // 1 to 3 tasks
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
  }> = [];

  for (let i = 0; i < taskCount; i++) {
    const id = crypto.randomUUID().slice(0, 8);
    const label = labels[i % labels.length];
    const rate = 0.01 + Math.random() * 0.04; // 1-5% per tick

    tasks.push({ id, label, progress: 0, rate });

    const startMsg = serializeStart(id, label);
    peer.send(startMsg);
    appendBroadcasterLog(`> ${startMsg}`);
  }

  const interval = setInterval(() => {
    let allDone = true;

    for (const task of tasks) {
      if (task.progress >= 1) continue;
      allDone = false;

      task.progress = Math.min(1, task.progress + task.rate);

      if (task.progress >= 1) {
        const endMsg = serializeEnd(task.id);
        peer.send(endMsg);
        appendBroadcasterLog(`> ${endMsg}`);
      } else {
        const pMsg = serializeProgress(task.id, task.progress);
        peer.send(pMsg);
        appendBroadcasterLog(`> ${pMsg}`);
      }
    }

    if (allDone) clearInterval(interval);
  }, 200);
}

// --- Entry ---

function main(): void {
  const params = parseFragment();
  if (!params) {
    showLanding();
    return;
  }

  const signalingUrl = getSignalingUrl();
  if (params.role === "broadcaster") {
    startBroadcaster(
      signalingUrl,
      params.room,
      getSenderJoinToken(params.senderToken),
    );
  } else {
    startViewer(signalingUrl, params.room);
  }
}

main();
