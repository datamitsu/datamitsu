// Mirror of datamitsu's internal/uievent.Event — the JSON-L envelope emitted on
// the language server's stderr. Only the fields the status bar reads are relied
// on; everything else is optional and ignored. type and op_id are mandatory on
// every line.

export interface Event {
  bytes_done?: number;
  bytes_total?: number;
  dir?: string;
  index?: number;
  msg?: string;
  name?: string;
  op?: string;
  op_id: string;
  percent?: number;
  status?: "done" | "fail" | "progress" | "start";
  success?: boolean;
  tool?: string;
  total?: number;
  type: EventType;
}

export type EventType = "chunk" | "done" | "download" | "error" | "install" | "phase" | "tool_run";

const EVENT_TYPES: ReadonlySet<string> = new Set<EventType>([
  "chunk",
  "done",
  "download",
  "error",
  "install",
  "phase",
  "tool_run",
]);

// StatusUpdate is the status-bar effect of one event: a label to show while the
// op is active, undefined to clear the op, and/or an error to surface.
export interface StatusUpdate {
  error?: string;
  label?: string;
  opId: string;
}

// parseEvent decodes one JSON-L line into an Event, or undefined when the line is
// blank, not valid JSON (e.g. a `--verbose` debug line that is not part of the
// typed stream), or missing the mandatory type/op_id discriminators.
export function parseEvent(line: string): Event | undefined {
  const trimmed = line.trim();
  if (trimmed === "") {
    return undefined;
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return undefined;
  }
  if (typeof parsed !== "object" || parsed === null) {
    return undefined;
  }

  const record = parsed as Record<string, unknown>;
  if (
    typeof record.type !== "string" ||
    !EVENT_TYPES.has(record.type) ||
    typeof record.op_id !== "string"
  ) {
    return undefined;
  }
  return record as unknown as Event;
}

const isTerminal = (status: string | undefined): boolean => status === "done" || status === "fail";

// toStatusUpdate is the pure mapping from an event to its status-bar effect, kept
// free of any editor API so it can be unit-tested directly.
export function toStatusUpdate(ev: Event): StatusUpdate {
  switch (ev.type) {
    case "chunk": {
      if (isTerminal(ev.status)) {
        return { opId: ev.op_id };
      }
      return { label: chunkLabel(ev), opId: ev.op_id };
    }
    case "done": {
      return { opId: ev.op_id };
    }
    case "download": {
      if (isTerminal(ev.status)) {
        return { opId: ev.op_id };
      }
      return { label: downloadLabel(ev), opId: ev.op_id };
    }
    case "error": {
      return { error: ev.msg ?? "datamitsu error", opId: ev.op_id };
    }
    case "install": {
      if (isTerminal(ev.status)) {
        return { opId: ev.op_id };
      }
      return { label: `installing ${ev.name ?? "tool"}`, opId: ev.op_id };
    }
    case "phase": {
      if (isTerminal(ev.status)) {
        return { opId: ev.op_id };
      }
      return { label: ev.op ?? "working", opId: ev.op_id };
    }
    case "tool_run": {
      if (isTerminal(ev.status)) {
        return { opId: ev.op_id };
      }
      return {
        label: ev.dir ? `${ev.tool ?? "tool"} (${ev.dir})` : (ev.tool ?? "tool"),
        opId: ev.op_id,
      };
    }
  }
}

function chunkLabel(ev: Event): string {
  const tool = ev.tool ?? "tool";
  if (typeof ev.index === "number" && typeof ev.total === "number" && ev.total > 0) {
    return `${tool} ${ev.index}/${ev.total}`;
  }
  return tool;
}

function downloadLabel(ev: Event): string {
  const name = ev.name ?? "artifact";
  const pct = downloadPercent(ev);
  return pct === undefined ? `downloading ${name}` : `downloading ${name} ${pct}%`;
}

function downloadPercent(ev: Event): number | undefined {
  if (typeof ev.percent === "number" && ev.percent > 0) {
    return ev.percent;
  }
  if (typeof ev.bytes_total === "number" && ev.bytes_total > 0) {
    return Math.floor(((ev.bytes_done ?? 0) * 100) / ev.bytes_total);
  }
  return undefined;
}
