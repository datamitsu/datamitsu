import { type ChildProcess, spawn } from "node:child_process";
import * as vscode from "vscode";
import {
  LanguageClient,
  type LanguageClientOptions,
  type ServerOptions,
  type StreamInfo,
} from "vscode-languageclient/node";

import { type BinaryMode, resolveBinary } from "./binary";
import { reportFormat } from "./format";
import { InFlight } from "./inflight";
import { buildInitializationOptions, describeEffectiveFormat, describeServedRoot } from "./options";
import { JsonlProgress } from "./progress";

// The server reads these once per session (binary location, initializationOptions),
// so a change to any of them takes a restart to apply.
const RESTART_SECTIONS = ["datamitsu.binaryMode", "datamitsu.format", "datamitsu.path"];

let client: LanguageClient | undefined;
// The running session's formatting requests; replaced on every start.
let formatting: InFlight | undefined;
let serverProcess: ChildProcess | undefined;
let progress: JsonlProgress | undefined;
let output: undefined | vscode.LogOutputChannel;

// Lifecycle transitions run one after another, so two quick setting changes
// cannot interleave a stop with a start and leave two servers running.
let lifecycle: Promise<void> = Promise.resolve();
let queuedRestart: undefined | { isImmediate: boolean };

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
    vscode.commands.registerCommand("datamitsu.restartServer", () => requestRestart(context, true)),
    vscode.workspace.onDidChangeConfiguration((event) => {
      const changed = RESTART_SECTIONS.filter((section) => event.affectsConfiguration(section));
      if (changed.length === 0) {
        return;
      }
      output?.info(`${changed.join(", ")} changed; restarting the language server`);
      requestRestart(context, false).then(undefined, () => {});
    }),
  );

  await serialize(() => start(context));
}

export async function deactivate(): Promise<void> {
  await serialize(stop);
}

// requestRestart queues at most one restart behind the running transition; it
// reads the settings when it starts, so a burst of changes restarts once. A
// restart that is not immediate waits until no format runs: stopping the server
// mid-format would cut its chain of fix tools short. The restart command is
// immediate: it cancels a running format and starts a fresh server even while a
// tool hangs.
function requestRestart(context: vscode.ExtensionContext, isImmediate: boolean): Promise<void> {
  if (queuedRestart !== undefined) {
    queuedRestart.isImmediate ||= isImmediate;
    return lifecycle;
  }
  const request = { isImmediate };
  queuedRestart = request;
  return serialize(async () => {
    queuedRestart = undefined;
    const session = formatting;
    if (session !== undefined && !request.isImmediate && session.size > 0) {
      output?.info("a format is running; the restart waits for it to finish");
      session
        .idle()
        // A different session means another restart already read the settings.
        .then(() => (session === formatting ? requestRestart(context, false) : undefined))
        .then(undefined, () => {});
      return;
    }
    await stop();
    await start(context);
  });
}

function serialize(transition: () => Promise<void>): Promise<void> {
  const run = lifecycle.then(transition);
  lifecycle = run.catch(() => {});
  return run;
}

// showError surfaces an error popup without blocking activation. Attaching a
// rejection handler keeps the thenable non-floating without the `void` operator.
function showError(message: string): void {
  vscode.window.showErrorMessage(message).then(undefined, () => {});
}

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
    let isSettled = false;

    child.once("error", (error) => {
      output?.appendLine(`server process error: ${error.message}`);
      if (!isSettled) {
        isSettled = true;
        reject(error);
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
      if (isSettled) {
        return;
      }
      const { stderr, stdin, stdout } = child;
      if (stdout === null || stdin === null) {
        isSettled = true;
        reject(new Error("datamitsu lsp: stdio pipes are unavailable"));
        return;
      }
      if (stderr !== null) {
        progress?.attach(stderr);
      }
      isSettled = true;
      resolve({ reader: stdout, writer: stdin });
    });
  });
}

async function start(context: vscode.ExtensionContext): Promise<void> {
  const config = vscode.workspace.getConfiguration("datamitsu");

  let binaryPath: string;
  try {
    binaryPath = await resolveBinary({
      explicitPath: config.get<string>("path") ?? "",
      log: (message) => output?.appendLine(message),
      mode: config.get<BinaryMode>("binaryMode") ?? "auto",
      storageDir: context.globalStorageUri.fsPath,
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    output?.appendLine(`failed to locate datamitsu: ${message}`);
    showError(`datamitsu: ${message}`);
    return;
  }
  output?.appendLine(`using datamitsu: ${binaryPath}`);

  const initializationOptions = buildInitializationOptions(config);
  if (initializationOptions !== undefined) {
    output?.appendLine(`initializationOptions: ${JSON.stringify(initializationOptions)}`);
  }

  const serverOptions: ServerOptions = () => spawnServer(binaryPath);
  const session = new InFlight();
  const clientOptions: LanguageClientOptions = {
    // The extension owns the process lifecycle (we returned a StreamInfo, not a
    // ChildProcess), so disable the client's own crash-restart: a dead binary
    // must not be restarted in a loop, and only datamitsu.restartServer re-spawns.
    connectionOptions: { maxRestartCount: 0 },
    documentSelector: [{ scheme: "file" }],
    middleware: {
      // Log every format request to the output channel. The session tracks each
      // request, on a token of its own, so a settings restart can wait for it and
      // a stop can cancel it.
      provideDocumentFormattingEdits: (document, options, token, next) =>
        session.track(token, (linked) =>
          reportFormat(
            document.uri.fsPath,
            linked,
            (line) => output?.appendLine(line),
            () => next(document, options, linked),
          ),
        ),
    },
  };
  if (initializationOptions !== undefined) {
    clientOptions.initializationOptions = initializationOptions;
  }
  if (output !== undefined) {
    clientOptions.outputChannel = output;
  }
  client = new LanguageClient("datamitsu", "datamitsu", serverOptions, clientOptions);
  formatting = session;
  try {
    await client.start();
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    output?.appendLine(`language server failed to start: ${message}`);
    showError(`datamitsu: language server failed to start: ${message}`);
    client = undefined;
    formatting = undefined;
    session.close();
    serverProcess?.kill();
    serverProcess = undefined;
    return;
  }
  context.subscriptions.push(client);

  const experimental: unknown = client.initializeResult?.capabilities.experimental;
  const root = describeServedRoot(experimental);
  if (root !== undefined) {
    output?.info(`served root: ${root}`);
  }
  const effective = describeEffectiveFormat(experimental);
  if (effective !== undefined) {
    output?.info(`effective format policy: ${effective}`);
  }
}

// stop cancels the running formats before the shutdown request, so the server
// stops them at their next checkpoint and answers the shutdown in time.
async function stop(): Promise<void> {
  const current = client;
  client = undefined;
  formatting?.close();
  formatting = undefined;
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
