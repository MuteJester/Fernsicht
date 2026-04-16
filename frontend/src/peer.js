/** WebRTC peer connection and DataChannel management for Fernsicht. */
import { SignalingClient } from "./signaling";
import { serializeKeepAlive } from "./protocol";
const STUN_SERVERS = [
    { urls: "stun:stun.l.google.com:19302" },
    { urls: "stun:stun1.l.google.com:19302" },
];
const KEEPALIVE_INTERVAL_MS = 20000;
const DATACHANNEL_LABEL = "fernsicht";
export class FernsichtPeer {
    constructor(signalingUrl, roomId, role, events, options = {}) {
        this.dc = null;
        this.keepAliveTimer = null;
        this.offerCreated = false;
        this.pendingIce = [];
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
                this.signaling.send(JSON.stringify({ type: "ice", payload: ev.candidate.toJSON() }));
            }
        };
        this.pc.onconnectionstatechange = () => {
            events.onStateChange(this.pc.connectionState);
            if (this.pc.connectionState === "disconnected" ||
                this.pc.connectionState === "failed" ||
                this.pc.connectionState === "closed") {
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
    start() {
        this.events.onStateChange("connecting");
        this.signaling.connect();
    }
    /** Send a string through the DataChannel. */
    send(data) {
        if (this.dc?.readyState === "open") {
            this.dc.send(data);
        }
    }
    /** Tear down everything. */
    close() {
        this.stopKeepAlive();
        this.dc?.close();
        this.pc.close();
        this.signaling.close();
    }
    // --- Private ---
    setupDataChannel(channel) {
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
    async createOfferAndChannel() {
        if (this.offerCreated)
            return;
        this.offerCreated = true;
        // SENDER creates the DataChannel, then the SDP offer
        if (this.dc === null) {
            this.dc = this.pc.createDataChannel(DATACHANNEL_LABEL, { ordered: true });
            this.setupDataChannel(this.dc);
        }
        try {
            const offer = await this.pc.createOffer();
            await this.pc.setLocalDescription(offer);
            this.signaling.send(JSON.stringify({ type: "offer", payload: this.pc.localDescription }));
        }
        catch (err) {
            this.offerCreated = false;
            throw err;
        }
    }
    async handleSignal(raw) {
        // SENDER: create offer when signaling server confirms both peers are present.
        if (raw === "READY" && this.role === "SENDER") {
            try {
                await this.createOfferAndChannel();
            }
            catch (err) {
                console.error("[peer] failed to create offer:", err);
            }
            return;
        }
        let msg;
        try {
            msg = JSON.parse(raw);
        }
        catch {
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
                await this.pc.setRemoteDescription(new RTCSessionDescription(msg.payload));
                await this.flushPendingIce();
                const answer = await this.pc.createAnswer();
                await this.pc.setLocalDescription(answer);
                this.signaling.send(JSON.stringify({ type: "answer", payload: this.pc.localDescription }));
                break;
            }
            case "answer": {
                if (this.pc.currentRemoteDescription !== null) {
                    console.warn("[peer] ignoring duplicate answer");
                    break;
                }
                // SENDER receives answer from VIEWER
                await this.pc.setRemoteDescription(new RTCSessionDescription(msg.payload));
                await this.flushPendingIce();
                break;
            }
            case "ice": {
                const candidate = msg.payload;
                if (this.pc.remoteDescription === null) {
                    this.pendingIce.push(candidate);
                    break;
                }
                await this.safeAddIceCandidate(candidate);
                break;
            }
        }
    }
    startKeepAlive() {
        if (this.keepAliveTimer !== null)
            return;
        this.keepAliveTimer = setInterval(() => {
            this.send(serializeKeepAlive());
        }, KEEPALIVE_INTERVAL_MS);
    }
    stopKeepAlive() {
        if (this.keepAliveTimer !== null) {
            clearInterval(this.keepAliveTimer);
            this.keepAliveTimer = null;
        }
    }
    async flushPendingIce() {
        if (this.pendingIce.length === 0)
            return;
        const queued = [...this.pendingIce];
        this.pendingIce = [];
        for (const candidate of queued) {
            await this.safeAddIceCandidate(candidate);
        }
    }
    async safeAddIceCandidate(candidate) {
        try {
            await this.pc.addIceCandidate(new RTCIceCandidate(candidate));
        }
        catch (err) {
            console.warn("[peer] failed to add ICE candidate:", err);
        }
    }
}
