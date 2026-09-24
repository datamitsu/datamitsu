// A cancellation token of the extension's own, so stopping the server can cancel
// a format VS Code still waits on. vscode-languageclient accepts any token with a
// boolean isCancellationRequested and an onCancellationRequested event, so this
// one is kept free of the vscode module and is unit-testable.

// The LSP RequestCancelled code: the answer to a cancelled request.
export const REQUEST_CANCELLED = -32_800;

export interface CancellationToken {
  readonly isCancellationRequested: boolean;
  readonly onCancellationRequested: (
    listener: (event?: unknown) => unknown,
    thisArgument?: unknown,
    disposables?: Disposable[],
  ) => Disposable;
}

export interface Disposable {
  dispose: () => unknown;
}

type Listener = () => void;

interface MutableToken extends CancellationToken {
  isCancellationRequested: boolean;
}

// CancellationSource owns one token. Linked to a parent, it is cancelled with it;
// cancel() cancels it alone. dispose() unlinks it once its request is settled.
export class CancellationSource {
  readonly token: CancellationToken;
  private link: Disposable | undefined;
  private readonly listeners = new Set<Listener>();
  private readonly state: MutableToken;

  constructor(parent?: CancellationToken) {
    this.state = {
      isCancellationRequested: parent?.isCancellationRequested === true,
      onCancellationRequested: (listener, thisArgument, disposables) => {
        const subscription = this.subscribe(() => {
          listener.call(thisArgument);
        });
        disposables?.push(subscription);
        return subscription;
      },
    };
    this.token = this.state;
    if (parent !== undefined && !this.state.isCancellationRequested) {
      this.link = parent.onCancellationRequested(() => {
        this.cancel();
      });
    }
  }

  cancel(): void {
    if (this.state.isCancellationRequested) {
      return;
    }
    this.state.isCancellationRequested = true;
    this.unlink();
    const listeners = [...this.listeners];
    this.listeners.clear();
    for (const listener of listeners) {
      try {
        listener();
      } catch {
        // One failing listener must not keep the others from hearing the cancel.
      }
    }
  }

  dispose(): void {
    this.listeners.clear();
    this.unlink();
  }

  // subscribe mirrors VS Code's tokens: a listener added after the cancel still
  // runs, on a later tick.
  private subscribe(listener: Listener): Disposable {
    if (this.state.isCancellationRequested) {
      const timer = setTimeout(listener, 0);
      return {
        dispose: () => {
          clearTimeout(timer);
        },
      };
    }
    this.listeners.add(listener);
    return {
      dispose: () => {
        this.listeners.delete(listener);
      },
    };
  }

  private unlink(): void {
    this.link?.dispose();
    this.link = undefined;
  }
}

// isCancellation recognizes a cancelled request as vscode-languageclient
// surfaces it: vscode.CancellationError (named "Canceled") when the server
// cancelled a request the editor had not, or the RequestCancelled response.
export function isCancellation(error: unknown): boolean {
  if (typeof error !== "object" || error === null) {
    return false;
  }
  const { code, name } = error as { code?: unknown; name?: unknown };
  return name === "Canceled" || code === REQUEST_CANCELLED;
}
