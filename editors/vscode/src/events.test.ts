import assert from "node:assert/strict";
import { test } from "node:test";

import { parseEvent, toStatusUpdate } from "./events";

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
  assert.equal(parseEvent("DEBUG esbuild StripTypes {elapsed: 1}"), undefined); // a --verbose line
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
