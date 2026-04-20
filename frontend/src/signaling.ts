/** HTTP-based signaling for Fernsicht V2 — fully connectionless. */

export interface SessionInfo {
  room_id: string;
  sender_secret: string;
  viewer_url: string;
  signaling_url: string;
  expires_at: string;
  expires_in: number;
  max_viewers: number;
  poll_interval_hint: number;
}

export interface TicketEntry {
  ticket_id: string;
  offer: RTCSessionDescriptionInit;
}

export interface PollResponse {
  tickets: TicketEntry[];
}

export interface WatchResponse {
  ticket_id: string;
  viewer_secret: string;
  status: string;
  ttl: number;
}

export interface ICEResponse {
  candidates: RTCIceCandidateInit[];
  seq: number;
}

// --- Viewer Signaling (ticket-based) ---

export interface ViewerSignalingEvents {
  onAnswer: (answer: RTCSessionDescriptionInit) => void;
  onError: (message: string, fatal: boolean) => void;
  onQueued: (ticketId: string) => void;
}

const VIEWER_POLL_INTERVAL_MS = 500;
const VIEWER_POLL_MAX_ATTEMPTS = 60; // 30 seconds at 500ms

export class ViewerSignaling {
  private readonly baseURL: string;
  private ticketId: string | null = null;
  /** Per-ticket secret returned by POST /watch. Required to authenticate
   *  subsequent POST /ticket/{id}/ice/viewer submissions. */
  private viewerSecret: string | null = null;
  private pollTimer: ReturnType<typeof setTimeout> | null = null;
  private pollAttempts = 0;
  private stopped = false;

  constructor(
    baseURL: string,
    private readonly events: ViewerSignalingEvents,
  ) {
    this.baseURL = baseURL.replace(/\/+$/, "");
  }

  async watch(roomId: string, offer: RTCSessionDescriptionInit): Promise<boolean> {
    this.stopped = false;
    try {
      const resp = await fetch(`${this.baseURL}/watch`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ room_id: roomId, offer }),
      });

      if (resp.status === 429) {
        const retryAfter = parseInt(resp.headers.get("Retry-After") || "5", 10);
        this.events.onError(`Room busy, retry in ${retryAfter}s`, false);
        return false;
      }
      if (resp.status === 404) {
        this.events.onError("Room not found. The sender may not be active yet.", true);
        return false;
      }
      if (!resp.ok) {
        this.events.onError(`Watch failed: HTTP ${resp.status}`, false);
        return false;
      }

      const data = (await resp.json()) as WatchResponse;
      this.ticketId = data.ticket_id;
      this.viewerSecret = data.viewer_secret;
      this.events.onQueued(data.ticket_id);
      this.pollForAnswer();
      return true;
    } catch (err) {
      this.events.onError(String(err), false);
      return false;
    }
  }

  async postViewerICE(candidates: RTCIceCandidateInit[]): Promise<boolean> {
    if (!this.ticketId || !this.viewerSecret) return false;
    try {
      const resp = await fetch(`${this.baseURL}/ticket/${this.ticketId}/ice/viewer`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ candidates, secret: this.viewerSecret }),
      });
      return resp.ok;
    } catch {
      return false;
    }
  }

  async getSenderICE(since: number): Promise<ICEResponse | null> {
    if (!this.ticketId) return null;
    try {
      const resp = await fetch(
        `${this.baseURL}/ticket/${this.ticketId}/ice/sender?since=${since}`,
      );
      if (!resp.ok) return null;
      return (await resp.json()) as ICEResponse;
    } catch {
      return null;
    }
  }

  stop(): void {
    this.stopped = true;
    this.viewerSecret = null;
    if (this.pollTimer !== null) {
      clearTimeout(this.pollTimer);
      this.pollTimer = null;
    }
  }

  private async pollForAnswer(): Promise<void> {
    if (this.stopped || !this.ticketId) return;

    this.pollAttempts++;
    if (this.pollAttempts > VIEWER_POLL_MAX_ATTEMPTS) {
      this.events.onError("Timed out waiting for sender to respond.", true);
      return;
    }

    try {
      const resp = await fetch(`${this.baseURL}/ticket/${this.ticketId}/answer`);
      if (!resp.ok) {
        if (resp.status === 404) {
          this.events.onError("Ticket expired.", true);
          return;
        }
        this.scheduleAnswerPoll();
        return;
      }

      const data = (await resp.json()) as { status: string; answer?: RTCSessionDescriptionInit };
      if (data.status === "answered" && data.answer) {
        this.events.onAnswer(data.answer);
        return;
      }
      // Still pending
      this.scheduleAnswerPoll();
    } catch {
      this.scheduleAnswerPoll();
    }
  }

  private scheduleAnswerPoll(): void {
    if (this.stopped) return;
    this.pollTimer = setTimeout(() => this.pollForAnswer(), VIEWER_POLL_INTERVAL_MS);
  }
}
