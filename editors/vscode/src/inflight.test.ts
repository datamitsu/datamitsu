import assert from "node:assert/strict";
import { test } from "node:test";

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
  const firstResult = inFlight.track(() => first.promise);
  const secondResult = inFlight.track(() => second.promise);
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
  const result = inFlight.track(() => request.promise);
  const idle = inFlight.idle();

  request.reject(new Error("request failed"));
  await assert.rejects(result, /request failed/);
  assert.equal(await hasSettled(idle), true);
});

test("InFlight: close releases waiters a request that never settles would hold", async () => {
  const inFlight = new InFlight();
  const never = inFlight.track(() => new Promise<void>(() => {}));
  const idle = inFlight.idle();
  assert.equal(await hasSettled(idle), false);

  inFlight.close();
  assert.equal(await hasSettled(idle), true);
  assert.equal(await hasSettled(inFlight.idle()), true);
  assert.equal(await hasSettled(never), false);
});
