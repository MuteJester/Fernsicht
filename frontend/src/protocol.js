/** Fernsicht wire protocol v2 — pipe-delimited messages over DataChannel. */
const FRACTION_RE = /^(?:0(?:\.\d+)?|1(?:\.0+)?)$/;
function assertNonEmptyField(value, field) {
    if (!value) {
        throw new Error(`${field} must not be empty`);
    }
    return value;
}
/** Parse a pipe-delimited wire message into a typed object. */
export function parseMessage(raw) {
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
            if (parts.length !== 2)
                throw new Error("ID message must have exactly 1 field");
            return { kind: "identity", id: assertNonEmptyField(parts[1], "id") };
        }
        case "START": {
            if (parts.length < 3)
                throw new Error("START message missing fields");
            const taskId = assertNonEmptyField(parts[1], "taskId");
            const label = assertNonEmptyField(parts.slice(2).join("|"), "label");
            return { kind: "start", taskId, label };
        }
        case "P": {
            if (parts.length !== 3)
                throw new Error("P message must have exactly 2 fields");
            const taskId = assertNonEmptyField(parts[1], "taskId");
            const valueRaw = parts[2];
            if (!FRACTION_RE.test(valueRaw)) {
                throw new Error(`P message has invalid value format: ${valueRaw}`);
            }
            const value = Number(valueRaw);
            return { kind: "progress", taskId, value };
        }
        case "END": {
            if (parts.length !== 2)
                throw new Error("END message must have exactly 1 field");
            return { kind: "end", taskId: assertNonEmptyField(parts[1], "taskId") };
        }
        default:
            throw new Error(`Unknown message tag: ${tag}`);
    }
}
// --- Serializers (used by broadcaster) ---
export function serializeIdentity(id) {
    return `ID|${id}`;
}
export function serializeStart(taskId, label) {
    return `START|${taskId}|${label}`;
}
export function serializeProgress(taskId, value) {
    return `P|${taskId}|${value.toFixed(2)}`;
}
export function serializeEnd(taskId) {
    return `END|${taskId}`;
}
export function serializeKeepAlive() {
    return "K";
}
export function serializeReady() {
    return "READY";
}
