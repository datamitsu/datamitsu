// InFlight counts one server session's requests still awaiting a response. The
// server handles one request at a time and cannot read a shutdown until a format
// returns, so a stop during a long format times out and kills it between two
// groups of fix tools. An automatic restart waits for idle() instead.
export class InFlight {
  get size(): number {
    return this.count;
  }
  private count = 0;
  private isClosed = false;

  private waiters: (() => void)[] = [];

  // close releases every idle() waiter: the session is gone, and a request its
  // stopped client never settles must not hold them forever.
  close(): void {
    this.isClosed = true;
    this.release();
  }

  idle(): Promise<void> {
    if (this.count === 0 || this.isClosed) {
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      this.waiters.push(resolve);
    });
  }

  async track<T>(request: () => PromiseLike<T> | T): Promise<T> {
    this.count++;
    try {
      return await request();
    } finally {
      this.count--;
      if (this.count === 0) {
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
