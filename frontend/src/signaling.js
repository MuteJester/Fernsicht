/** WebSocket signaling client for Fernsicht room-based handshake. */
const RECONNECT_BASE_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 30000;
export class SignalingClient {
    constructor(url, roomId, role, events, options = {}) {
        this.ws = null;
        this.reconnectTimer = null;
        this.reconnectAttempt = 0;
        this.closed = false;
        this.url = url;
        this.roomId = roomId;
        this.role = role;
        this.events = events;
        this.senderJoinToken = options.senderJoinToken?.trim() || null;
    }
    connect() {
        if (this.ws !== null &&
            (this.ws.readyState === WebSocket.OPEN ||
                this.ws.readyState === WebSocket.CONNECTING)) {
            return;
        }
        this.closed = false;
        this.ws = new WebSocket(this.url);
        this.ws.onopen = () => {
            console.log("[signaling] ws open, sending JOIN");
            this.reconnectAttempt = 0;
            this.ws.send(this.buildJoinCommand());
            this.events.onOpen();
        };
        this.ws.onmessage = (ev) => {
            const data = typeof ev.data === "string" ? ev.data : "";
            if (data.startsWith("ERROR|")) {
                const reason = data.slice("ERROR|".length) || "UNKNOWN";
                this.events.onError(new Error(`Signaling server error: ${reason}`));
                if (reason === "ROLE_TAKEN") {
                    this.closed = true;
                }
                return;
            }
            this.events.onSignal(data);
        };
        this.ws.onerror = (ev) => {
            this.events.onError(ev);
        };
        this.ws.onclose = (ev) => {
            // Policy violations generally indicate a client-side configuration issue.
            if (ev.code === 1008) {
                this.closed = true;
            }
            this.events.onClose();
            this.scheduleReconnect();
        };
    }
    send(data) {
        if (this.ws?.readyState === WebSocket.OPEN) {
            this.ws.send(data);
        }
    }
    close() {
        this.closed = true;
        if (this.reconnectTimer !== null) {
            clearTimeout(this.reconnectTimer);
            this.reconnectTimer = null;
        }
        this.ws?.close();
        this.ws = null;
    }
    buildJoinCommand() {
        if (this.role === "SENDER" && this.senderJoinToken !== null) {
            return `JOIN|${this.roomId}|${this.role}|${this.senderJoinToken}`;
        }
        return `JOIN|${this.roomId}|${this.role}`;
    }
    scheduleReconnect() {
        if (this.closed)
            return;
        if (this.reconnectTimer !== null)
            return;
        const delayMs = this.nextReconnectDelayMs();
        this.reconnectTimer = setTimeout(() => {
            this.reconnectTimer = null;
            this.connect();
        }, delayMs);
    }
    nextReconnectDelayMs() {
        const exponentialFactor = 2 ** Math.min(this.reconnectAttempt, 5);
        const baseDelay = RECONNECT_BASE_DELAY_MS * exponentialFactor;
        const jitter = Math.floor(Math.random() * 500);
        this.reconnectAttempt += 1;
        return Math.min(RECONNECT_MAX_DELAY_MS, baseDelay + jitter);
    }
}
