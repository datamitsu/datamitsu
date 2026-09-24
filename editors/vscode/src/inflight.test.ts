import assert from "node:assert/strict";
import { test } from "node:test";

import { CancellationSource, type CancellationToken } from "./cancellation";
import { InFlight } from "./inflight";

// hasSettled reports whether a promise has settled once pending callbacks have run.
async function hasSettled(promise: Promise<unknown>): Promise<boolean> {
  let isSettled = false;
  promise
    .finally(() => {
      isSettled = true;
    })
    .catch(() => {});
  await new Promise((resolve) => {
    setImmediate(resolve);
  });
  return isSettled;
}

test("InFlight: idle resolves at once when nothing runs", async () => {
  const inFlight = new InFlight();
  assert.equal(inFlight.size, 0);
  assert.equal(await hasSettled(inFlight.idle()), true);
});

test("InFlight: idle waits for the last running request", async () => {
  const inFlight = new InFlight();
  const first = Promise.withResolvers<string>();
  const second = Promise.withResolvers<string>();
  const firstResult = inFlight.track(undefined, () => first.promise);
  const secondResult = inFlight.track(undefined, () => second.promise);
  assert.equal(inFlight.size, 2);

  const idle = inFlight.idle();
  first.resolve("a");
  assert.equal(await firstResult, "a");
  assert.equal(await hasSettled(idle), false);

  second.resolve("b");
  assert.equal(await secondResult, "b");
  assert.equal(await hasSettled(idle), true);
  assert.equal(inFlight.size, 0);
});

test("InFlight: a failed request still counts as finished", async () => {
  const inFlight = new InFlight();
  const request = Promise.withResolvers<string>();
  const result = inFlight.track(undefined, () => request.promise);
  const idle = inFlight.idle();

  request.reject(new Error("request failed"));
  await assert.rejects(result, /request failed/);
  assert.equal(await hasSettled(idle), true);
});

test("InFlight: close releases waiters a request that never settles would hold", async () => {
  const inFlight = new InFlight();
  const never = inFlight.track(undefined, () => new Promise<void>(() => {}));
  const idle = inFlight.idle();
  assert.equal(await hasSettled(idle), false);

  inFlight.close();
  assert.equal(await hasSettled(idle), true);
  assert.equal(await hasSettled(inFlight.idle()), true);
  assert.equal(await hasSettled(never), false);
});

test("InFlight: a request runs on its own token, linked to the editor's", async () => {
  const inFlight = new InFlight();
  const editor = new CancellationSource();
  let seen: CancellationToken | undefined;
  const request = Promise.withResolvers<string>();
  const result = inFlight.track(editor.token, (token) => {
    seen = token;
    return request.promise;
  });

  assert.notEqual(seen, editor.token);
  assert.equal(seen?.isCancellationRequested, false);
  editor.cancel();
  assert.equal(seen?.isCancellationRequested, true);
  request.resolve("done");
  assert.equal(await result, "done");
});

test("InFlight: close cancels the running requests but not the editor's token", async () => {
  const inFlight = new InFlight();
  const editor = new CancellationSource();
  const tokens: CancellationToken[] = [];
  let cancels = 0;
  const pending = [editor.token, undefined].map((parent) =>
    inFlight.track(parent, (token) => {
      tokens.push(token);
      token.onCancellationRequested(() => cancels++);
      return new Promise<void>(() => {});
    }),
  );
  assert.equal(pending.length, 2);

  inFlight.close();
  assert.deepEqual(
    tokens.map((token) => token.isCancellationRequested),
    [true, true],
  );
  assert.equal(cancels, 2);
  assert.equal(editor.token.isCancellationRequested, false);
});

test("InFlight: a request tracked after close starts cancelled", async () => {
  const inFlight = new InFlight();
  inFlight.close();
  const isCancelled = await inFlight.track(undefined, (token) => token.isCancellationRequested);
  assert.equal(isCancelled, true);
});

test("InFlight: a settled request is unlinked from the editor's token", async () => {
  const inFlight = new InFlight();
  const editor = new CancellationSource();
  let token: CancellationToken | undefined;
  await inFlight.track(editor.token, (own) => {
    token = own;
  });
  editor.cancel();
  assert.equal(token?.isCancellationRequested, false);
});
