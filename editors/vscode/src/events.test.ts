import assert from "node:assert/strict";
import { test } from "node:test";

import {
  alertText,
  isAlertLevel,
  isEmptyFormat,
  type LogLevel,
  parseEvent,
  toNotice,
  toStatusUpdate,
} from "./events";

test("parseEvent decodes a typed line", () => {
  const event = parseEvent(
    `{"type":"download","op_id":"dl-1","status":"progress","name":"golangci","percent":42}`,
  );
  assert.equal(event?.type, "download");
  assert.equal(event?.op_id, "dl-1");
  assert.equal(event?.percent, 42);
});

test("parseEvent rejects blank, non-JSON, and lines missing discriminators", () => {
  assert.equal(parseEvent(""), undefined);
  assert.equal(parseEvent(" ".repeat(3)), undefined);
  assert.equal(parseEvent("DEBUG esbuild StripTypes {elapsed: 1}"), undefined); // plain text
  assert.equal(parseEvent(`{"op_id":"x"}`), undefined); // no type
  assert.equal(parseEvent(`{"type":"download"}`), undefined); // no op_id
  assert.equal(parseEvent(`{"type":"nope","op_id":"x"}`), undefined); // unknown type
  assert.equal(parseEvent("42"), undefined);
});

test("toStatusUpdate: download progress shows percent, terminal clears the op", () => {
  assert.deepEqual(
    toStatusUpdate({
      name: "ruff",
      op_id: "dl-1",
      percent: 47,
      status: "progress",
      type: "download",
    }),
    { label: "downloading ruff 47%", opId: "dl-1" },
  );
  assert.deepEqual(
    toStatusUpdate({ name: "ruff", op_id: "dl-1", status: "done", type: "download" }),
    {
      opId: "dl-1",
    },
  );
});

test("toStatusUpdate: download falls back to bytes when percent absent", () => {
  assert.deepEqual(
    toStatusUpdate({
      bytes_done: 50,
      bytes_total: 200,
      name: "x",
      op_id: "dl-2",
      status: "progress",
      type: "download",
    }),
    { label: "downloading x 25%", opId: "dl-2" },
  );
  // Unknown length (no percent, no total) -> no percentage.
  assert.deepEqual(
    toStatusUpdate({ name: "y", op_id: "dl-3", status: "progress", type: "download" }),
    {
      label: "downloading y",
      opId: "dl-3",
    },
  );
});

test("toStatusUpdate: chunk, tool_run, install, phase labels", () => {
  assert.equal(
    toStatusUpdate({
      index: 3,
      op_id: "c",
      status: "progress",
      tool: "eslint",
      total: 10,
      type: "chunk",
    }).label,
    "eslint 3/10",
  );
  assert.equal(
    toStatusUpdate({ dir: "pkg/a", op_id: "t", status: "start", tool: "gofmt", type: "tool_run" })
      .label,
    "gofmt (pkg/a)",
  );
  assert.equal(
    toStatusUpdate({ name: "uv", op_id: "i", status: "start", type: "install" }).label,
    "installing uv",
  );
  assert.equal(
    toStatusUpdate({ op: "fix", op_id: "p", status: "start", type: "phase" }).label,
    "fix",
  );
});

test("toStatusUpdate: error surfaces a message and done clears", () => {
  assert.deepEqual(toStatusUpdate({ msg: "boom", op_id: "e", type: "error" }), {
    error: "boom",
    opId: "e",
  });
  assert.deepEqual(toStatusUpdate({ op_id: "p", status: "done", type: "done" }), { opId: "p" });
});

test("toStatusUpdate: error names the failing tool and its directory", () => {
  assert.deepEqual(
    toStatusUpdate({
      dir: "pkg/a",
      msg: "exited with code 1",
      op_id: "fmt-1:gofmt:pkg/a",
      tool: "gofmt",
      type: "error",
    }),
    { error: "gofmt (pkg/a): exited with code 1", opId: "fmt-1:gofmt:pkg/a" },
  );
});

test("parseEvent + toStatusUpdate: log is a notice at its level, never a status change", () => {
  const event = parseEvent(
    `{"type":"log","op_id":"lsp-1","level":"warn","msg":"format.tools: unknown tool \\"x\\""}`,
  );
  assert.equal(event?.type, "log");
  assert.equal(event?.level, "warn");
  assert.deepEqual(toStatusUpdate(event ?? { op_id: "", type: "log" }), {
    log: { level: "warn", message: 'format.tools: unknown tool "x"' },
    opId: "lsp-1",
  });

  const info = toStatusUpdate({ level: "info", msg: "format policy", op_id: "l", type: "log" });
  assert.deepEqual(info, { log: { level: "info", message: "format policy" }, opId: "l" });
  assert.equal(info.label, undefined);
  assert.equal(info.error, undefined);

  // A missing or unknown level is logged as info.
  assert.equal(toStatusUpdate({ msg: "m", op_id: "l", type: "log" }).log?.level, "info");
  const unknown = parseEvent(`{"type":"log","op_id":"l","level":"fatal","msg":"m"}`);
  assert.equal(toStatusUpdate(unknown ?? { op_id: "", type: "log" }).log?.level, "info");
});

test("toStatusUpdate: every log level reaches the output channel as itself", () => {
  const levels: LogLevel[] = ["debug", "info", "warn", "error"];
  for (const level of levels) {
    const event = parseEvent(JSON.stringify({ level, msg: "m", op_id: "log-1", type: "log" }));
    assert.deepEqual(toStatusUpdate(event ?? { op_id: "", type: "log" }), {
      log: { level, message: "m" },
      opId: "log-1",
    });
  }
});

test("isAlertLevel: only warnings and errors may pop up", () => {
  assert.equal(isAlertLevel("debug"), false);
  assert.equal(isAlertLevel("info"), false);
  assert.equal(isAlertLevel("warn"), true);
  assert.equal(isAlertLevel("error"), true);
});

test("toNotice: a failed tool is an error notice, so it can pop up like a warning", () => {
  // The format request itself succeeds, so this event is the only report.
  const failed = toStatusUpdate({
    dir: "web",
    msg: "exited with code 2\n[error] src/a.ts: SyntaxError",
    op_id: "fmt-1:prettier:web",
    tool: "prettier",
    type: "error",
  });
  const notice = toNotice(failed);
  assert.deepEqual(notice, {
    level: "error",
    message: "prettier (web): exited with code 2\n[error] src/a.ts: SyntaxError",
  });
  assert.ok(notice !== undefined && isAlertLevel(notice.level));
});

test("toNotice: a log is a notice at its level; progress carries none", () => {
  assert.deepEqual(
    toNotice(toStatusUpdate({ level: "debug", msg: "m", op_id: "l", type: "log" })),
    {
      level: "debug",
      message: "m",
    },
  );
  assert.equal(
    toNotice(toStatusUpdate({ op_id: "t", status: "start", tool: "gofmt", type: "tool_run" })),
    undefined,
  );
  assert.equal(toNotice(toStatusUpdate({ op: "format", op_id: "f", type: "done" })), undefined);
});

test("alertText: the popup keeps the first line, the output channel the rest", () => {
  assert.equal(alertText("cache save failed"), "datamitsu: cache save failed");
  assert.equal(
    alertText("prettier (web): exited with code 2\r\n[error] src/a.ts: SyntaxError"),
    "datamitsu: prettier (web): exited with code 2",
  );
  assert.equal(alertText(""), "datamitsu: ");
});

test("isEmptyFormat: a successful format that ran no tool", () => {
  // runs is omitted on the wire when zero.
  assert.equal(isEmptyFormat({ op: "format", op_id: "fmt-1", success: true, type: "done" }), true);
  assert.equal(
    isEmptyFormat({ op: "format", op_id: "fmt-1", runs: 0, success: true, type: "done" }),
    true,
  );
  assert.equal(
    isEmptyFormat({ op: "format", op_id: "fmt-1", runs: 2, success: true, type: "done" }),
    false,
  );
  // With no run, a failure is the request's own, which the client reports.
  assert.equal(
    isEmptyFormat({ op: "format", op_id: "fmt-1", success: false, type: "done" }),
    false,
  );
  assert.equal(
    isEmptyFormat({ op: "format", op_id: "fmt-1", status: "fail", type: "done" }),
    false,
  );
  // Other operations and other event types never count.
  assert.equal(isEmptyFormat({ op: "fix", op_id: "run-1", type: "done" }), false);
  assert.equal(
    isEmptyFormat({ op: "format", op_id: "fmt-1", status: "start", type: "phase" }),
    false,
  );
});
