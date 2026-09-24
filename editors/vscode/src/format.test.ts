import assert from "node:assert/strict";
import { test } from "node:test";

import { CancellationSource, REQUEST_CANCELLED } from "./cancellation";
import { reportFormat } from "./format";

// cancellationError mirrors vscode.CancellationError: an Error named "Canceled".
// DOMException takes its name as an argument, so the name is never reassigned.
const cancellationError = (): Error => new DOMException("Canceled", "Canceled");

function logger(): { lines: string[]; log: (line: string) => void } {
  const lines: string[] = [];
  return {
    lines,
    log: (line) => {
      lines.push(line);
    },
  };
}

test("reportFormat: logs the file and the number of edits", async () => {
  const { lines, log } = logger();
  const edits = await reportFormat("/w/a.ts", new CancellationSource().token, log, () =>
    Promise.resolve(["e1", "e2"]),
  );
  assert.deepEqual(edits, ["e1", "e2"]);
  assert.deepEqual(lines, ["format: /w/a.ts", "format: 2 edit(s)"]);
});

test("reportFormat: no result is 0 edits", async () => {
  const { lines, log } = logger();
  await reportFormat("/w/a.ts", new CancellationSource().token, log, () => {});
  assert.deepEqual(lines, ["format: /w/a.ts", "format: 0 edit(s)"]);
});

test("reportFormat: a request whose token was cancelled logs cancelled, not a count", async () => {
  const { lines, log } = logger();
  const source = new CancellationSource();
  // The client resolves a cancelled request to no edits.
  await reportFormat("/w/a.ts", source.token, log, () => {
    source.cancel();
  });
  assert.deepEqual(lines, ["format: /w/a.ts", "format: cancelled"]);
});

test("reportFormat: a cancellation the server reported is logged and rethrown", async () => {
  const responses = [
    cancellationError(),
    Object.assign(new Error("cancelled"), { code: REQUEST_CANCELLED }),
  ];
  for (const error of responses) {
    const { lines, log } = logger();
    await assert.rejects(
      reportFormat("/w/a.ts", new CancellationSource().token, log, () => Promise.reject(error)),
      (thrown) => thrown === error,
    );
    assert.deepEqual(lines, ["format: /w/a.ts", "format: cancelled"]);
  }
});

test("reportFormat: another failure is rethrown without a result line", async () => {
  const { lines, log } = logger();
  const failure = Object.assign(new Error("request failed"), { code: -32_803 });
  await assert.rejects(
    reportFormat("/w/a.ts", new CancellationSource().token, log, () => Promise.reject(failure)),
    /request failed/,
  );
  assert.deepEqual(lines, ["format: /w/a.ts"]);
});
