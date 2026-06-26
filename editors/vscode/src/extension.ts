import { type ChildProcess, spawn } from "node:child_process";
import * as vscode from "vscode";
import {
  LanguageClient,
  type LanguageClientOptions,
  type ServerOptions,
  type StreamInfo,
} from "vscode-languageclient/node";

import { type BinaryMode, resolveBinary } from "./binary";
import { JsonlProgress } from "./progress";

let client: LanguageClient | undefined;
let serverProcess: ChildProcess | undefined;
let progress: JsonlProgress | undefined;
let output: undefined | vscode.LogOutputChannel;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  // A LogOutputChannel (vscode-languageclient 10 requires one for outputChannel)
  // also drives the datamitsu.trace.server setting.
  output = vscode.window.createOutputChannel("datamitsu", { log: true });
  progress = new JsonlProgress(output);
  context.subscriptions.push(output, progress);

  context.subscriptions.push(
    vscode.commands.registerCommand("datamitsu.showOutput", () => {
      output?.show();
    }),
    vscode.commands.registerCommand("datamitsu.restartServer", () => restart(context)),
  );

  await start(context);
}

export async function deactivate(): Promise<void> {
  await stop();
}

async function restart(context: vscode.ExtensionContext): Promise<void> {
  await stop();
  await start(context);
}

// showError surfaces an error popup without blocking activation. Attaching a
// rejection handler keeps the thenable non-floating without the `void` operator.
function showError(message: string): void {
  vscode.window.showErrorMessage(message).then(undefined, () => {});
}

function showInfo(message: string): void {
  vscode.window.showInformationMessage(message).then(undefined, () => {});
}

// formatHintShown gates the one-time "no changes" hint so it isn't repeated on
// every format that produces no edits.
let formatHintShown = false;

// spawnServer launches `datamitsu lsp` and returns its stdio as an LSP stream
// pair. We spawn it ourselves (rather than let the client own a ChildProcess) so
// we keep stderr — the JSON-L status stream — for the status bar; stdout/stdin
// carry only LSP traffic. The promise resolves only once the child has actually
// spawned, and REJECTS on a spawn failure (missing/non-executable binary), so
// client.start() surfaces one clear error instead of receiving a dead stream that
// it would then respawn in a loop.
function spawnServer(binaryPath: string): Promise<StreamInfo> {
  const cwd = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  return new Promise<StreamInfo>((resolve, reject) => {
    const child = spawn(binaryPath, ["lsp"], { cwd });
    serverProcess = child;
    let settled = false;

    child.once("error", (err) => {
      output?.appendLine(`server process error: ${err.message}`);
      if (!settled) {
        settled = true;
        reject(err);
      }
    });
    child.on("exit", (code, signal) => {
      output?.appendLine(`datamitsu lsp exited (code ${String(code)}, signal ${String(signal)})`);
      // Drop the handle to the child we own so a later kill() never targets a
      // reaped or replaced pid. The status bar is cleared via the stderr reader's
      // close event in JsonlProgress.
      if (child === serverProcess) {
        serverProcess = undefined;
      }
    });
    child.once("spawn", () => {
      if (settled) {
        return;
      }
      const { stderr, stdin, stdout } = child;
      if (stdout === null || stdin === null) {
        settled = true;
        reject(new Error("datamitsu lsp: stdio pipes are unavailable"));
        return;
      }
      if (stderr !== null) {
        progress?.attach(stderr);
      }
      settled = true;
      resolve({ reader: stdout, writer: stdin });
    });
  });
}

async function start(context: vscode.ExtensionContext): Promise<void> {
  const cfg = vscode.workspace.getConfiguration("datamitsu");

  let binaryPath: string;
  try {
    binaryPath = await resolveBinary({
      explicitPath: cfg.get<string>("path") ?? "",
      log: (message) => output?.appendLine(message),
      mode: cfg.get<BinaryMode>("binaryMode") ?? "auto",
      storageDir: context.globalStorageUri.fsPath,
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    output?.appendLine(`failed to locate datamitsu: ${message}`);
    showError(`datamitsu: ${message}`);
    return;
  }
  output?.appendLine(`using datamitsu: ${binaryPath}`);

  const serverOptions: ServerOptions = () => spawnServer(binaryPath);
  const clientOptions: LanguageClientOptions = {
    // The extension owns the process lifecycle (we returned a StreamInfo, not a
    // ChildProcess), so disable the client's own crash-restart: a dead binary
    // must not be restarted in a loop, and only datamitsu.restartServer re-spawns.
    connectionOptions: { maxRestartCount: 0 },
    documentSelector: [{ scheme: "file" }],
    middleware: {
      // Log every format and its outcome to the output channel so a no-op format
      // is visible (not silent). On the first format that yields no edits, hint
      // once that a stdin->stdout formatter may be missing from the config — a
      // bare 0-edit result is otherwise indistinguishable from "already formatted".
      provideDocumentFormattingEdits: async (document, options, token, next) => {
        output?.appendLine(`format: ${document.uri.fsPath}`);
        const edits = await next(document, options, token);
        const count = edits?.length ?? 0;
        output?.appendLine(`format: ${count} edit(s)`);
        if (count === 0 && !formatHintShown) {
          formatHintShown = true;
          showInfo(
            "datamitsu: no formatting changes — the file is already formatted, or no " +
              "datamitsu fix tool applies to this file type. See the datamitsu output channel.",
          );
        }
        return edits;
      },
    },
  };
  if (output !== undefined) {
    clientOptions.outputChannel = output;
  }
  client = new LanguageClient("datamitsu", "datamitsu", serverOptions, clientOptions);
  try {
    await client.start();
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    output?.appendLine(`language server failed to start: ${message}`);
    showError(`datamitsu: language server failed to start: ${message}`);
    client = undefined;
    serverProcess?.kill();
    serverProcess = undefined;
    return;
  }
  context.subscriptions.push(client);
}

async function stop(): Promise<void> {
  const current = client;
  client = undefined;
  if (current !== undefined) {
    try {
      await current.stop();
    } catch {
      // The graceful shutdown failed; the kill below is the backstop.
    }
  }
  serverProcess?.kill();
  serverProcess = undefined;
}
