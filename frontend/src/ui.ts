/** DOM rendering for Fernsicht viewer — single observation card. */

const $ = (id: string) => document.getElementById(id)!;

// --- DOM accessors (lazy; DOM always present, `hidden` class toggles) ---
const el = {
  landing:        () => $("landing"),
  viewer:         () => $("viewer-view"),
  siteFooter:     () => document.getElementById("site-footer"),
  completionNote: () => $("completion-note"),
  status:         () => $("connection-status"),
  statusLabel:   () => $("connection-label"),
  roomLabel:     () => $("room-label"),
  peerValue:     () => $("peer-value"),
  signalText:    () => $("signal-text"),
  signalDot:     () => $("signal-dot"),
  obsCard:        () => $("observation-card"),
  stepper:        () => $("connect-stepper"),
  stepCountdown:  () => $("connect-step-countdown"),
  title:         () => $("obs-title"),
  subtitle:      () => $("obs-subtitle"),
  percent:       () => $("obs-percent"),
  percentWrap:   () => $("percent-wrap"),
  fill:          () => $("obs-fill"),
  dot:           () => $("obs-dot"),
  startClock:    () => $("obs-start"),
  etaClock:      () => $("obs-eta-clock"),
  statEta:       () => $("stat-eta"),
  statRate:      () => $("stat-rate"),
  statRateUnit:  () => $("stat-rate-unit"),
  statItems:     () => $("stat-items"),
  statTotal:     () => $("stat-total"),
  statElapsed:   () => $("stat-elapsed"),
  copyBtn:       () => $("copy-link-btn") as HTMLButtonElement,
  viewersCount:  () => $("viewers-count"),
  viewersList:   () => $("viewers-list"),
};

let activeTaskId: string | null = null;
let viewerInited = false;

interface ObsState {
  startedAt: number;
}
let obs: ObsState | null = null;
let completedResetTimer: ReturnType<typeof setTimeout> | null = null;
let completionNoteFadeTimer: ReturnType<typeof setTimeout> | null = null;
const COMPLETION_HOLD_MS = 15000;

// --- View switching -------------------------------------------------------

export function showLanding(): void {
  el.landing().classList.remove("hidden");
  el.viewer().classList.add("hidden");
  el.siteFooter()?.classList.remove("hidden");
}

export function showViewerView(): void {
  el.landing().classList.add("hidden");
  el.viewer().classList.remove("hidden");
  // The viewer has its own support pill (header) and contextual ask on
  // completion; the fixed Ko-fi bar is landing-only.
  el.siteFooter()?.classList.add("hidden");
  initViewerOnce();
}

// --- Connection status ----------------------------------------------------

type Status = "connecting" | "connected" | "disconnected" | "signaling-error";

export function setConnectionStatus(status: Status): void {
  const s = el.status();
  const label = el.statusLabel();
  const sDot = el.signalDot();
  const sText = el.signalText();
  s.className = "status";

  switch (status) {
    case "connecting":
      s.classList.add("status--connecting");
      label.textContent = "Connecting";
      sDot.className = "signal-dot signal-dot--amber";
      sText.textContent = "Connecting…";
      break;
    case "connected":
      s.classList.add("status--live");
      label.textContent = activeTaskId ? "Live" : "Standby";
      sDot.className = "signal-dot";
      sText.textContent = "Signal stable";
      break;
    case "disconnected":
      s.classList.add("status--disconnected");
      label.textContent = "Offline";
      sDot.className = "signal-dot signal-dot--red";
      sText.textContent = "Disconnected";
      break;
    case "signaling-error":
      s.classList.add("status--error");
      label.textContent = "Error";
      sDot.className = "signal-dot signal-dot--red";
      sText.textContent = "Signaling error";
      break;
  }
}

export function setRoomId(roomId: string): void {
  const short = roomId.length > 12 ? roomId.slice(0, 8) : roomId;
  el.roomLabel().textContent = short;
}

export function setPeerId(id: string): void {
  el.peerValue().textContent = id;
}

export function setConnectionDetail(
  message: string | null,
  _tone: "info" | "warning" | "error" = "info",
): void {
  // Surface pre-handshake messages as the card subtitle. Once a task is
  // active, preserve the task's own subtitle.
  if (activeTaskId) return;
  el.subtitle().textContent = message ?? "";
}

// --- Connection-phase stepper --------------------------------------------
//
// Visible only while the viewer is mid-handshake. The stepper sits in
// place of the percent / horizon / stats block; once the first START
// frame arrives, createProgressBar() reveals the real progress UI.

export type ConnectionPhase =
  | "contacting-server"
  | "queued"
  | "negotiating"
  | "connected"
  | "failed";

const STEP_ORDER: ConnectionPhase[] = [
  "contacting-server",
  "queued",
  "negotiating",
  "connected",
];

// Worst-case sender poll interval. The bridge defaults to 25s; SDKs
// may set their own. Surface this as a fixed cycle so the user sees
// motion instead of a static "any second now" message.
const SENDER_POLL_HINT_SEC = 25;

let countdownTimer: ReturnType<typeof setInterval> | null = null;
let countdownRemaining = SENDER_POLL_HINT_SEC;

export function setConnectionPhase(phase: ConnectionPhase): void {
  // Clear any active countdown — it only runs during the queued phase.
  stopCountdown();

  const stepper = el.stepper();
  if (phase === "failed") {
    // Mark whichever step was current as failed, leave others as-is.
    for (const step of stepper.children) {
      if (step.classList.contains("is-active")) {
        step.classList.remove("is-active");
        step.classList.add("is-failed");
      }
    }
    return;
  }

  const idx = STEP_ORDER.indexOf(phase);
  if (idx < 0) return;

  for (let i = 0; i < STEP_ORDER.length; i++) {
    const step = stepper.children[i] as HTMLElement | undefined;
    if (!step) continue;
    step.classList.remove("is-pending", "is-active", "is-done", "is-failed");
    if (i < idx)        step.classList.add("is-done");
    else if (i === idx) step.classList.add("is-active");
    else                step.classList.add("is-pending");
  }

  if (phase === "queued") startQueuedCountdown();
  else                    el.stepCountdown().textContent = "";
}

function startQueuedCountdown(): void {
  countdownRemaining = SENDER_POLL_HINT_SEC;
  renderCountdown();
  countdownTimer = setInterval(() => {
    countdownRemaining -= 1;
    if (countdownRemaining <= 0) countdownRemaining = SENDER_POLL_HINT_SEC;
    renderCountdown();
  }, 1000);
}

function renderCountdown(): void {
  el.stepCountdown().textContent =
    `Sender polls every ~${SENDER_POLL_HINT_SEC}s · next check-in in ${countdownRemaining}s`;
}

function stopCountdown(): void {
  if (countdownTimer !== null) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
  el.stepCountdown().textContent = "";
}

/** Hide the connecting stepper and reveal the progress UI.
 *  Called from createProgressBar(), but also exposed in case the
 *  caller wants to reveal early (e.g., on dc.onopen for a non-task
 *  smoke). */
export function revealProgressUI(): void {
  el.obsCard().classList.remove("is-connecting");
  stopCountdown();
}

// --- Observation (single active task) -------------------------------------

export function createProgressBar(taskId: string, label: string): void {
  if (completedResetTimer) {
    clearTimeout(completedResetTimer);
    completedResetTimer = null;
  }
  hideCompletionNote();
  // First task frame — the handshake stepper has done its job; flip
  // the card over to the live progress view.
  revealProgressUI();

  activeTaskId = taskId;
  obs = { startedAt: Date.now() };

  el.title().textContent = label;
  el.subtitle().textContent = "Starting…";
  el.percent().textContent = "0";
  el.percentWrap().dataset.progressTier = "low";
  el.fill().style.width = "0%";
  el.dot().style.left = "0%";
  el.startClock().textContent = formatClock(obs.startedAt);
  el.etaClock().textContent = "—";
  el.statEta().textContent = "—";
  el.statRate().textContent = "—";
  el.statRateUnit().textContent = "items/s";
  el.statItems().textContent = "—";
  el.statTotal().textContent = "—";
  el.statElapsed().textContent = "0:00";

  const lbl = el.statusLabel();
  if (lbl.textContent === "Standby") lbl.textContent = "Live";
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
  // Single-observation model: adopt an unseen task if none is active,
  // otherwise ignore background-task updates.
  if (!activeTaskId) createProgressBar(taskId, taskId);
  if (taskId !== activeTaskId) return;

  const pct = Math.max(0, Math.min(100, value * 100));
  el.percent().textContent = String(Math.floor(pct));
  el.fill().style.width = `${pct}%`;
  el.dot().style.left = `${pct}%`;
  el.percentWrap().dataset.progressTier = tierFor(pct);

  if (stats) {
    const unit = stats.unit || "items";
    el.statRateUnit().textContent = `${unit}/s`;

    if (stats.n !== null) el.statItems().textContent = fmtNum(stats.n);
    if (stats.total !== null) el.statTotal().textContent = fmtNum(stats.total);
    if (stats.rate !== null) {
      const r = stats.rate;
      el.statRate().textContent = r >= 10 ? r.toFixed(0) : r.toFixed(1);
    }
    if (stats.elapsed !== null) el.statElapsed().textContent = formatDuration(stats.elapsed);
    if (stats.eta !== null) {
      el.statEta().textContent = formatDuration(stats.eta);
      el.etaClock().textContent = formatClock(Date.now() + stats.eta * 1000);
    }

    if (stats.total !== null && stats.n !== null) {
      el.subtitle().textContent = `${fmtNum(stats.n)} / ${fmtNum(stats.total)} ${unit}`;
    } else {
      el.subtitle().textContent = "Running";
    }
  }
}

export function completeProgressBar(taskId: string): void {
  if (taskId !== activeTaskId) return;

  el.percent().textContent = "100";
  el.percentWrap().dataset.progressTier = "done";
  el.fill().style.width = "100%";
  el.dot().style.left = "100%";
  el.statEta().textContent = "done";
  el.subtitle().textContent = "Completed";

  showCompletionNote();

  if (completedResetTimer) clearTimeout(completedResetTimer);
  completedResetTimer = setTimeout(() => {
    activeTaskId = null;
    obs = null;
    resetToIdle();
  }, COMPLETION_HOLD_MS);
}

function resetToIdle(): void {
  hideCompletionNote();
  el.title().textContent = "Awaiting signal";
  el.subtitle().textContent = "Ready for the next observation";
  el.percent().textContent = "0";
  el.percentWrap().dataset.progressTier = "low";
  el.fill().style.width = "0%";
  el.dot().style.left = "0%";
  el.startClock().textContent = "—";
  el.etaClock().textContent = "—";
  el.statEta().textContent = "—";
  el.statRate().textContent = "—";
  el.statItems().textContent = "—";
  el.statTotal().textContent = "—";
  el.statElapsed().textContent = "—";

  const lbl = el.statusLabel();
  if (lbl.textContent === "Live") lbl.textContent = "Standby";
}

// --- Completion note (contextual support ask) ----------------------------

function showCompletionNote(): void {
  const note = el.completionNote();
  if (completionNoteFadeTimer) {
    clearTimeout(completionNoteFadeTimer);
    completionNoteFadeTimer = null;
  }
  note.classList.remove("is-hiding");
  note.removeAttribute("hidden");
  // Start fading out slightly before the card's reset so the visual
  // transitions don't collide.
  completionNoteFadeTimer = setTimeout(() => {
    note.classList.add("is-hiding");
  }, COMPLETION_HOLD_MS - 500);
}

function hideCompletionNote(): void {
  if (completionNoteFadeTimer) {
    clearTimeout(completionNoteFadeTimer);
    completionNoteFadeTimer = null;
  }
  const note = el.completionNote();
  note.classList.remove("is-hiding");
  note.setAttribute("hidden", "");
}

// --- Viewer chrome init ---------------------------------------------------

function initViewerOnce(): void {
  if (viewerInited) return;
  viewerInited = true;

  el.copyBtn().addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(window.location.href);
      const btn = el.copyBtn();
      const orig = btn.textContent ?? "Copy viewer link";
      btn.textContent = "Copied";
      btn.classList.add("copied");
      setTimeout(() => {
        btn.textContent = orig;
        btn.classList.remove("copied");
      }, 1500);
    } catch (err) {
      console.warn("Copy failed:", err);
    }
  });
}

/** Sender→viewers authoritative presence list. Replaces the viewers strip. */
export function setPresence(names: string[]): void {
  const list = el.viewersList();
  list.innerHTML = "";

  const local = getLocalViewerName();
  for (const name of names) {
    const isMe = name === local;
    const viewerEl = document.createElement("div");
    viewerEl.className = isMe ? "viewer viewer--me" : "viewer";
    viewerEl.title = isMe ? `${name} (you)` : name;
    viewerEl.innerHTML = `
      <div class="viewer-avatar">${makeAvatar(name)}</div>
      <div class="viewer-name">${escapeHtml(name)}</div>
    `;
    list.appendChild(viewerEl);
  }
  el.viewersCount().textContent = String(names.length);
}

export function getLocalViewerName(): string {
  const KEY = "fernsicht.viewer.name";
  let name = sessionStorage.getItem(KEY);
  if (!name) {
    const pool = [
      "vega", "orion", "lyra", "sirius", "nova", "iris", "atlas",
      "rigel", "altair", "cassia", "aurora", "deneb", "polaris",
      "mira", "pavo", "cosmo",
    ];
    name = pool[Math.floor(Math.random() * pool.length)];
    sessionStorage.setItem(KEY, name);
  }
  return name;
}

// --- Procedural wave-pixel avatar -----------------------------------------

function hashStr(s: string): number {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h;
}

function mulberry32(seed: number): () => number {
  let s = seed;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function makeAvatar(name: string): string {
  const size = 40;
  const pixel = 2;
  const pad = 2;
  const gridN = Math.floor((size - pad * 2) / pixel);
  const offset = (size - gridN * pixel) / 2;

  const rng = mulberry32(hashStr(name));
  const phase = rng() * Math.PI * 2;
  const amplitude = 0.12 + rng() * 0.18;
  const frequency = 1.4 + rng() * 1.8;
  const thickness = 1.0 + rng() * 1.2;
  const palette = ["#3fb950", "#52c37a", "#7dce8a", "#4fb89a", "#88c670"];
  const color = palette[hashStr(name) % palette.length];

  const cx = size / 2, cy = size / 2;
  const circleR = size / 2 - 0.5;

  let pixels = "";
  for (let gy = 0; gy < gridN; gy++) {
    for (let gx = 0; gx < gridN; gx++) {
      const px = offset + gx * pixel + pixel / 2;
      const py = offset + gy * pixel + pixel / 2;
      const dx = px - cx, dy = py - cy;
      const distCenter = Math.sqrt(dx * dx + dy * dy);
      if (distCenter > circleR - 0.5) continue;

      const t = (px - cx) / (size / 2);
      const waveY = cy + Math.sin(t * Math.PI * frequency + phase) * amplitude * size;
      const distWave = Math.abs(py - waveY);

      if (distWave < pixel * thickness) {
        const falloff = 1 - distWave / (pixel * thickness);
        const edgeFade = Math.min(1, (circleR - distCenter) / 3.5);
        const opacity = (0.35 + falloff * 0.6) * edgeFade;
        if (opacity > 0.05) {
          pixels += `<rect x="${offset + gx * pixel}" y="${offset + gy * pixel}" width="${pixel}" height="${pixel}" fill="${color}" opacity="${opacity.toFixed(2)}"/>`;
        }
      }
    }
  }

  return `<svg width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">
    <circle cx="${cx}" cy="${cy}" r="${circleR}" fill="#141821" stroke="#2a2f3a" stroke-width="1"/>
    ${pixels}
  </svg>`;
}

// --- Utilities ------------------------------------------------------------

function tierFor(pct: number): string {
  if (pct >= 100) return "done";
  if (pct >= 70) return "high";
  if (pct >= 35) return "mid";
  return "low";
}

function fmtNum(n: number): string {
  return n.toLocaleString("en-US").replace(/,/g, " ");
}

function formatClock(ts: number): string {
  const d = new Date(ts);
  const pad = (x: number) => String(x).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `0:${String(s).padStart(2, "0")}`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  if (m < 60) return `${m}:${String(rem).padStart(2, "0")}`;
  const h = Math.floor(m / 60);
  const remM = m % 60;
  return `${h}:${String(remM).padStart(2, "0")}:${String(rem).padStart(2, "0")}`;
}

function escapeHtml(text: string): string {
  const div = document.createElement("div");
  div.textContent = text;
  return div.innerHTML;
}
