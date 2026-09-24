import assert from "node:assert/strict";
import { test } from "node:test";

import {
  CancellationSource,
  type Disposable,
  isCancellation,
  REQUEST_CANCELLED,
} from "./cancellation";

// cancellationError mirrors vscode.CancellationError: an Error named "Canceled".
// DOMException takes its name as an argument, so the name is never reassigned.
const cancellationError = (): Error => new DOMException("Canceled", "Canceled");

const nextTick = (): Promise<void> =>
  new Promise((resolve) => {
    setTimeout(resolve, 0);
  });

test("CancellationSource: cancel sets the flag and fires each listener once", () => {
  const source = new CancellationSource();
  let fired = 0;
  source.token.onCancellationRequested(() => fired++);
  assert.equal(source.token.isCancellationRequested, false);

  source.cancel();
  source.cancel();
  assert.equal(source.token.isCancellationRequested, true);
  assert.equal(fired, 1);
});

test("CancellationSource: a disposed subscription does not fire", () => {
  const source = new CancellationSource();
  let fired = 0;
  const subscription = source.token.onCancellationRequested(() => fired++);
  subscription.dispose();
  source.cancel();
  assert.equal(fired, 0);
});

test("CancellationSource: listeners get thisArgs and land in disposables", () => {
  const source = new CancellationSource();
  const context = { calls: 0 };
  const disposables: Disposable[] = [];
  source.token.onCancellationRequested(
    function (this: typeof context) {
      this.calls++;
    },
    context,
    disposables,
  );
  assert.equal(disposables.length, 1);
  source.cancel();
  assert.equal(context.calls, 1);
});

test("CancellationSource: a failing listener does not silence the others", () => {
  const source = new CancellationSource();
  let fired = 0;
  source.token.onCancellationRequested(() => {
    throw new Error("listener failed");
  });
  source.token.onCancellationRequested(() => fired++);
  source.cancel();
  assert.equal(fired, 1);
});

test("CancellationSource: a listener added after the cancel runs on a later tick", async () => {
  const source = new CancellationSource();
  source.cancel();
  let fired = 0;
  source.token.onCancellationRequested(() => fired++);
  assert.equal(fired, 0);
  await nextTick();
  assert.equal(fired, 1);

  const disposed = source.token.onCancellationRequested(() => fired++);
  disposed.dispose();
  await nextTick();
  assert.equal(fired, 1);
});

test("CancellationSource: follows its parent, and cancels alone without it", () => {
  const parent = new CancellationSource();
  const child = new CancellationSource(parent.token);
  let fired = 0;
  child.token.onCancellationRequested(() => fired++);
  parent.cancel();
  assert.equal(child.token.isCancellationRequested, true);
  assert.equal(fired, 1);

  const other = new CancellationSource();
  const own = new CancellationSource(other.token);
  own.cancel();
  assert.equal(own.token.isCancellationRequested, true);
  assert.equal(other.token.isCancellationRequested, false);
});

test("CancellationSource: a parent cancelled already cancels it at once", () => {
  const parent = new CancellationSource();
  parent.cancel();
  const child = new CancellationSource(parent.token);
  assert.equal(child.token.isCancellationRequested, true);
});

test("CancellationSource: dispose unlinks it from the parent", () => {
  const parent = new CancellationSource();
  const child = new CancellationSource(parent.token);
  let fired = 0;
  child.token.onCancellationRequested(() => fired++);
  child.dispose();
  parent.cancel();
  assert.equal(child.token.isCancellationRequested, false);
  assert.equal(fired, 0);
});

test("isCancellation: CancellationError and RequestCancelled, nothing else", () => {
  assert.equal(isCancellation(cancellationError()), true);
  assert.equal(isCancellation({ code: REQUEST_CANCELLED, message: "cancelled" }), true);

  assert.equal(isCancellation({ code: -32_803, message: "request failed" }), false);
  assert.equal(isCancellation(new Error("Canceled")), false);
  assert.equal(isCancellation("Canceled"), false);
  assert.equal(isCancellation(null), false);
});
