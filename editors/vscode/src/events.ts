// Mirror of datamitsu's internal/uievent.Event — the JSON-L envelope emitted on
// the language server's stderr. Only the fields the status bar reads are relied
// on; everything else is optional and ignored. type and op_id are mandatory on
// every line.

export interface Event {
  bytes_done?: number;
  bytes_total?: number;
  dir?: string;
  index?: number;
  level?: LogLevel;
  msg?: string;
  name?: string;
  op?: string;
  op_id: string;
  percent?: number;
  runs?: number;
  status?: "done" | "fail" | "progress" | "start";
  success?: boolean;
  tool?: string;
  total?: number;
  type: EventType;
}

const EVENT_TYPE_NAMES = [
  "chunk",
  "done",
  "download",
  "error",
  "install",
  "log",
  "phase",
  "tool_run",
] as const;

export type EventType = (typeof EVENT_TYPE_NAMES)[number];

const LOG_LEVEL_NAMES = ["debug", "error", "info", "warn"] as const;

export type LogLevel = (typeof LOG_LEVEL_NAMES)[number];

const EVENT_TYPES: ReadonlySet<string> = new Set(EVENT_TYPE_NAMES);

const LOG_LEVELS: ReadonlySet<string> = new Set(LOG_LEVEL_NAMES);

// Notice is one line for the output channel, at its level.
export interface Notice {
  level: LogLevel;
  message: string;
}

// StatusUpdate is the status-bar effect of one event: a label to show while the
// op is active, undefined to clear the op, and/or an error to surface. An update
// carrying log is a notice for the output channel and leaves the status bar alone.
export interface StatusUpdate {
  error?: string;
  label?: string;
  log?: Notice;
  opId: string;
}

// alertText is a notice's popup: its first line, since a failed tool's message
// carries the tail of the tool's output. The output channel keeps all of it.
export function alertText(message: string): string {
  return `datamitsu: ${message.split(/\r?\n/, 1)[0] ?? ""}`;
}

// isAlertLevel reports a notice that may also pop up, not only reach the output
// channel: the session's first warning or error.
export function isAlertLevel(level: LogLevel): boolean {
  return level === "warn" || level === "error";
}

// isEmptyFormat reports a format request that completed without running any fix
// tool: none applies to the file's type, or the editor policy left them all out.
// runs is omitted from the wire when zero. With no run, success false means the
// request itself failed, which the language client reports on its own, and
// "nothing ran" would misstate why.
export function isEmptyFormat(event: Event): boolean {
  return (
    event.type === "done" &&
    event.op === "format" &&
    (event.runs ?? 0) === 0 &&
    event.success !== false &&
    event.status !== "fail"
  );
}

// parseEvent decodes one JSON-L line into an Event, or undefined when the line is
// blank, not valid JSON (e.g. a plain-text line from an older server), or missing
// the mandatory type/op_id discriminators.
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

// toNotice is the output-channel line an update carries: a log event at its
// level, or an error event at error level. From the language server an error
// event is a fix tool that failed while its format request still succeeded, so
// nothing else tells the user about it.
export function toNotice(update: StatusUpdate): Notice | undefined {
  if (update.log !== undefined) {
    return update.log;
  }
  if (update.error !== undefined) {
    return { level: "error", message: update.error };
  }
  return undefined;
}

// toStatusUpdate is the pure mapping from an event to its status-bar effect, kept
// free of any editor API so it can be unit-tested directly.
export function toStatusUpdate(event: Event): StatusUpdate {
  switch (event.type) {
    case "chunk": {
      if (isTerminal(event.status)) {
        return { opId: event.op_id };
      }
      return { label: chunkLabel(event), opId: event.op_id };
    }
    case "done": {
      return { opId: event.op_id };
    }
    case "download": {
      if (isTerminal(event.status)) {
        return { opId: event.op_id };
      }
      return { label: downloadLabel(event), opId: event.op_id };
    }
    case "error": {
      const message = event.msg ?? "datamitsu error";
      return {
        error: event.tool === undefined ? message : `${toolLabel(event)}: ${message}`,
        opId: event.op_id,
      };
    }
    case "install": {
      if (isTerminal(event.status)) {
        return { opId: event.op_id };
      }
      return { label: `installing ${event.name ?? "tool"}`, opId: event.op_id };
    }
    case "log": {
      return {
        log: { level: logLevel(event.level), message: event.msg ?? "" },
        opId: event.op_id,
      };
    }
    case "phase": {
      if (isTerminal(event.status)) {
        return { opId: event.op_id };
      }
      return { label: event.op ?? "working", opId: event.op_id };
    }
    case "tool_run": {
      if (isTerminal(event.status)) {
        return { opId: event.op_id };
      }
      return { label: toolLabel(event), opId: event.op_id };
    }
  }
}

function chunkLabel(event: Event): string {
  const tool = event.tool ?? "tool";
  if (typeof event.index === "number" && typeof event.total === "number" && event.total > 0) {
    return `${tool} ${event.index}/${event.total}`;
  }
  return tool;
}

function downloadLabel(event: Event): string {
  const name = event.name ?? "artifact";
  const pct = downloadPercent(event);
  return pct === undefined ? `downloading ${name}` : `downloading ${name} ${pct}%`;
}

function downloadPercent(event: Event): number | undefined {
  if (typeof event.percent === "number" && event.percent > 0) {
    return event.percent;
  }
  if (typeof event.bytes_total === "number" && event.bytes_total > 0) {
    return Math.floor(((event.bytes_done ?? 0) * 100) / event.bytes_total);
  }
  return undefined;
}

// logLevel records a missing or unknown level as info, so a newer server's level
// still reaches the output channel.
function logLevel(level: unknown): LogLevel {
  return typeof level === "string" && LOG_LEVELS.has(level) ? (level as LogLevel) : "info";
}

function toolLabel(event: Event): string {
  const tool = event.tool ?? "tool";
  return event.dir ? `${tool} (${event.dir})` : tool;
}
