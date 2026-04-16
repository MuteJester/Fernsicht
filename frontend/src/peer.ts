/** WebRTC peer connection and DataChannel management for Fernsicht. */

import { SignalingClient, type Role } from "./signaling";
import { serializeKeepAlive } from "./protocol";

/** SDP/ICE messages relayed through the signaling server. */
interface SignalEnvelope {
  type: "offer" | "answer" | "ice";
  payload: RTCSessionDescriptionInit | RTCIceCandidateInit;
}

export interface PeerEvents {
  onOpen: () => void;
  onMessage: (data: string) => void;
  onClose: () => void;
  onStateChange: (state: string) => void;
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

export class FernsichtPeer {
  private pc: RTCPeerConnection;
  private dc: RTCDataChannel | null = null;
  private signaling: SignalingClient;
  private readonly events: PeerEvents;
  private readonly role: Role;
  private keepAliveTimer: ReturnType<typeof setInterval> | null = null;
  private offerCreated = false;
  private pendingIce: RTCIceCandidateInit[] = [];

  constructor(
    signalingUrl: string,
    roomId: string,
    role: Role,
    events: PeerEvents,
    options: PeerOptions = {},
  ) {
    this.role = role;
    this.events = events;

    this.pc = new RTCPeerConnection({ iceServers: STUN_SERVERS });

    this.signaling = new SignalingClient(signalingUrl, roomId, role, {
      onOpen: () => {
        console.log("[peer] signaling onOpen, role:", this.role);
        events.onStateChange("signaling-joined");
      },
      onSignal: (data) => this.handleSignal(data),
      onError: (err) => {
        console.error("[signaling] error:", err);
        events.onStateChange("signaling-error");
      },
      onClose: () => {
        events.onStateChange("signaling-closed");
      },
    }, options);

    // Send ICE candidates to the remote peer via signaling
    this.pc.onicecandidate = (ev) => {
      if (ev.candidate) {
        this.signaling.send(
          JSON.stringify({ type: "ice", payload: ev.candidate.toJSON() }),
        );
      }
    };

    this.pc.onconnectionstatechange = () => {
      events.onStateChange(this.pc.connectionState);
      if (
        this.pc.connectionState === "disconnected" ||
        this.pc.connectionState === "failed" ||
        this.pc.connectionState === "closed"
      ) {
        this.stopKeepAlive();
        events.onClose();
      }
    };

    // VIEWER waits for the remote SENDER to create the DataChannel
    if (role === "VIEWER") {
      this.pc.ondatachannel = (ev) => {
        this.setupDataChannel(ev.channel);
      };
    }
  }

  /** Start the connection: connect to signaling, begin WebRTC negotiation. */
  start(): void {
    this.events.onStateChange("connecting");
    this.signaling.connect();
  }

  /** Send a string through the DataChannel. */
  send(data: string): void {
    if (this.dc?.readyState === "open") {
      this.dc.send(data);
    }
  }

  /** Tear down everything. */
  close(): void {
    this.stopKeepAlive();
    this.dc?.close();
    this.pc.close();
    this.signaling.close();
  }

  // --- Private ---

  private setupDataChannel(channel: RTCDataChannel): void {
    this.dc = channel;

    channel.onopen = () => {
      this.events.onOpen();
      this.startKeepAlive();
    };

    channel.onmessage = (ev) => {
      this.events.onMessage(typeof ev.data === "string" ? ev.data : "");
    };

    channel.onclose = () => {
      this.dc = null;
      this.stopKeepAlive();
      this.events.onClose();
    };
  }

  private async createOfferAndChannel(): Promise<void> {
    if (this.offerCreated) return;
    this.offerCreated = true;

    // SENDER creates the DataChannel, then the SDP offer
    if (this.dc === null) {
      this.dc = this.pc.createDataChannel(DATACHANNEL_LABEL, { ordered: true });
      this.setupDataChannel(this.dc);
    }

    try {
      const offer = await this.pc.createOffer();
      await this.pc.setLocalDescription(offer);
      this.signaling.send(
        JSON.stringify({ type: "offer", payload: this.pc.localDescription }),
      );
    } catch (err) {
      this.offerCreated = false;
      throw err;
    }
  }

  private async handleSignal(raw: string): Promise<void> {
    // SENDER: create offer when signaling server confirms both peers are present.
    if (raw === "READY" && this.role === "SENDER") {
      try {
        await this.createOfferAndChannel();
      } catch (err) {
        console.error("[peer] failed to create offer:", err);
      }
      return;
    }

    let msg: SignalEnvelope;
    try {
      msg = JSON.parse(raw) as SignalEnvelope;
    } catch {
      // Not JSON — could be a protocol message like READY from the other side
      console.warn("[peer] ignoring non-JSON signaling message:", raw);
      return;
    }

    switch (msg.type) {
      case "offer": {
        if (this.pc.currentRemoteDescription !== null) {
          console.warn("[peer] ignoring duplicate offer");
          break;
        }
        // VIEWER receives offer from SENDER
        await this.pc.setRemoteDescription(
          new RTCSessionDescription(msg.payload as RTCSessionDescriptionInit),
        );
        await this.flushPendingIce();
        const answer = await this.pc.createAnswer();
        await this.pc.setLocalDescription(answer);
        this.signaling.send(
          JSON.stringify({ type: "answer", payload: this.pc.localDescription }),
        );
        break;
      }
      case "answer": {
        if (this.pc.currentRemoteDescription !== null) {
          console.warn("[peer] ignoring duplicate answer");
          break;
        }
        // SENDER receives answer from VIEWER
        await this.pc.setRemoteDescription(
          new RTCSessionDescription(msg.payload as RTCSessionDescriptionInit),
        );
        await this.flushPendingIce();
        break;
      }
      case "ice": {
        const candidate = msg.payload as RTCIceCandidateInit;
        if (this.pc.remoteDescription === null) {
          this.pendingIce.push(candidate);
          break;
        }
        await this.safeAddIceCandidate(candidate);
        break;
      }
    }
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

  private async flushPendingIce(): Promise<void> {
    if (this.pendingIce.length === 0) return;
    const queued = [...this.pendingIce];
    this.pendingIce = [];
    for (const candidate of queued) {
      await this.safeAddIceCandidate(candidate);
    }
  }

  private async safeAddIceCandidate(candidate: RTCIceCandidateInit): Promise<void> {
    try {
      await this.pc.addIceCandidate(new RTCIceCandidate(candidate));
    } catch (err) {
      console.warn("[peer] failed to add ICE candidate:", err);
    }
  }
}
