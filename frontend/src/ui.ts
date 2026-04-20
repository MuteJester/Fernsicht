/** DOM rendering for Fernsicht viewer — new viewer bench design. */

const $ = (id: string) => document.getElementById(id)!;

// --- DOM accessors --------------------------------------------------------

const el = {
  // Top rail
  statusChip:    () => $("statusChip"),
  railRoom:      () => $("railRoom"),
  // Left rail: session
  sessRoom:      () => $("sessRoom"),
  sessStarted:   () => $("sessStarted"),
  sessTransport: () => $("sessTransport"),
  latency:       () => $("latency"),
  // Left rail: share
  shareUrl:      () => $("shareUrl"),
  shareCopy:     () => $("shareCopy") as HTMLButtonElement,
  taskBannerClose: () => $("taskBannerClose") as HTMLButtonElement,
  taskBannerIcon:  () => $("taskBannerIcon"),
  // Left rail: handshake
  handshake:     () => $("handshake"),
  // Center: instrument strip
  stripRoom:     () => $("stripRoom"),
  transport:     () => $("transport"),
  viewerCount:   () => $("viewerCount"),
  // Center: task header
  taskTitle:     () => $("taskTitle"),
  taskSub:       () => $("taskSub"),
  taskBadge:     () => $("taskBadge"),
  // Center: progress readout
  pct:           () => $("pct"),
  fracLabel:     () => $("fracLabel"),
  fracN:         () => $("fracN"),
  fracT:         () => $("fracT"),
  fracN3:        () => $("fracN3"),
  fracT3:        () => $("fracT3"),
  fill:          () => $("fill"),
  dot:           () => $("dot"),
  // Center: stats row
  eta:           () => $("eta"),
  rate:          () => $("rate"),
  rateUnit:      () => $("rateUnit"),
  elapsed:       () => $("elapsed"),
  // Center: error/warning banner
  taskBanner:    () => $("taskBanner"),
  taskBannerText: () => $("taskBannerText"),
  // Completion card
  doneCard:       () => $("doneCard"),
  doneCardClose:  () => $("doneCardClose") as HTMLButtonElement,
  doneCardMaybe:  () => $("doneCardMaybe") as HTMLButtonElement,
  doneCardElapsed: () => $("doneCardElapsed"),
  doneCardTimerFill: () => $("doneCardTimerFill"),
  // Right rail: viewers
  viewers:       () => $("viewers"),
  viewerCount2:  () => $("viewerCount2"),
  // Right rail: metrics
  mLatency:      () => $("mLatency"),
  mLatencyV:     () => $("mLatencyV"),
  mPath:         () => $("mPath"),
  mPathV:        () => $("mPathV"),
  mSignal:       () => $("mSignal"),
  mSignalV:      () => $("mSignalV"),
  mKeep:         () => $("mKeep"),
  mKeepV:        () => $("mKeepV"),
  // Right rail: log
  log:           () => $("log"),
  // Footer
  barState:      () => $("barState"),
  barTransport:  () => $("barTransport"),
  latencyFoot:   () => $("latencyFoot"),
};

let activeTaskId: string | null = null;
let viewerInited = false;
let handshakeStartedAt = 0;
let lastKeepaliveAt = 0;

// Client-side rate/eta derivation. The viewer receives progress frames
// from the sender; some senders (e.g. CLI magic-prefix in N/TOTAL form)
// don't fill the rate/eta fields, so derive them from a rolling window
// of (n, timestamp) samples. If the sender DOES send rate/eta, we use
// its values verbatim and skip derivation.
interface RateSample { n: number; t: number; }
const RATE_WINDOW_MS = 20_000;
const RATE_MIN_SAMPLES = 2;
let rateSamples: RateSample[] = [];
let taskStartedAt = 0;
let elapsedTickTimer: ReturnType<typeof setInterval> | null = null;
let maxProgressSeen = 0; // highest progress value (0..1) observed for the current task

let completedResetTimer: ReturnType<typeof setTimeout> | null = null;
const COMPLETION_HOLD_MS = 15000;

// --- View switching -------------------------------------------------------
//
// app.html is viewer-only — there's no landing section to hide or show.
// These functions remain in the public API so main.ts's shared branches
// work unchanged. `showLanding` on this page means "no room fragment;
// bounce to landing".

export function showLanding(): void {
  window.location.replace("/");
}

export function showViewerView(): void {
  initViewerOnce();
  handshakeStartedAt = performance.now();
  renderStarted(new Date());
}

// --- Connection status ----------------------------------------------------

type Status = "connecting" | "connected" | "disconnected" | "signaling-error";

export function setConnectionStatus(status: Status): void {
  const chip = el.statusChip();
  chip.classList.remove("live", "warn", "error");
  switch (status) {
    case "connecting":
      chip.textContent = "Connecting";
      chip.classList.add("warn");
      el.barState().textContent = "CONNECTING";
      break;
    case "connected":
      chip.textContent = activeTaskId ? "Live" : "Standby";
      chip.classList.add("live");
      el.barState().textContent = "READY";
      break;
    case "disconnected":
      chip.textContent = "Offline";
      chip.classList.add("error");
      el.barState().textContent = "OFFLINE";
      // No banner here — main.ts decides (completion banner vs. a plain
      // disconnect notice) based on whether the task actually finished.
      break;
    case "signaling-error":
      chip.textContent = "Error";
      chip.classList.add("error");
      el.barState().textContent = "ERROR";
      break;
  }
}

export function setRoomId(roomId: string): void {
  const short = roomId.length > 12 ? `${roomId.slice(0, 8)}…` : roomId;
  el.railRoom().textContent = short;
  el.sessRoom().textContent = short;
  el.stripRoom().textContent = short;

  // Share URL: the *current* page URL works (it carries the room fragment).
  el.shareUrl().textContent = window.location.href;
}

// No-op — the new design shows "remote" as a literal Host value rather
// than a peer identifier. Kept in the public API for main.ts compat.
export function setPeerId(_id: string): void {
  /* no-op */
}

export function setConnectionDetail(
  message: string | null,
  tone: "info" | "warning" | "error" = "info",
): void {
  // Pre-handshake: the subtitle mirrors the active step. Once a task is
  // live, preserve the task's own subtitle.
  if (activeTaskId) return;
  el.taskSub().textContent = message ?? "";

  // Only surface truly hard errors through the flat banner — transient
  // connecting warnings are already visible in the subtitle and the
  // handshake stepper. A banner for every reconnect attempt was noisy.
  if (tone === "error") {
    showBanner(message ?? "Connection error", "error");
  } else {
    hideBanner();
  }
}

// --- Handshake stepper ----------------------------------------------------

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

const SENDER_POLL_HINT_SEC = 25;
let countdownTimer: ReturnType<typeof setInterval> | null = null;
let countdownRemaining = SENDER_POLL_HINT_SEC;
const phaseTimestamps = new Map<ConnectionPhase, number>();

export function setConnectionPhase(phase: ConnectionPhase): void {
  stopCountdown();

  const steps = el.handshake().children;

  if (phase === "failed") {
    // Mark whichever step is active as failed, leave the rest.
    for (const step of Array.from(steps)) {
      if (step.classList.contains("is-active")) {
        step.classList.remove("is-active");
        step.classList.add("is-failed");
        const t = step.querySelector<HTMLElement>(".timing");
        if (t) t.textContent = "failed";
      }
    }
    logEvent("handshake failed");
    return;
  }

  // Record phase entry for timing annotations.
  if (!phaseTimestamps.has(phase)) {
    phaseTimestamps.set(phase, performance.now());
  }

  const idx = STEP_ORDER.indexOf(phase);
  if (idx < 0) return;

  for (let i = 0; i < STEP_ORDER.length; i++) {
    const step = steps[i] as HTMLElement | undefined;
    if (!step) continue;
    step.classList.remove("is-active", "is-done", "is-failed");
    const t = step.querySelector<HTMLElement>(".timing");
    if (i < idx) {
      step.classList.add("is-done");
      if (t) t.textContent = formatPhaseTiming(STEP_ORDER[i]);
    } else if (i === idx) {
      step.classList.add("is-active");
      if (t) t.textContent = phase === "queued" ? "" : formatPhaseTiming(phase);
    } else {
      if (t) t.textContent = "";
    }
  }

  // If we've reached `connected`, show the 5th (streaming) step as
  // pending until the first START frame lands.
  const streamStep = steps[4] as HTMLElement | undefined;
  if (streamStep) {
    streamStep.classList.remove("is-active", "is-done");
    const t = streamStep.querySelector<HTMLElement>(".timing");
    if (phase === "connected") {
      streamStep.classList.add("is-active");
      if (t) t.textContent = "awaiting frame";
    } else {
      if (t) t.textContent = "";
    }
  }

  if (phase === "queued") startQueuedCountdown();
}

function formatPhaseTiming(phase: ConnectionPhase): string {
  const t = phaseTimestamps.get(phase);
  if (!t || !handshakeStartedAt) return "";
  const ms = Math.max(0, Math.round(t - handshakeStartedAt));
  if (ms < 1000) return `+ ${ms} ms`;
  return `+ ${(ms / 1000).toFixed(1)} s`;
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
  // Display the countdown in the queued step's timing slot.
  const queuedStep = el.handshake().children[1] as HTMLElement | undefined;
  if (!queuedStep) return;
  const t = queuedStep.querySelector<HTMLElement>(".timing");
  if (t) t.textContent = `next check-in ${countdownRemaining}s`;
}

function stopCountdown(): void {
  if (countdownTimer !== null) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
}

// --- Observation (single active task) -------------------------------------

export function createProgressBar(taskId: string, label: string): void {
  if (completedResetTimer) {
    clearTimeout(completedResetTimer);
    completedResetTimer = null;
  }
  hideBanner();

  activeTaskId = taskId;
  taskStartedAt = Date.now();
  rateSamples = [];
  maxProgressSeen = 0;
  startElapsedTick();

  el.taskTitle().textContent = label;
  el.taskSub().textContent = "Starting…";
  el.taskBadge().textContent = "Running";
  el.pct().textContent = "0";
  el.fill().style.width = "0%";
  el.dot().style.left = "0%";
  el.fracN().textContent = "—";
  el.fracT().textContent = "—";
  el.fracN3().textContent = "—";
  el.fracT3().textContent = "—";
  el.eta().textContent = "—";
  el.rate().textContent = "—";
  el.rateUnit().textContent = "/s";
  el.elapsed().textContent = "0:00";

  // Mark the 5th (streaming) handshake step as done.
  const streamStep = el.handshake().children[4] as HTMLElement | undefined;
  if (streamStep) {
    streamStep.classList.remove("is-active");
    streamStep.classList.add("is-done");
    const t = streamStep.querySelector<HTMLElement>(".timing");
    if (t) t.textContent = "live";
  }

  const chip = el.statusChip();
  if (chip.textContent === "Standby") chip.textContent = "Live";

  logEvent(`task <b>${escapeHtml(label)}</b> started`);
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
  if (!activeTaskId) createProgressBar(taskId, taskId);
  if (taskId !== activeTaskId) return;

  const pct = Math.max(0, Math.min(100, value * 100));
  el.pct().textContent = String(Math.floor(pct));
  el.fill().style.width = `${pct}%`;
  el.dot().style.left = `${pct}%`;
  if (value > maxProgressSeen) maxProgressSeen = value;

  if (stats) {
    const unit = stats.unit || "items";
    el.fracLabel().textContent = unit;
    el.rateUnit().textContent = `${unit}/s`;

    if (stats.n !== null) {
      const nFmt = fmtNum(stats.n);
      el.fracN().textContent = nFmt;
      el.fracN3().textContent = nFmt;
    }
    if (stats.total !== null) {
      const tFmt = fmtNum(stats.total);
      el.fracT().textContent = tFmt;
      el.fracT3().textContent = tFmt;
    }
    // Record a sample for client-side rate derivation whenever we have
    // an n (absolute counter); total isn't required to compute rate.
    const now = Date.now();
    if (stats.n !== null) {
      rateSamples.push({ n: stats.n, t: now });
      const cutoff = now - RATE_WINDOW_MS;
      while (rateSamples.length > 1 && rateSamples[0].t < cutoff) {
        rateSamples.shift();
      }
    }

    // Prefer the sender's rate if present; otherwise derive from the
    // sample window.
    let rate: number | null = stats.rate;
    if (rate === null && rateSamples.length >= RATE_MIN_SAMPLES) {
      const first = rateSamples[0];
      const last = rateSamples[rateSamples.length - 1];
      const dn = last.n - first.n;
      const dt = (last.t - first.t) / 1000;
      if (dt > 0 && dn >= 0) rate = dn / dt;
    }
    if (rate !== null) {
      el.rate().textContent = rate >= 10 ? rate.toFixed(0) : rate.toFixed(2);
    }

    // Elapsed: prefer sender's value; otherwise use the client-side
    // clock (which keeps ticking between progress frames thanks to
    // startElapsedTick).
    if (stats.elapsed !== null) {
      el.elapsed().textContent = formatDuration(stats.elapsed);
    } else if (taskStartedAt > 0) {
      el.elapsed().textContent = formatDuration((now - taskStartedAt) / 1000);
    }

    // ETA: prefer sender's; otherwise derive from remaining / rate.
    let eta: number | null = stats.eta;
    if (eta === null && rate !== null && rate > 0
        && stats.n !== null && stats.total !== null && stats.total > stats.n) {
      eta = (stats.total - stats.n) / rate;
    }
    if (eta !== null) {
      el.eta().textContent = formatDuration(eta);
    }

    if (stats.total !== null && stats.n !== null) {
      el.taskSub().textContent = `${fmtNum(stats.n)} / ${fmtNum(stats.total)} ${unit} · live`;
    } else {
      el.taskSub().textContent = "Running";
    }
  }
}

function startElapsedTick(): void {
  stopElapsedTick();
  elapsedTickTimer = setInterval(() => {
    if (!activeTaskId || taskStartedAt === 0) return;
    // Only advance the elapsed label between sender frames — don't
    // stomp on a freshly-written sender value; this fires on a 1s
    // cadence regardless, and sender frames at >=1Hz will win the race.
    el.elapsed().textContent = formatDuration((Date.now() - taskStartedAt) / 1000);
  }, 1000);
}

function stopElapsedTick(): void {
  if (elapsedTickTimer !== null) {
    clearInterval(elapsedTickTimer);
    elapsedTickTimer = null;
  }
}

export function completeProgressBar(taskId: string): void {
  if (taskId !== activeTaskId) return;

  el.pct().textContent = "100";
  el.fill().style.width = "100%";
  el.dot().style.left = "100%";
  el.eta().textContent = "done";
  el.taskSub().textContent = "Completed";
  el.taskBadge().textContent = "Done";

  logEvent(`task <b>completed</b>`);
  showDoneCard();

  stopElapsedTick();

  if (completedResetTimer) clearTimeout(completedResetTimer);
  completedResetTimer = setTimeout(() => {
    activeTaskId = null;
    taskStartedAt = 0;
    rateSamples = [];
    // Don't dismiss the done-card on idle reset — its own auto-dismiss
    // timer (20s) handles that and keeps the Ko-fi ask on screen briefly
    // past the task state flip.
    resetToIdle();
  }, COMPLETION_HOLD_MS);
}

/** Safety net: the sender disconnected before an explicit END frame
 *  arrived. If the task had meaningfully progressed, assume it finished
 *  and show the done-card anyway. */
export function maybeShowCompletionOnClose(): void {
  if (maxProgressSeen >= 0.95) showDoneCard();
}

function resetToIdle(): void {
  hideBanner();
  el.taskTitle().textContent = "Awaiting signal";
  el.taskSub().textContent = "Ready for the next observation";
  el.taskBadge().textContent = "Idle";
  el.pct().textContent = "0";
  el.fill().style.width = "0%";
  el.dot().style.left = "0%";
  el.fracN().textContent = "—";
  el.fracT().textContent = "—";
  el.fracN3().textContent = "—";
  el.fracT3().textContent = "—";
  el.eta().textContent = "—";
  el.rate().textContent = "—";
  el.elapsed().textContent = "—";

  const chip = el.statusChip();
  if (chip.textContent === "Live") chip.textContent = "Standby";
}

// --- Done card (task complete → Ko-fi ask) --------------------------------
//
// Soft-entrance card shown on task completion. Auto-dismisses after 20s
// (CSS-driven timer bar). Hovering/focusing pauses the timer so the
// viewer has time to read. Idempotent per task — showDoneCard is safe
// to call multiple times; only the first invocation since the last
// hideDoneCard triggers the entrance animation.

const DONE_CARD_AUTO_DISMISS_MS = 20_000;
const DONE_CARD_POST_HOVER_MS   =  6_000;
let doneCardShown = false;
let doneCardHideTimer: ReturnType<typeof setTimeout> | null = null;

function showDoneCard(): void {
  if (doneCardShown) return;
  doneCardShown = true;

  const card = el.doneCard();
  const timerFill = el.doneCardTimerFill();
  const elapsedTxt = el.elapsed().textContent?.trim();
  el.doneCardElapsed().textContent =
    elapsedTxt && elapsedTxt !== "—" ? elapsedTxt : "finished";

  card.removeAttribute("hidden");

  // Restart the CSS timer animation by toggling the class.
  timerFill.classList.remove("is-running");
  // Force a reflow so the keyframes reset.
  void timerFill.offsetWidth;
  timerFill.classList.add("is-running");

  if (doneCardHideTimer) clearTimeout(doneCardHideTimer);
  doneCardHideTimer = setTimeout(hideDoneCard, DONE_CARD_AUTO_DISMISS_MS);
}

function hideDoneCard(): void {
  const card = el.doneCard();
  if (card.hasAttribute("hidden")) { doneCardShown = false; return; }

  card.classList.add("is-leaving");
  if (doneCardHideTimer) { clearTimeout(doneCardHideTimer); doneCardHideTimer = null; }
  setTimeout(() => {
    card.setAttribute("hidden", "");
    card.classList.remove("is-leaving");
    el.doneCardTimerFill().classList.remove("is-running");
    doneCardShown = false;
  }, 420);
}

function initDoneCardHandlers(): void {
  const card = el.doneCard();
  const timerFill = el.doneCardTimerFill();

  el.doneCardClose().addEventListener("click", hideDoneCard);
  el.doneCardMaybe().addEventListener("click", hideDoneCard);

  // Pause the auto-dismiss timer while the viewer is reading.
  const pause = () => {
    timerFill.classList.add("is-paused");
    if (doneCardHideTimer) { clearTimeout(doneCardHideTimer); doneCardHideTimer = null; }
  };
  const resume = () => {
    timerFill.classList.remove("is-paused");
    if (doneCardHideTimer) clearTimeout(doneCardHideTimer);
    doneCardHideTimer = setTimeout(hideDoneCard, DONE_CARD_POST_HOVER_MS);
  };
  card.addEventListener("mouseenter", pause);
  card.addEventListener("focusin", pause);
  card.addEventListener("mouseleave", resume);
  card.addEventListener("focusout", resume);
}

// --- Flat banner (error / warning only; completion uses doneCard) --------

function showBanner(
  text: string,
  tone: "warn" | "error",
  icon?: string,
): void {
  const banner = el.taskBanner();
  const textEl = el.taskBannerText();
  const iconEl = el.taskBannerIcon();

  banner.classList.remove("task-banner--warn", "task-banner--error");
  banner.classList.add(`task-banner--${tone}`);
  banner.removeAttribute("hidden");

  textEl.textContent = text;
  iconEl.textContent = icon ?? (tone === "warn" ? "!" : "×");
}

function hideBanner(): void {
  el.taskBanner().setAttribute("hidden", "");
}

// --- Viewer chrome init ---------------------------------------------------

function initViewerOnce(): void {
  if (viewerInited) return;
  viewerInited = true;

  initRailToggles();
  initDoneCardHandlers();

  el.shareCopy().addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(window.location.href);
      const btn = el.shareCopy();
      const orig = btn.textContent ?? "Copy";
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

  el.taskBannerClose().addEventListener("click", () => hideBanner());

  // Keyboard shortcuts: F (fullscreen), C (copy link).
  document.addEventListener("keydown", (ev) => {
    const target = ev.target as HTMLElement | null;
    if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA")) return;
    const key = ev.key.toLowerCase();
    if (key === "c") el.shareCopy().click();
    if (key === "f") {
      if (!document.fullscreenElement) {
        document.documentElement.requestFullscreen().catch(() => {});
      } else {
        document.exitFullscreen().catch(() => {});
      }
    }
  });
}

// Tap a rail-block's header (on mobile, per CSS) to toggle its body.
// On desktop the clicks are a no-op because the CSS doesn't hide the
// body — the class switch is harmless.
function initRailToggles(): void {
  const blocks = document.querySelectorAll<HTMLElement>(".rail-block[data-collapsible]");
  for (const block of Array.from(blocks)) {
    const head = block.querySelector<HTMLElement>(".rail-block-head");
    if (!head) continue;
    head.addEventListener("click", () => {
      block.classList.toggle("is-open");
    });
  }
}

function renderStarted(when: Date): void {
  const pad = (n: number) => String(n).padStart(2, "0");
  el.sessStarted().textContent =
    `${pad(when.getUTCHours())}:${pad(when.getUTCMinutes())}:${pad(when.getUTCSeconds())} UTC`;
}

// --- Viewer presence ------------------------------------------------------

export function setPresence(names: string[]): void {
  const list = el.viewers();
  list.innerHTML = "";

  const local = getLocalViewerName();
  for (const name of names) {
    const isMe = name === local;
    const row = document.createElement("div");
    row.className = isMe ? "viewer-row me" : "viewer-row";
    row.innerHTML = `
      <div class="viewer-avatar">${makeAvatar(name)}</div>
      <div class="viewer-name">${escapeHtml(name)}${isMe ? " <span class=\"dim\">(you)</span>" : ""}</div>
      <div class="viewer-since">${isMe ? "just now" : ""}</div>
    `;
    list.appendChild(row);
  }
  el.viewerCount().textContent = String(names.length);
  el.viewerCount2().textContent = `${names.length} live`;
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

// --- Event log ------------------------------------------------------------

const logStartedAt = performance.now();
const LOG_MAX = 40;

export function logEvent(html: string): void {
  const feed = el.log();
  if (!feed) return;

  const elapsed = (performance.now() - logStartedAt) / 1000;
  const mm = Math.floor(elapsed / 60);
  const ss = Math.floor(elapsed % 60);
  const ts = `+${String(mm).padStart(2, "0")}:${String(ss).padStart(2, "0")}`;

  const row = document.createElement("div");
  row.className = "ln";
  row.innerHTML = `<span class="ts">${ts}</span><span class="msg">${html}</span>`;
  feed.appendChild(row);
  while (feed.children.length > LOG_MAX) feed.removeChild(feed.firstChild!);
  feed.scrollTop = feed.scrollHeight;
}

// --- Stats polling --------------------------------------------------------
//
// Called once after the RTCPeerConnection is live. Uses getStats() to
// read RTT + selected ICE pair; derives transport path (direct vs relay)
// and signal quality. Keepalive liveness is derived from our own send
// loop via `markKeepalive()`.

const STATS_POLL_INTERVAL_MS = 2000;

interface IceCandidateStats {
  id: string;
  candidateType?: string;      // host | srflx | prflx | relay
  type: string;
}

interface CandidatePairStats {
  selected?: boolean;
  nominated?: boolean;
  state?: string;
  localCandidateId?: string;
  remoteCandidateId?: string;
  currentRoundTripTime?: number; // seconds
  type: string;
}

let statsTimer: ReturnType<typeof setInterval> | null = null;

export function initStatsPolling(pc: RTCPeerConnection): void {
  stopStatsPolling();

  const tick = async () => {
    try {
      const report = await pc.getStats();
      const pairs: CandidatePairStats[] = [];
      const cands = new Map<string, IceCandidateStats>();

      report.forEach((s: any) => {
        if (s.type === "candidate-pair") pairs.push(s as CandidatePairStats);
        if (s.type === "local-candidate" || s.type === "remote-candidate") {
          cands.set(s.id, s as IceCandidateStats);
        }
      });

      // Prefer the selected/nominated succeeded pair.
      const selected = pairs.find((p) => p.selected)
        ?? pairs.find((p) => p.state === "succeeded" && p.nominated)
        ?? pairs.find((p) => p.state === "succeeded");

      if (selected) {
        const rttSec = selected.currentRoundTripTime ?? 0;
        const rttMs = Math.max(1, Math.round(rttSec * 1000));
        el.latency().textContent = String(rttMs);
        el.latencyFoot().textContent = String(rttMs);

        // Latency bar: 0ms=100%, 400ms=0% (clamped).
        const latPct = Math.max(4, Math.min(100, 100 - (rttMs / 400) * 100));
        el.mLatency().style.width = `${latPct}%`;
        el.mLatencyV().textContent = `${rttMs} ms`;

        // Transport: inspect local candidate type.
        const local = selected.localCandidateId ? cands.get(selected.localCandidateId) : undefined;
        const remote = selected.remoteCandidateId ? cands.get(selected.remoteCandidateId) : undefined;
        const localType = (local as any)?.candidateType as string | undefined;
        const remoteType = (remote as any)?.candidateType as string | undefined;
        const isRelay = localType === "relay" || remoteType === "relay";
        const pathLabel = isRelay ? "relay" : "direct";

        el.transport().textContent = `webrtc · ${pathLabel}`;
        el.sessTransport().textContent = `webrtc / ${pathLabel}`;
        el.barTransport().textContent = `stream · ${pathLabel}`;

        el.mPath().style.width = isRelay ? "55%" : "100%";
        el.mPathV().textContent = pathLabel;

        // Signal quality from RTT: <120ms = stable, 120–300ms = fair, >300ms = jittery.
        let signalPct = 100, signalLabel = "stable";
        if (rttMs > 300)      { signalPct = 30; signalLabel = "jittery"; }
        else if (rttMs > 120) { signalPct = 70; signalLabel = "fair"; }
        el.mSignal().style.width = `${signalPct}%`;
        el.mSignalV().textContent = signalLabel;
      }

      // Keepalive: if we've seen one in the last 30s, we're healthy.
      const keepalive_age = lastKeepaliveAt === 0 ? null : (Date.now() - lastKeepaliveAt) / 1000;
      if (keepalive_age === null) {
        el.mKeep().style.width = "0%";
        el.mKeepV().textContent = "waiting";
      } else if (keepalive_age < 30) {
        el.mKeep().style.width = "100%";
        el.mKeepV().textContent = "ok";
      } else {
        el.mKeep().style.width = "30%";
        el.mKeepV().textContent = "late";
      }
    } catch (err) {
      console.warn("[stats] getStats failed:", err);
    }
  };

  tick();
  statsTimer = setInterval(tick, STATS_POLL_INTERVAL_MS);
}

export function stopStatsPolling(): void {
  if (statsTimer !== null) {
    clearInterval(statsTimer);
    statsTimer = null;
  }
}

export function markKeepalive(): void {
  lastKeepaliveAt = Date.now();
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
  const size = 32;
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

function fmtNum(n: number): string {
  return n.toLocaleString("en-US").replace(/,/g, " ");
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
