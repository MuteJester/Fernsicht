/** WebRTC peer connection for Fernsicht V2 — viewer-offer-first, HTTP signaling. */

import {
  SenderSignaling,
  ViewerSignaling,
} from "./signaling";
import { serializeKeepAlive } from "./protocol";

export interface PeerEvents {
  onOpen: () => void;
  onMessage: (data: string) => void;
  onClose: () => void;
  onStateChange: (state: string) => void;
  onSignalingError: (code: string, message: string, fatal: boolean) => void;
}

const STUN_SERVERS: RTCIceServer[] = [
  { urls: "stun:stun.l.google.com:19302" },
  { urls: "stun:stun1.l.google.com:19302" },
];

const KEEPALIVE_INTERVAL_MS = 20_000;
const DATACHANNEL_LABEL = "fernsicht";
const ICE_POLL_INTERVAL_MS = 500;
const ICE_POLL_MAX_ROUNDS = 30; // 15 seconds

// --- Viewer Peer ---

export class ViewerPeer {
  private pc: RTCPeerConnection;
  private dc: RTCDataChannel | null = null;
  private signaling: ViewerSignaling;
  private keepAliveTimer: ReturnType<typeof setInterval> | null = null;
  private icePollTimer: ReturnType<typeof setTimeout> | null = null;
  private pendingICE: RTCIceCandidateInit[] = [];
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
        if (fatal) events.onStateChange("signaling-error");
      },
      onQueued: () => events.onStateChange("queued"),
    });

    this.setupPeerConnection();
  }

  async start(): Promise<void> {
    this.events.onStateChange("connecting");

    // Viewer creates the DataChannel and the offer
    this.dc = this.pc.createDataChannel(DATACHANNEL_LABEL, { ordered: true });
    this.setupDataChannel(this.dc);

    const offer = await this.pc.createOffer();
    await this.pc.setLocalDescription(offer);

    const success = await this.signaling.watch(this.roomId, this.pc.localDescription!);
    if (!success) {
      this.events.onStateChange("signaling-error");
    }
  }

  send(data: string): void {
    if (this.dc?.readyState === "open") {
      this.dc.send(data);
    }
  }

  close(): void {
    this.closed = true;
    this.stopKeepAlive();
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
      if (state === "disconnected" || state === "failed" || state === "closed") {
        this.stopKeepAlive();
        this.stopICEPoll();
        this.events.onClose();
      }
    };
  }

  private setupDataChannel(dc: RTCDataChannel): void {
    dc.onopen = () => {
      this.events.onOpen();
      this.events.onStateChange("connected");
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
    try {
      await this.pc.setRemoteDescription(new RTCSessionDescription(answer));
      // Start polling for sender's ICE candidates
      this.startICEPoll();
    } catch (err) {
      console.error("[viewer-peer] failed to set remote description:", err);
    }
  }

  private async flushViewerICE(): Promise<void> {
    if (this.pendingICE.length === 0) return;
    const batch = [...this.pendingICE];
    this.pendingICE = [];
    await this.signaling.postViewerICE(batch);
    this.iceSendSeq += batch.length;
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

// --- Sender Peer ---

interface SenderViewerState {
  ticketId: string;
  pc: RTCPeerConnection;
  dc: RTCDataChannel | null;
  iceRecvSeq: number;
  icePollTimer: ReturnType<typeof setTimeout> | null;
}

export class SenderPeer {
  private signaling: SenderSignaling;
  private viewers = new Map<string, SenderViewerState>();
  private keepAliveTimer: ReturnType<typeof setInterval> | null = null;
  private closed = false;

  constructor(
    baseURL: string,
    roomId: string,
    secret: string,
    pollIntervalMs: number,
    private readonly events: PeerEvents,
  ) {
    this.signaling = new SenderSignaling(baseURL, roomId, secret, pollIntervalMs, {
      onTicket: (ticketId, offer) => this.handleViewerTicket(ticketId, offer),
      onError: (msg, fatal) => {
        events.onSignalingError("SIGNALING", msg, fatal);
        if (fatal) events.onStateChange("signaling-error");
      },
      onPollOk: () => {
        if (this.viewers.size === 0) {
          events.onStateChange("signaling-joined");
        }
      },
    });
  }

  start(): void {
    this.events.onStateChange("connecting");
    this.signaling.start();
  }

  send(data: string): void {
    for (const state of this.viewers.values()) {
      if (state.dc?.readyState === "open") {
        state.dc.send(data);
      }
    }
  }

  close(): void {
    this.closed = true;
    this.stopKeepAlive();
    this.signaling.stop();
    for (const state of this.viewers.values()) {
      if (state.icePollTimer) clearTimeout(state.icePollTimer);
      state.dc?.close();
      state.pc.close();
    }
    this.viewers.clear();
  }

  private async handleViewerTicket(
    ticketId: string,
    offer: RTCSessionDescriptionInit,
  ): Promise<void> {
    const pc = new RTCPeerConnection({ iceServers: STUN_SERVERS });
    const state: SenderViewerState = {
      ticketId,
      pc,
      dc: null,
      iceRecvSeq: 0,
      icePollTimer: null,
    };

    const pendingSenderICE: RTCIceCandidateInit[] = [];

    pc.onicecandidate = (ev) => {
      if (!ev.candidate) return;
      pendingSenderICE.push(ev.candidate.toJSON());
      // Flush in batches
      if (pendingSenderICE.length >= 3) {
        const batch = [...pendingSenderICE];
        pendingSenderICE.length = 0;
        this.signaling.postSenderICE(ticketId, batch);
      }
    };

    pc.onicegatheringstatechange = () => {
      if (pc.iceGatheringState === "complete" && pendingSenderICE.length > 0) {
        const batch = [...pendingSenderICE];
        pendingSenderICE.length = 0;
        this.signaling.postSenderICE(ticketId, batch);
      }
    };

    pc.ondatachannel = (ev) => {
      state.dc = ev.channel;
      ev.channel.onopen = () => {
        this.events.onOpen();
        this.updateAggregateState();
        this.startKeepAlive();
      };
      ev.channel.onmessage = (msgEv) => {
        this.events.onMessage(typeof msgEv.data === "string" ? msgEv.data : "");
      };
      ev.channel.onclose = () => {
        this.viewers.delete(ticketId);
        this.updateAggregateState();
        if (!this.anyOpenChannel()) {
          this.stopKeepAlive();
          this.events.onClose();
        }
      };
    };

    pc.onconnectionstatechange = () => {
      const connState = pc.connectionState;
      if (connState === "failed" || connState === "closed") {
        if (state.icePollTimer) clearTimeout(state.icePollTimer);
        this.viewers.delete(ticketId);
        this.updateAggregateState();
      }
    };

    this.viewers.set(ticketId, state);
    this.updateAggregateState();

    try {
      await pc.setRemoteDescription(new RTCSessionDescription(offer));
      const answer = await pc.createAnswer();
      await pc.setLocalDescription(answer);

      // Post answer to server
      await this.signaling.postAnswer(ticketId, pc.localDescription!);

      // Flush any remaining ICE
      if (pendingSenderICE.length > 0) {
        const batch = [...pendingSenderICE];
        pendingSenderICE.length = 0;
        this.signaling.postSenderICE(ticketId, batch);
      }

      // Start polling for viewer's ICE candidates
      this.startViewerICEPoll(state);
    } catch (err) {
      console.error("[sender-peer] handshake failed for ticket:", ticketId, err);
      pc.close();
      this.viewers.delete(ticketId);
    }
  }

  private startViewerICEPoll(state: SenderViewerState): void {
    let rounds = 0;
    const poll = async () => {
      if (this.closed || rounds >= ICE_POLL_MAX_ROUNDS) return;
      rounds++;
      const resp = await this.signaling.getViewerICE(state.ticketId, state.iceRecvSeq);
      if (resp && resp.candidates.length > 0) {
        for (const c of resp.candidates) {
          try {
            await state.pc.addIceCandidate(new RTCIceCandidate(c));
          } catch (err) {
            console.warn("[sender-peer] failed to add ICE candidate:", err);
          }
        }
        state.iceRecvSeq = resp.seq;
      }
      if (!this.closed) {
        state.icePollTimer = setTimeout(poll, ICE_POLL_INTERVAL_MS);
      }
    };
    poll();
  }

  private anyOpenChannel(): boolean {
    for (const state of this.viewers.values()) {
      if (state.dc?.readyState === "open") return true;
    }
    return false;
  }

  private updateAggregateState(): void {
    if (this.anyOpenChannel()) {
      this.events.onStateChange("connected");
    } else if (this.viewers.size > 0) {
      this.events.onStateChange("connecting");
    } else {
      this.events.onStateChange("signaling-joined");
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
