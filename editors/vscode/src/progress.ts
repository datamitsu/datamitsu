import type { Readable } from "node:stream";

import * as readline from "node:readline";
import * as vscode from "vscode";

import { parseEvent, toStatusUpdate } from "./events";

// JsonlProgress consumes the language server's JSON-L stderr stream and reflects
// long-running work (tool downloads, installs, runs) in a status-bar item. It
// tracks active ops by op_id and shows the most recently updated one; the item is
// hidden when nothing is active. Non-JSON lines (e.g. `--verbose` debug output)
// are ignored.
export class JsonlProgress implements vscode.Disposable {
  private readonly active = new Map<string, string>();
  private readonly item: vscode.StatusBarItem;
  private order: string[] = [];
  private readonly output: vscode.OutputChannel;
  private reader: readline.Interface | undefined;

  constructor(output: vscode.OutputChannel) {
    this.output = output;
    this.item = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 0);
    this.item.name = "datamitsu";
  }

  // attach consumes one server's stderr. A server restart replaces the previous
  // reader (no stacking), starting from a clean state. When the stream ends (the
  // server exited) no terminal events can arrive for in-flight ops, so all active
  // ops are cleared and the spinner is hidden — it never gets stuck.
  attach(stderr: Readable): void {
    this.reader?.close();
    this.clearAll();

    const reader = readline.createInterface({ input: stderr });
    this.reader = reader;
    reader.on("line", (line) => {
      this.onLine(line);
    });
    reader.on("close", () => {
      if (this.reader === reader) {
        this.clearAll();
      }
    });
  }

  // clearAll drops every active op and hides the status item.
  clearAll(): void {
    this.active.clear();
    this.order = [];
    this.render();
  }

  dispose(): void {
    this.reader?.close();
    this.reader = undefined;
    this.item.dispose();
  }

  private clear(opId: string): void {
    if (this.active.delete(opId)) {
      this.order = this.order.filter((id) => id !== opId);
    }
  }

  private onLine(line: string): void {
    const event = parseEvent(line);
    if (event === undefined) {
      return;
    }
    const update = toStatusUpdate(event);

    if (update.error !== undefined) {
      // Logged, not popped up: the failing LSP request already surfaces its own
      // error to the editor, so a second modal would be noise.
      this.output.appendLine(`error: ${update.error}`);
    }

    if (update.label === undefined) {
      this.clear(update.opId);
    } else {
      this.set(update.opId, update.label);
    }
    this.render();
  }

  private render(): void {
    // Show the most recently touched active op, hide when none remain.
    for (let index = this.order.length - 1; index >= 0; index--) {
      const id = this.order[index];
      if (id === undefined) {
        continue;
      }
      const label = this.active.get(id);
      if (label !== undefined) {
        this.item.text = `$(sync~spin) datamitsu: ${label}`;
        this.item.show();
        return;
      }
    }
    this.item.hide();
  }

  private set(opId: string, label: string): void {
    if (!this.active.has(opId)) {
      this.order.push(opId);
    }
    this.active.set(opId, label);
  }
}
