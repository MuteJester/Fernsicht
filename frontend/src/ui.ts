/** DOM rendering for Fernsicht — supports multiple concurrent progress bars. */

const $ = (id: string) => document.getElementById(id)!;
const COMPLETED_BAR_REMOVE_DELAY_MS = 5000;
const MAX_LOG_LINES = 400;

// --- View switching ---

export function showLanding(): void {
  $("landing").classList.remove("hidden");
  $("header").classList.add("hidden");
  $("broadcaster-view").classList.add("hidden");
  $("viewer-view").classList.add("hidden");
  setConnectionDetail(null);
}

export function showBroadcasterView(): void {
  $("landing").classList.add("hidden");
  $("header").classList.remove("hidden");
  $("broadcaster-view").classList.remove("hidden");
  $("viewer-view").classList.add("hidden");
  setConnectionDetail(null);
}

export function showViewerView(): void {
  $("landing").classList.add("hidden");
  $("header").classList.remove("hidden");
  $("broadcaster-view").classList.add("hidden");
  $("viewer-view").classList.remove("hidden");
  setConnectionDetail(null);
  updateViewerEmptyState();
}

// --- Connection status ---

export function setConnectionStatus(
  status: "connecting" | "connected" | "disconnected" | "signaling-error",
): void {
  const el = $("connection-status");
  el.textContent = status;
  el.className = `status ${status}`;
}

export function setRoomId(roomId: string): void {
  $("room-id").textContent = `Room: ${roomId}`;
}

export function setPeerId(id: string): void {
  $("peer-id").textContent = `Peer: ${id}`;
}

export function setConnectionDetail(
  message: string | null,
  tone: "info" | "warning" | "error" = "info",
): void {
  const el = $("connection-detail");
  if (message === null || message.trim() === "") {
    el.textContent = "";
    el.className = "connection-detail hidden";
    return;
  }

  el.textContent = message;
  el.className = `connection-detail ${tone}`;
}

// --- Viewer: multi-bar progress rendering ---

const activeBars = new Map<string, HTMLElement>();

export function createProgressBar(taskId: string, label: string): void {
  const container = $("bars-container");
  const existing = activeBars.get(taskId);
  if (existing) {
    existing.remove();
    activeBars.delete(taskId);
  }

  const bar = document.createElement("div");
  bar.className = "task-bar";
  bar.dataset.taskId = taskId;
  bar.innerHTML = `
    <div class="task-label">${escapeHtml(label)}</div>
    <div class="progress-container">
      <div class="progress-bar">
        <div class="progress-fill" style="width: 0%"></div>
      </div>
      <span class="progress-pct">0%</span>
    </div>
  `;

  container.appendChild(bar);
  activeBars.set(taskId, bar);
  updateViewerEmptyState();
}

export function updateProgressBar(taskId: string, value: number): void {
  const bar = activeBars.get(taskId);
  if (!bar) return;

  const percent = Math.min(100, Math.round(value * 100));
  const fill = bar.querySelector(".progress-fill") as HTMLElement;
  const pct = bar.querySelector(".progress-pct") as HTMLElement;

  fill.style.width = `${percent}%`;
  pct.textContent = `${percent}%`;
}

export function completeProgressBar(taskId: string): void {
  const bar = activeBars.get(taskId);
  if (!bar) return;

  const fill = bar.querySelector(".progress-fill") as HTMLElement;
  const pct = bar.querySelector(".progress-pct") as HTMLElement;

  fill.style.width = "100%";
  fill.classList.add("done");
  pct.textContent = "100%";
  bar.classList.add("completed");

  setTimeout(() => {
    if (activeBars.get(taskId) !== bar) return;
    activeBars.delete(taskId);
    bar.remove();
    updateViewerEmptyState();
  }, COMPLETED_BAR_REMOVE_DELAY_MS);
}

// --- Broadcaster: log ---

export function appendBroadcasterLog(message: string): void {
  const log = $("broadcaster-log");
  const line = document.createElement("div");
  line.className = "log-line";
  line.textContent = message;
  log.appendChild(line);

  while (log.childElementCount > MAX_LOG_LINES) {
    log.removeChild(log.firstElementChild!);
  }
  log.scrollTop = log.scrollHeight;
}

// --- Utility ---

function escapeHtml(text: string): string {
  const div = document.createElement("div");
  div.textContent = text;
  return div.innerHTML;
}

function updateViewerEmptyState(): void {
  const empty = $("viewer-empty");
  empty.classList.toggle("hidden", activeBars.size > 0);
}
