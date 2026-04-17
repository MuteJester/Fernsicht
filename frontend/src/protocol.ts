/** Fernsicht wire protocol v2 — pipe-delimited messages over DataChannel. */

export type MessageKind =
  | "identity"
  | "start"
  | "progress"
  | "end"
  | "keepalive"
  | "ready";

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

export type FernsichtMessage =
  | IdentityMessage
  | StartMessage
  | ProgressMessage
  | EndMessage
  | KeepAliveMessage
  | ReadyMessage;

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
    default:
      throw new Error(`Unknown message tag: ${tag}`);
  }
}

// --- Serializers (used by broadcaster) ---

export function serializeIdentity(id: string): string {
  return `ID|${id}`;
}

export function serializeStart(taskId: string, label: string): string {
  return `START|${taskId}|${label}`;
}

export function serializeProgress(
  taskId: string,
  value: number,
  stats?: {
    elapsed?: number;
    eta?: number;
    n?: number;
    total?: number;
    rate?: number;
    unit?: string;
  },
): string {
  const f = (v: number | undefined) => v !== undefined ? v.toFixed(1) : "-";
  const i = (v: number | undefined) => v !== undefined ? String(v) : "-";
  const s = stats ?? {};
  return `P|${taskId}|${value.toFixed(4)}|${f(s.elapsed)}|${f(s.eta)}|${i(s.n)}|${i(s.total)}|${f(s.rate)}|${s.unit ?? "it"}`;
}

export function serializeEnd(taskId: string): string {
  return `END|${taskId}`;
}

export function serializeKeepAlive(): string {
  return "K";
}

export function serializeReady(): string {
  return "READY";
}
