/** Fernsicht frontend entry point — WebRTC P2P progress tracking. */
import { FernsichtPeer } from "./peer";
import { parseMessage, serializeEnd, serializeProgress, serializeStart, } from "./protocol";
import { appendBroadcasterLog, completeProgressBar, createProgressBar, setConnectionStatus, setPeerId, setRoomId, showBroadcasterView, showLanding, showViewerView, updateProgressBar, } from "./ui";
function parseFragment() {
    const hash = window.location.hash.slice(1);
    if (!hash)
        return null;
    const params = new URLSearchParams(hash);
    const room = params.get("room");
    if (!room)
        return null;
    const roleParam = params.get("role");
    const role = roleParam === "broadcaster" ? "broadcaster" : "viewer";
    const senderToken = params.get("token")?.trim() || undefined;
    return { room, role, senderToken };
}
function toSignalingRole(role) {
    return role === "broadcaster" ? "SENDER" : "VIEWER";
}
function getSignalingUrl() {
    const url = import.meta.env.VITE_SIGNALING_URL;
    if (!url) {
        throw new Error("VITE_SIGNALING_URL is not set. Copy .env.example to .env and configure it.");
    }
    return url;
}
function getSenderJoinToken(fragmentToken) {
    if (fragmentToken && fragmentToken.length > 0) {
        return fragmentToken;
    }
    const token = import.meta.env.VITE_SIGNALING_SENDER_TOKEN;
    const trimmed = token?.trim();
    return trimmed && trimmed.length > 0 ? trimmed : undefined;
}
// --- Viewer ---
function startViewer(signalingUrl, roomId) {
    showViewerView();
    setRoomId(roomId);
    setConnectionStatus("connecting");
    const peer = new FernsichtPeer(signalingUrl, roomId, toSignalingRole("viewer"), {
        onOpen: () => setConnectionStatus("connected"),
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
            }
            catch (err) {
                console.error("Failed to parse message:", err, raw);
            }
        },
        onClose: () => setConnectionStatus("disconnected"),
        onStateChange: (state) => {
            if (state === "connecting")
                setConnectionStatus("connecting");
        },
    });
    peer.start();
}
// --- Broadcaster ---
function startBroadcaster(signalingUrl, roomId, senderJoinToken) {
    showBroadcasterView();
    setRoomId(roomId);
    setConnectionStatus("connecting");
    const mockBtn = document.getElementById("mock-btn");
    if (mockBtn)
        mockBtn.disabled = true;
    const peer = new FernsichtPeer(signalingUrl, roomId, toSignalingRole("broadcaster"), {
        onOpen: () => {
            setConnectionStatus("connected");
            appendBroadcasterLog("DataChannel open — ready to send");
            if (mockBtn)
                mockBtn.disabled = false;
        },
        onMessage: (raw) => {
            appendBroadcasterLog(`< ${raw}`);
        },
        onClose: () => {
            setConnectionStatus("disconnected");
            if (mockBtn)
                mockBtn.disabled = true;
        },
        onStateChange: (state) => {
            appendBroadcasterLog(`State: ${state}`);
        },
    }, { senderJoinToken });
    peer.start();
    if (mockBtn) {
        mockBtn.addEventListener("click", () => runMockSimulation(peer));
    }
}
function runMockSimulation(peer) {
    const taskCount = 1 + Math.floor(Math.random() * 3); // 1 to 3 tasks
    const labels = [
        "Training model",
        "Downloading dataset",
        "Processing images",
        "Compiling shaders",
    ];
    const tasks = [];
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
            if (task.progress >= 1)
                continue;
            allDone = false;
            task.progress = Math.min(1, task.progress + task.rate);
            if (task.progress >= 1) {
                const endMsg = serializeEnd(task.id);
                peer.send(endMsg);
                appendBroadcasterLog(`> ${endMsg}`);
            }
            else {
                const pMsg = serializeProgress(task.id, task.progress);
                peer.send(pMsg);
                appendBroadcasterLog(`> ${pMsg}`);
            }
        }
        if (allDone)
            clearInterval(interval);
    }, 200);
}
// --- Entry ---
function main() {
    const params = parseFragment();
    if (!params) {
        showLanding();
        return;
    }
    const signalingUrl = getSignalingUrl();
    if (params.role === "broadcaster") {
        startBroadcaster(signalingUrl, params.room, getSenderJoinToken(params.senderToken));
    }
    else {
        startViewer(signalingUrl, params.room);
    }
}
main();
