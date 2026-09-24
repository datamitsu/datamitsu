import { CancellationSource, type CancellationToken } from "./cancellation";

// InFlight tracks one server session's formatting requests still awaiting a
// response. Each runs on a token of its own, linked to VS Code's, so close() can
// cancel them when the server stops: the server stops a cancelled format at its
// next checkpoint, so the shutdown request that follows fits inside the client's
// stop window. A running tool is never stopped. One that outlasts the window
// survives the kill that follows too: it runs in its own process group and
// finishes on its own. A restart after a settings change waits for idle()
// instead, so it does not cut a save's chain of fix tools short.
export class InFlight {
  get size(): number {
    return this.requests.size;
  }
  private isClosed = false;
  private readonly requests = new Set<CancellationSource>();

  private waiters: (() => void)[] = [];

  // close cancels every request still in flight, and any tracked later, and
  // releases every idle() waiter: the session is gone, and a request its stopped
  // client never settles must not hold them forever.
  close(): void {
    this.isClosed = true;
    for (const request of this.requests) {
      request.cancel();
    }
    this.release();
  }

  idle(): Promise<void> {
    if (this.requests.size === 0 || this.isClosed) {
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      this.waiters.push(resolve);
    });
  }

  async track<T>(
    token: CancellationToken | undefined,
    request: (token: CancellationToken) => PromiseLike<T> | T,
  ): Promise<T> {
    const source = new CancellationSource(token);
    if (this.isClosed) {
      source.cancel();
    }
    this.requests.add(source);
    try {
      return await request(source.token);
    } finally {
      source.dispose();
      this.requests.delete(source);
      if (this.requests.size === 0) {
        this.release();
      }
    }
  }

  private release(): void {
    const waiters = this.waiters;
    this.waiters = [];
    for (const resolve of waiters) {
      resolve();
    }
  }
}
