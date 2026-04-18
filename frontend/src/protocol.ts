/** Fernsicht wire protocol v2 — pipe-delimited messages over DataChannel. */

export type MessageKind =
  | "identity"
  | "start"
  | "progress"
  | "end"
  | "keepalive"
  | "ready"
  | "presence";

export interface IdentityMessage {
  kind: "identity";
  id: string;
}

export interface StartMessage {
  kind: "start";
  taskId: string;
  label: string;
}

export interface ProgressMessage {
  kind: "progress";
  taskId: string;
  value: number;       // 0.0000 to 1.0000
  elapsed: number | null;  // seconds
  eta: number | null;      // seconds remaining
  n: number | null;        // items completed
  total: number | null;    // total items
  rate: number | null;     // items/sec
  unit: string;            // "it", "batch", etc.
}

export interface EndMessage {
  kind: "end";
  taskId: string;
}

export interface KeepAliveMessage {
  kind: "keepalive";
}

export interface ReadyMessage {
  kind: "ready";
}

export interface PresenceMessage {
  kind: "presence";
  names: string[];
}

export type FernsichtMessage =
  | IdentityMessage
  | StartMessage
  | ProgressMessage
  | EndMessage
  | KeepAliveMessage
  | ReadyMessage
  | PresenceMessage;

function assertNonEmptyField(value: string, field: string): string {
  if (!value) {
    throw new Error(`${field} must not be empty`);
  }
  return value;
}

/** Parse a pipe-delimited wire message into a typed object. */
export function parseMessage(raw: string): FernsichtMessage {
  const trimmed = raw.trim();

  if (trimmed === "K") {
    return { kind: "keepalive" };
  }

  if (trimmed === "READY") {
    return { kind: "ready" };
  }

  const parts = trimmed.split("|");
  const tag = parts[0];

  switch (tag) {
    case "ID": {
      if (parts.length !== 2) throw new Error("ID message must have exactly 1 field");
      return { kind: "identity", id: assertNonEmptyField(parts[1], "id") };
    }
    case "START": {
      if (parts.length < 3) throw new Error("START message missing fields");
      const taskId = assertNonEmptyField(parts[1], "taskId");
      const label = assertNonEmptyField(parts.slice(2).join("|"), "label");
      return { kind: "start", taskId, label };
    }
    case "P": {
      if (parts.length < 3) throw new Error("P message must have at least 2 fields");
      const taskId = assertNonEmptyField(parts[1], "taskId");
      const value = parseFloat(parts[2]);
      if (isNaN(value)) throw new Error(`P message has invalid value: ${parts[2]}`);

      const optNum = (idx: number): number | null => {
        if (idx >= parts.length) return null;
        const v = parts[idx];
        if (v === "-" || v === "") return null;
        const n = parseFloat(v);
        return isNaN(n) ? null : n;
      };
      const optInt = (idx: number): number | null => {
        if (idx >= parts.length) return null;
        const v = parts[idx];
        if (v === "-" || v === "") return null;
        const n = parseInt(v, 10);
        return isNaN(n) ? null : n;
      };

      return {
        kind: "progress",
        taskId,
        value: Math.max(0, Math.min(1, value)),
        elapsed: optNum(3),
        eta: optNum(4),
        n: optInt(5),
        total: optInt(6),
        rate: optNum(7),
        unit: parts.length > 8 ? parts[8] || "it" : "it",
      };
    }
    case "END": {
      if (parts.length !== 2) throw new Error("END message must have exactly 1 field");
      return { kind: "end", taskId: assertNonEmptyField(parts[1], "taskId") };
    }
    case "V": {
      // V|name1|name2|... — authoritative viewer presence list from the
      // sender. Empty `V` (no names) is valid and means nobody.
      return { kind: "presence", names: parts.slice(1).filter((s) => s.length > 0) };
    }
    default:
      throw new Error(`Unknown message tag: ${tag}`);
  }
}

// --- Serializers (used by viewer → sender over DataChannel) ---

export function serializeKeepAlive(): string {
  return "K";
}

export function serializeHello(name: string): string {
  // Sanitize: strip pipes (no escape mechanism), truncate to 32 chars.
  const clean = (name ?? "").replace(/\|/g, "").trim().slice(0, 32);
  return `HELLO|${clean}`;
}
