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
    <div class="task-header">
      <span class="task-label">${escapeHtml(label)}</span>
      <span class="progress-pct">0%</span>
    </div>
    <div class="progress-bar">
      <div class="progress-fill" style="width: 0%"></div>
    </div>
    <div class="task-stats">
      <span class="stat stat-count"></span>
      <span class="stat stat-rate"></span>
      <span class="stat stat-elapsed"></span>
      <span class="stat stat-eta"></span>
    </div>
  `;

  container.appendChild(bar);
  activeBars.set(taskId, bar);
  updateViewerEmptyState();
}

export function updateProgressBar(
  taskId: string,
  value: number,
  stats?: {
    elapsed: number | null;
    eta: number | null;
    n: number | null;
    total: number | null;
    rate: number | null;
    unit: string;
  },
): void {
  const bar = activeBars.get(taskId);
  if (!bar) return;

  const percent = Math.min(100, Math.round(value * 100));
  const fill = bar.querySelector(".progress-fill") as HTMLElement;
  const pct = bar.querySelector(".progress-pct") as HTMLElement;

  fill.style.width = `${percent}%`;
  fill.style.background = progressGradient(value);
  pct.textContent = `${percent}%`;

  if (stats) {
    const countEl = bar.querySelector(".stat-count") as HTMLElement;
    const rateEl = bar.querySelector(".stat-rate") as HTMLElement;
    const elapsedEl = bar.querySelector(".stat-elapsed") as HTMLElement;
    const etaEl = bar.querySelector(".stat-eta") as HTMLElement;

    if (stats.n !== null && stats.total !== null) {
      countEl.textContent = `${stats.n.toLocaleString()} / ${stats.total.toLocaleString()} ${stats.unit}`;
    } else if (stats.n !== null) {
      countEl.textContent = `${stats.n.toLocaleString()} ${stats.unit}`;
    }

    if (stats.rate !== null) {
      rateEl.textContent = `${stats.rate >= 10 ? stats.rate.toFixed(0) : stats.rate.toFixed(1)} ${stats.unit}/s`;
    }

    if (stats.elapsed !== null) {
      elapsedEl.textContent = formatDuration(stats.elapsed);
    }

    if (stats.eta !== null) {
      etaEl.textContent = `~${formatDuration(stats.eta)} left`;
    } else {
      etaEl.textContent = "";
    }
  }
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

function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

function progressGradient(t: number): string {
  // t: 0.0 → 1.0
  // 0.0  = #30363d (dark grey)
  // 0.5  = #238636 (mid green)
  // 1.0  = #3fb950 (bright green)
  const clamp = Math.max(0, Math.min(1, t));

  let r: number, g: number, b: number;
  if (clamp < 0.5) {
    const p = clamp / 0.5;
    r = Math.round(lerp(0x30, 0x23, p));
    g = Math.round(lerp(0x36, 0x86, p));
    b = Math.round(lerp(0x3d, 0x36, p));
  } else {
    const p = (clamp - 0.5) / 0.5;
    r = Math.round(lerp(0x23, 0x3f, p));
    g = Math.round(lerp(0x86, 0xb9, p));
    b = Math.round(lerp(0x36, 0x50, p));
  }

  return `rgb(${r}, ${g}, ${b})`;
}

function formatDuration(seconds: number): string {
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  if (m < 60) return `${m}m ${rem}s`;
  const h = Math.floor(m / 60);
  const remM = m % 60;
  return `${h}h ${remM}m`;
}

function escapeHtml(text: string): string {
  const div = document.createElement("div");
  div.textContent = text;
  return div.innerHTML;
}

function updateViewerEmptyState(): void {
  const empty = $("viewer-empty");
  empty.classList.toggle("hidden", activeBars.size > 0);
}
