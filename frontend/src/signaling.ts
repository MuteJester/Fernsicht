/** WebSocket signaling client for Fernsicht room-based handshake. */

export type Role = "SENDER" | "VIEWER";

export interface SignalingError {
  code: string;
  message: string;
  fatal: boolean;
}

export interface SignalingEvents {
  onOpen: () => void;
  onSignal: (data: string) => void;
  onError: (err: SignalingError) => void;
  onClose: () => void;
}

export interface SignalingClientOptions {
  senderJoinToken?: string;
}

const RECONNECT_BASE_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 30000;

export class SignalingClient {
  private ws: WebSocket | null = null;
  private readonly url: string;
  private readonly roomId: string;
  private readonly role: Role;
  private readonly events: SignalingEvents;
  private readonly senderJoinToken: string | null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectAttempt = 0;
  private closed = false;

  constructor(
    url: string,
    roomId: string,
    role: Role,
    events: SignalingEvents,
    options: SignalingClientOptions = {},
  ) {
    this.url = url;
    this.roomId = roomId;
    this.role = role;
    this.events = events;
    this.senderJoinToken = options.senderJoinToken?.trim() || null;
  }

  connect(): void {
    if (
      this.ws !== null &&
      (this.ws.readyState === WebSocket.OPEN ||
        this.ws.readyState === WebSocket.CONNECTING)
    ) {
      return;
    }

    this.closed = false;
    this.ws = new WebSocket(this.url);

    this.ws.onopen = () => {
      console.log("[signaling] ws open, sending JOIN");
      this.reconnectAttempt = 0;
      this.ws!.send(this.buildJoinCommand());
      this.events.onOpen();
    };

    this.ws.onmessage = (ev: MessageEvent) => {
      const data = typeof ev.data === "string" ? ev.data : "";
      if (data.startsWith("ERROR|")) {
        const reason = data.slice("ERROR|".length) || "UNKNOWN";
        this.events.onError({
          code: reason,
          message: `Signaling server error: ${reason}`,
          fatal: true,
        });
        if (reason === "ROLE_TAKEN") {
          this.closed = true;
        }
        return;
      }
      this.events.onSignal(data);
    };

    this.ws.onerror = (ev: Event) => {
      this.events.onError({
        code: "WS_ERROR",
        message: String(ev.type || "websocket error"),
        fatal: false,
      });
    };

    this.ws.onclose = (ev: CloseEvent) => {
      // Policy violations generally indicate a client-side configuration issue.
      if (ev.code === 1008) {
        if (ev.reason) {
          this.events.onError({
            code: "POLICY_VIOLATION",
            message: ev.reason,
            fatal: true,
          });
        }
        this.closed = true;
      }
      this.events.onClose();
      this.scheduleReconnect();
    };
  }

  send(data: string): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(data);
    }
  }

  close(): void {
    this.closed = true;
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
  }

  private buildJoinCommand(): string {
    if (this.role === "SENDER" && this.senderJoinToken !== null) {
      return `JOIN|${this.roomId}|${this.role}|${this.senderJoinToken}`;
    }
    return `JOIN|${this.roomId}|${this.role}`;
  }

  private scheduleReconnect(): void {
    if (this.closed) return;
    if (this.reconnectTimer !== null) return;
    const delayMs = this.nextReconnectDelayMs();
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delayMs);
  }

  private nextReconnectDelayMs(): number {
    const exponentialFactor = 2 ** Math.min(this.reconnectAttempt, 5);
    const baseDelay = RECONNECT_BASE_DELAY_MS * exponentialFactor;
    const jitter = Math.floor(Math.random() * 500);
    this.reconnectAttempt += 1;
    return Math.min(RECONNECT_MAX_DELAY_MS, baseDelay + jitter);
  }
}
