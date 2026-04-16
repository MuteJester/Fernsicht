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
  value: number; // 0.00 to 1.00
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

const FRACTION_RE = /^(?:0(?:\.\d+)?|1(?:\.0+)?)$/;

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
      if (parts.length !== 3) throw new Error("P message must have exactly 2 fields");
      const taskId = assertNonEmptyField(parts[1], "taskId");
      const valueRaw = parts[2];
      if (!FRACTION_RE.test(valueRaw)) {
        throw new Error(`P message has invalid value format: ${valueRaw}`);
      }
      const value = Number(valueRaw);
      return { kind: "progress", taskId, value };
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

export function serializeProgress(taskId: string, value: number): string {
  return `P|${taskId}|${value.toFixed(2)}`;
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
