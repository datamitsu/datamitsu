import type { Readable } from "node:stream";

import * as readline from "node:readline";
import * as vscode from "vscode";

import {
  alertText,
  isAlertLevel,
  isEmptyFormat,
  type Notice,
  parseEvent,
  toNotice,
  toStatusUpdate,
} from "./events";

const SHOW_OUTPUT = "Show Output";

// JsonlProgress consumes the language server's JSON-L stderr stream and reflects
// long-running work (tool downloads, installs, runs) in a status-bar item. It
// tracks active ops by op_id and shows the most recently updated one; the item is
// hidden when nothing is active. Non-JSON lines are ignored. The server's notices
// and failed tools go to the output channel at their level; the first warning or
// error and the first format that ran no tool of each server session also pop up
// once.
export class JsonlProgress implements vscode.Disposable {
  private readonly active = new Map<string, string>();
  private isAlertShown = false;
  private isEmptyFormatHintShown = false;
  private readonly item: vscode.StatusBarItem;
  private order: string[] = [];
  private readonly output: vscode.LogOutputChannel;
  private reader: readline.Interface | undefined;

  constructor(output: vscode.LogOutputChannel) {
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
    this.isAlertShown = false;
    this.isEmptyFormatHintShown = false;

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

  private notify({ level, message }: Notice): void {
    switch (level) {
      case "debug": {
        this.output.debug(message);
        break;
      }
      case "error": {
        this.output.error(message);
        break;
      }
      case "info": {
        this.output.info(message);
        break;
      }
      case "warn": {
        this.output.warn(message);
        break;
      }
    }
    if (!isAlertLevel(level) || this.isAlertShown) {
      return;
    }
    this.isAlertShown = true;
    const text = alertText(message);
    this.popUp(
      level === "error"
        ? vscode.window.showErrorMessage(text, SHOW_OUTPUT)
        : vscode.window.showWarningMessage(text, SHOW_OUTPUT),
    );
  }

  private onLine(line: string): void {
    const event = parseEvent(line);
    if (event === undefined) {
      return;
    }
    const update = toStatusUpdate(event);

    const notice = toNotice(update);
    if (notice !== undefined) {
      this.notify(notice);
    }
    if (update.log !== undefined) {
      return;
    }

    if (isEmptyFormat(event) && !this.isEmptyFormatHintShown) {
      this.isEmptyFormatHintShown = true;
      this.popUp(
        vscode.window.showInformationMessage(
          "datamitsu: no fix tool ran for this file — none in the config applies to its " +
            "type, or the editor's format policy left them out. See the datamitsu output channel.",
          SHOW_OUTPUT,
        ),
      );
    }

    if (update.label === undefined) {
      this.clear(update.opId);
    } else {
      this.set(update.opId, update.label);
    }
    this.render();
  }

  // popUp opens the output channel when the notification's action is chosen,
  // without blocking the stream reader on the user's answer.
  private popUp(choice: Thenable<string | undefined>): void {
    Promise.resolve(choice)
      .then((picked) => {
        if (picked === SHOW_OUTPUT) {
          this.output.show();
        }
      })
      .catch(() => {});
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
