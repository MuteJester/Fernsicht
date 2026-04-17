/** WebRTC peer connection and DataChannel management for Fernsicht. */

import { SignalingClient, type Role } from "./signaling";
import { serializeKeepAlive } from "./protocol";

/** SDP/ICE messages relayed through the signaling server. */
interface SignalEnvelope {
  to?: string;
  from?: string;
  type: "offer" | "answer" | "ice";
  payload: RTCSessionDescriptionInit | RTCIceCandidateInit;
}

interface SenderPeerState {
  viewerId: string;
  pc: RTCPeerConnection;
  dc: RTCDataChannel;
  pendingIce: RTCIceCandidateInit[];
}

export interface PeerEvents {
  onOpen: () => void;
  onMessage: (data: string) => void;
  onClose: () => void;
  onStateChange: (state: string) => void;
  onSignalingError: (code: string, message: string, fatal: boolean) => void;
}

export interface PeerOptions {
  senderJoinToken?: string;
}

const STUN_SERVERS: RTCIceServer[] = [
  { urls: "stun:stun.l.google.com:19302" },
  { urls: "stun:stun1.l.google.com:19302" },
];

const KEEPALIVE_INTERVAL_MS = 20_000;
const DATACHANNEL_LABEL = "fernsicht";
const LEGACY_VIEWER_ID = "__legacy__";

export class FernsichtPeer {
  private viewerPc: RTCPeerConnection | null = null;
  private viewerDc: RTCDataChannel | null = null;
  private viewerPendingIce: RTCIceCandidateInit[] = [];

  private senderPeers = new Map<string, SenderPeerState>();

  private signaling: SignalingClient;
  private readonly events: PeerEvents;
  private readonly role: Role;
  private keepAliveTimer: ReturnType<typeof setInterval> | null = null;

  constructor(
    signalingUrl: string,
    roomId: string,
    role: Role,
    events: PeerEvents,
    options: PeerOptions = {},
  ) {
    this.role = role;
    this.events = events;

    this.signaling = new SignalingClient(
      signalingUrl,
      roomId,
      role,
      {
        onOpen: () => {
          events.onStateChange("signaling-joined");
        },
        onSignal: (data) => this.handleSignal(data),
        onError: (err) => {
          events.onStateChange("signaling-error");
          events.onSignalingError(err.code, err.message, err.fatal);
        },
        onClose: () => {
          events.onStateChange("signaling-closed");
        },
      },
      options,
    );

    if (role === "VIEWER") {
      this.initViewerPeer();
    }
  }

  /** Start the connection: connect to signaling, begin WebRTC negotiation. */
  start(): void {
    this.events.onStateChange("connecting");
    this.signaling.connect();
  }

  /** Send a string through the DataChannel. */
  send(data: string): void {
    if (this.role === "SENDER") {
      for (const state of this.senderPeers.values()) {
        if (state.dc.readyState === "open") {
          state.dc.send(data);
        }
      }
      return;
    }

    if (this.viewerDc?.readyState === "open") {
      this.viewerDc.send(data);
    }
  }

  /** Tear down everything. */
  close(): void {
    this.stopKeepAlive();
    this.closeViewerPeer();
    for (const state of this.senderPeers.values()) {
      state.dc.close();
      state.pc.close();
    }
    this.senderPeers.clear();
    this.signaling.close();
  }

  // --- Private ---

  private initViewerPeer(): void {
    const pc = new RTCPeerConnection({ iceServers: STUN_SERVERS });
    this.viewerPc = pc;

    pc.onicecandidate = (ev) => {
      if (!ev.candidate) return;
      this.signaling.send(
        JSON.stringify({ type: "ice", payload: ev.candidate.toJSON() }),
      );
    };

    pc.onconnectionstatechange = () => {
      this.events.onStateChange(pc.connectionState);
      if (
        pc.connectionState === "disconnected" ||
        pc.connectionState === "failed" ||
        pc.connectionState === "closed"
      ) {
        this.stopKeepAlive();
        this.events.onClose();
      }
    };

    pc.ondatachannel = (ev) => {
      this.setupViewerDataChannel(ev.channel);
    };
  }

  private closeViewerPeer(): void {
    this.viewerDc?.close();
    this.viewerDc = null;
    this.viewerPc?.close();
    this.viewerPc = null;
    this.viewerPendingIce = [];
  }

  private setupViewerDataChannel(channel: RTCDataChannel): void {
    this.viewerDc = channel;

    channel.onopen = () => {
      this.events.onOpen();
      this.events.onStateChange("connected");
      this.startKeepAlive();
    };

    channel.onmessage = (ev) => {
      this.events.onMessage(typeof ev.data === "string" ? ev.data : "");
    };

    channel.onclose = () => {
      this.viewerDc = null;
      this.stopKeepAlive();
      this.events.onClose();
    };
  }

  private async createSenderPeer(viewerId: string): Promise<SenderPeerState> {
    const prev = this.senderPeers.get(viewerId);
    if (prev) {
      prev.dc.close();
      prev.pc.close();
      this.senderPeers.delete(viewerId);
    }

    const pc = new RTCPeerConnection({ iceServers: STUN_SERVERS });
    const dc = pc.createDataChannel(DATACHANNEL_LABEL, { ordered: true });

    const state: SenderPeerState = {
      viewerId,
      pc,
      dc,
      pendingIce: [],
    };

    pc.onicecandidate = (ev) => {
      if (!ev.candidate) return;
      this.signaling.send(
        JSON.stringify({
          to: viewerId,
          type: "ice",
          payload: ev.candidate.toJSON(),
        }),
      );
    };

    pc.onconnectionstatechange = () => {
      const status = pc.connectionState;
      if (status === "failed" || status === "closed") {
        this.senderPeers.delete(viewerId);
        this.updateSenderAggregateState();
      }
    };

    dc.onopen = () => {
      this.events.onOpen();
      this.updateSenderAggregateState();
      this.startKeepAlive();
    };

    dc.onmessage = (ev) => {
      this.events.onMessage(typeof ev.data === "string" ? ev.data : "");
    };

    dc.onclose = () => {
      this.senderPeers.delete(viewerId);
      this.updateSenderAggregateState();
      if (!this.anyOpenSenderChannel()) {
        this.stopKeepAlive();
        this.events.onClose();
      }
    };

    this.senderPeers.set(viewerId, state);
    this.updateSenderAggregateState();
    return state;
  }

  private async createOfferForViewer(viewerId: string): Promise<void> {
    const state = this.senderPeers.get(viewerId);
    if (!state) return;

    const offer = await state.pc.createOffer();
    await state.pc.setLocalDescription(offer);
    this.signaling.send(
      JSON.stringify({
        to: viewerId,
        type: "offer",
        payload: state.pc.localDescription,
      }),
    );
  }

  private async handleSignal(raw: string): Promise<void> {
    if (this.role === "SENDER" && raw.startsWith("READY")) {
      const viewerId = this.parseReadyViewerId(raw);
      if (viewerId) {
        try {
          await this.createSenderPeer(viewerId);
          await this.createOfferForViewer(viewerId);
        } catch (err) {
          console.error("[peer] failed to create offer for viewer:", viewerId, err);
        }
      }
      return;
    }

    let msg: SignalEnvelope;
    try {
      msg = JSON.parse(raw) as SignalEnvelope;
    } catch {
      console.warn("[peer] ignoring non-JSON signaling message:", raw);
      return;
    }

    if (msg.type === "offer") {
      await this.handleOffer(msg);
      return;
    }
    if (msg.type === "answer") {
      await this.handleAnswer(msg);
      return;
    }
    if (msg.type === "ice") {
      await this.handleIce(msg);
    }
  }

  private async handleOffer(msg: SignalEnvelope): Promise<void> {
    if (this.role !== "VIEWER") return;
    if (this.viewerPc === null) return;
    if (this.viewerPc.currentRemoteDescription !== null) {
      console.warn("[peer] ignoring duplicate offer");
      return;
    }

    await this.viewerPc.setRemoteDescription(
      new RTCSessionDescription(msg.payload as RTCSessionDescriptionInit),
    );
    await this.flushPendingIceForViewer();
    const answer = await this.viewerPc.createAnswer();
    await this.viewerPc.setLocalDescription(answer);
    this.signaling.send(
      JSON.stringify({ type: "answer", payload: this.viewerPc.localDescription }),
    );
  }

  private async handleAnswer(msg: SignalEnvelope): Promise<void> {
    if (this.role !== "SENDER") return;
    const viewerId = this.resolveSenderTarget(msg);
    if (!viewerId) return;
    const state = this.senderPeers.get(viewerId);
    if (!state) return;

    if (state.pc.currentRemoteDescription !== null) {
      console.warn("[peer] ignoring duplicate answer");
      return;
    }
    await state.pc.setRemoteDescription(
      new RTCSessionDescription(msg.payload as RTCSessionDescriptionInit),
    );
    await this.flushPendingIceForSender(state);
  }

  private async handleIce(msg: SignalEnvelope): Promise<void> {
    if (this.role === "VIEWER") {
      if (this.viewerPc === null) return;
      const candidate = msg.payload as RTCIceCandidateInit;
      if (this.viewerPc.remoteDescription === null) {
        this.viewerPendingIce.push(candidate);
        return;
      }
      await this.safeAddIceCandidate(this.viewerPc, candidate);
      return;
    }

    const viewerId = this.resolveSenderTarget(msg);
    if (!viewerId) return;
    const state = this.senderPeers.get(viewerId);
    if (!state) return;
    const candidate = msg.payload as RTCIceCandidateInit;
    if (state.pc.remoteDescription === null) {
      state.pendingIce.push(candidate);
      return;
    }
    await this.safeAddIceCandidate(state.pc, candidate);
  }

  private resolveSenderTarget(msg: SignalEnvelope): string | null {
    if (typeof msg.from === "string" && msg.from.length > 0) {
      return msg.from;
    }
    if (this.senderPeers.size === 1) {
      return this.senderPeers.keys().next().value as string;
    }
    return null;
  }

  private parseReadyViewerId(raw: string): string | null {
    if (raw === "READY") return LEGACY_VIEWER_ID;
    if (!raw.startsWith("READY|")) return null;
    const viewerId = raw.slice("READY|".length).trim();
    return viewerId.length > 0 ? viewerId : null;
  }

  private updateSenderAggregateState(): void {
    if (this.role !== "SENDER") return;
    if (this.anyOpenSenderChannel()) {
      this.events.onStateChange("connected");
      return;
    }
    if (this.senderPeers.size > 0) {
      this.events.onStateChange("connecting");
      return;
    }
    this.events.onStateChange("signaling-joined");
  }

  private anyOpenSenderChannel(): boolean {
    for (const state of this.senderPeers.values()) {
      if (state.dc.readyState === "open") return true;
    }
    return false;
  }

  private startKeepAlive(): void {
    if (this.keepAliveTimer !== null) return;
    this.keepAliveTimer = setInterval(() => {
      this.send(serializeKeepAlive());
    }, KEEPALIVE_INTERVAL_MS);
  }

  private stopKeepAlive(): void {
    if (this.keepAliveTimer !== null) {
      clearInterval(this.keepAliveTimer);
      this.keepAliveTimer = null;
    }
  }

  private async flushPendingIceForViewer(): Promise<void> {
    if (this.viewerPc === null || this.viewerPendingIce.length === 0) return;
    const queued = [...this.viewerPendingIce];
    this.viewerPendingIce = [];
    for (const candidate of queued) {
      await this.safeAddIceCandidate(this.viewerPc, candidate);
    }
  }

  private async flushPendingIceForSender(state: SenderPeerState): Promise<void> {
    if (state.pendingIce.length === 0) return;
    const queued = [...state.pendingIce];
    state.pendingIce = [];
    for (const candidate of queued) {
      await this.safeAddIceCandidate(state.pc, candidate);
    }
  }

  private async safeAddIceCandidate(
    pc: RTCPeerConnection,
    candidate: RTCIceCandidateInit,
  ): Promise<void> {
    try {
      await pc.addIceCandidate(new RTCIceCandidate(candidate));
    } catch (err) {
      console.warn("[peer] failed to add ICE candidate:", err);
    }
  }
}

