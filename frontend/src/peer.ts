/** WebRTC peer connection for Fernsicht V2 — viewer-offer-first, HTTP signaling. */

import { ViewerSignaling } from "./signaling";
import { serializeKeepAlive } from "./protocol";

export type ConnectionPhase =
  | "contacting-server"   // POST /watch in flight
  | "queued"              // server accepted ticket; waiting for sender to poll
  | "negotiating"         // answer received; ICE exchange + DTLS handshake
  | "connected"           // DataChannel open; ready for first frame
  | "failed";             // signaling or peer connection gave up

export interface PeerEvents {
  onOpen: () => void;
  onMessage: (data: string) => void;
  onClose: () => void;
  onStateChange: (state: string) => void;
  onPhase: (phase: ConnectionPhase) => void;
  onSignalingError: (code: string, message: string, fatal: boolean) => void;
}

const STUN_SERVERS: RTCIceServer[] = [
  { urls: "stun:stun.l.google.com:19302" },
  { urls: "stun:stun1.l.google.com:19302" },
];

const KEEPALIVE_INTERVAL_MS = 20_000;
const DATACHANNEL_LABEL = "fernsicht";
const ICE_POLL_INTERVAL_MS = 500;
// Allow a longer ICE exchange window for slower mobile handshakes.
const ICE_POLL_MAX_ROUNDS = 60; // 30 seconds

// How long to allow the peer-connection to sit in WebRTC's transient
// "disconnected" state before treating it as a real failure. Mobile
// networks routinely flip ICE transports through brief disconnect
// windows that recover on their own; tearing down at the first hint
// of trouble (the previous behavior) caused phantom "Disconnected"
// banners while the sender was still streaming. The spec promises
// either recovery to "connected" or escalation to "failed" — we wait
// for one or the other.
const DISCONNECT_RECOVERY_GRACE_MS = 30_000;

// --- Viewer Peer ---

export class ViewerPeer {
  private pc: RTCPeerConnection;
  private dc: RTCDataChannel | null = null;
  private signaling: ViewerSignaling;
  private keepAliveTimer: ReturnType<typeof setInterval> | null = null;
  private icePollTimer: ReturnType<typeof setTimeout> | null = null;
  private iceFlushRetryTimer: ReturnType<typeof setTimeout> | null = null;
  private disconnectGraceTimer: ReturnType<typeof setTimeout> | null = null;
  private pendingICE: RTCIceCandidateInit[] = [];
  private flushingViewerICE = false;
  private iceSendSeq = 0;
  private iceRecvSeq = 0;
  private closed = false;

  constructor(
    baseURL: string,
    private readonly roomId: string,
    private readonly events: PeerEvents,
  ) {
    this.pc = new RTCPeerConnection({ iceServers: STUN_SERVERS });
    this.signaling = new ViewerSignaling(baseURL, {
      onAnswer: (answer) => this.handleAnswer(answer),
      onError: (msg, fatal) => {
        events.onSignalingError("SIGNALING", msg, fatal);
        if (fatal) {
          events.onStateChange("signaling-error");
          events.onPhase("failed");
        }
      },
      onQueued: () => {
        events.onStateChange("queued");
        events.onPhase("queued");
      },
    });

    this.setupPeerConnection();
  }

  async start(): Promise<void> {
    this.events.onStateChange("connecting");
    this.events.onPhase("contacting-server");

    // Viewer creates the DataChannel and the offer
    this.dc = this.pc.createDataChannel(DATACHANNEL_LABEL, { ordered: true });
    this.setupDataChannel(this.dc);

    const offer = await this.pc.createOffer();
    await this.pc.setLocalDescription(offer);

    const success = await this.signaling.watch(this.roomId, this.pc.localDescription!);
    if (!success) {
      this.events.onStateChange("signaling-error");
      this.events.onPhase("failed");
      return;
    }

    // ICE gathering can start before /watch returns. Drain any candidates
    // queued while the ticket/secret were still unavailable.
    this.flushViewerICE();
  }

  send(data: string): void {
    if (this.dc?.readyState === "open") {
      this.dc.send(data);
    }
  }

  // Exposed for stats polling (getStats, selected ICE pair inspection) in ui
  // code. Read-only surface; don't mutate the connection from outside.
  get connection(): RTCPeerConnection {
    return this.pc;
  }

  close(): void {
    this.closed = true;
    this.clearDisconnectGrace();
    this.stopKeepAlive();
    this.stopICEFlushRetry();
    this.stopICEPoll();
    this.signaling.stop();
    this.dc?.close();
    this.pc.close();
  }

  private setupPeerConnection(): void {
    this.pc.onicecandidate = (ev) => {
      if (!ev.candidate) return;
      this.pendingICE.push(ev.candidate.toJSON());
      this.flushViewerICE();
    };

    this.pc.onconnectionstatechange = () => {
      const state = this.pc.connectionState;
      this.events.onStateChange(state);

      if (state === "connected") {
        // Recovered (or first connect) — cancel any pending grace timer.
        this.clearDisconnectGrace();
        return;
      }

      if (state === "failed" || state === "closed") {
        // Terminal — tear down immediately.
        this.clearDisconnectGrace();
        this.stopKeepAlive();
        this.stopICEPoll();
        this.events.onClose();
        return;
      }

      if (state === "disconnected") {
        // Transient. Per the WebRTC spec the connection may recover to
        // "connected" without intervention — don't tear down yet. Start
        // a grace timer; if we're still disconnected when it fires,
        // treat it as a real failure.
        if (this.disconnectGraceTimer === null) {
          this.disconnectGraceTimer = setTimeout(() => {
            this.disconnectGraceTimer = null;
            if (this.closed) return;
            if (this.pc.connectionState !== "connected") {
              this.stopKeepAlive();
              this.stopICEPoll();
              this.events.onClose();
            }
          }, DISCONNECT_RECOVERY_GRACE_MS);
        }
      }
    };
  }

  private clearDisconnectGrace(): void {
    if (this.disconnectGraceTimer !== null) {
      clearTimeout(this.disconnectGraceTimer);
      this.disconnectGraceTimer = null;
    }
  }

  private setupDataChannel(dc: RTCDataChannel): void {
    dc.onopen = () => {
      this.events.onOpen();
      this.events.onStateChange("connected");
      this.events.onPhase("connected");
      this.startKeepAlive();
      this.stopICEPoll(); // handshake done
    };
    dc.onmessage = (ev) => {
      this.events.onMessage(typeof ev.data === "string" ? ev.data : "");
    };
    dc.onclose = () => {
      this.dc = null;
      this.stopKeepAlive();
      this.events.onClose();
    };
  }

  private async handleAnswer(answer: RTCSessionDescriptionInit): Promise<void> {
    this.events.onPhase("negotiating");
    try {
      await this.pc.setRemoteDescription(new RTCSessionDescription(answer));
      // Start polling for sender's ICE candidates
      this.startICEPoll();
    } catch (err) {
      console.error("[viewer-peer] failed to set remote description:", err);
    }
  }

  private async flushViewerICE(): Promise<void> {
    if (this.flushingViewerICE || this.closed) return;
    this.flushingViewerICE = true;
    try {
      while (!this.closed) {
        if (this.pendingICE.length === 0) return;
        const batch = this.pendingICE.slice();
        const posted = await this.signaling.postViewerICE(batch);
        if (!posted) {
          this.scheduleICEFlushRetry();
          return;
        }

        this.pendingICE.splice(0, batch.length);
        this.iceSendSeq += batch.length;
      }
    } finally {
      this.flushingViewerICE = false;
    }
  }

  private scheduleICEFlushRetry(): void {
    if (this.closed || this.iceFlushRetryTimer !== null) return;
    this.iceFlushRetryTimer = setTimeout(() => {
      this.iceFlushRetryTimer = null;
      this.flushViewerICE();
    }, 250);
  }

  private stopICEFlushRetry(): void {
    if (this.iceFlushRetryTimer !== null) {
      clearTimeout(this.iceFlushRetryTimer);
      this.iceFlushRetryTimer = null;
    }
  }

  private startICEPoll(): void {
    if (this.icePollTimer !== null) return;
    let rounds = 0;
    const poll = async () => {
      if (this.closed || rounds >= ICE_POLL_MAX_ROUNDS) return;
      rounds++;
      const resp = await this.signaling.getSenderICE(this.iceRecvSeq);
      if (resp && resp.candidates.length > 0) {
        for (const c of resp.candidates) {
          try {
            await this.pc.addIceCandidate(new RTCIceCandidate(c));
          } catch (err) {
            console.warn("[viewer-peer] failed to add ICE candidate:", err);
          }
        }
        this.iceRecvSeq = resp.seq;
      }
      if (!this.closed) {
        this.icePollTimer = setTimeout(poll, ICE_POLL_INTERVAL_MS);
      }
    };
    poll();
  }

  private stopICEPoll(): void {
    if (this.icePollTimer !== null) {
      clearTimeout(this.icePollTimer);
      this.icePollTimer = null;
    }
  }

  private startKeepAlive(): void {
    if (this.keepAliveTimer !== null) return;
    this.keepAliveTimer = setInterval(() => this.send(serializeKeepAlive()), KEEPALIVE_INTERVAL_MS);
  }

  private stopKeepAlive(): void {
    if (this.keepAliveTimer !== null) {
      clearInterval(this.keepAliveTimer);
      this.keepAliveTimer = null;
    }
  }
}
