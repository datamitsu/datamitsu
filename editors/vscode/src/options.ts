// initializationOptions for `datamitsu lsp`, built from the datamitsu.format.*
// settings. Kept free of the vscode module so it is unit-testable: the extension
// passes its WorkspaceConfiguration, which satisfies SettingsReader.

// The keys under datamitsu.format, which map one-to-one onto the server's
// initializationOptions.format. Values are forwarded as written: the server
// validates them, falls back per key and reports what it rejected.
const FORMAT_KEYS = ["timeoutMs", "tools", "widenTo"] as const;

export interface InitializationOptions {
  format: Partial<Record<(typeof FORMAT_KEYS)[number], unknown>>;
}

export interface SettingInspection {
  globalValue?: unknown;
  workspaceFolderValue?: unknown;
  workspaceValue?: unknown;
}

export interface SettingsReader {
  get: (section: string) => unknown;
  inspect: (section: string) => SettingInspection | undefined;
}

// buildInitializationOptions returns the options for the settings a user set at
// any level, or undefined when none is set. An unset setting is left out rather
// than sent at its default, so the server keeps its DATAMITSU_LSP_FORMAT_*
// environment or its own default. get() supplies the merged value (object
// settings such as tools merge across levels).
export function buildInitializationOptions(
  config: SettingsReader,
): InitializationOptions | undefined {
  const format: InitializationOptions["format"] = {};
  for (const key of FORMAT_KEYS) {
    const section = `format.${key}`;
    if (isExplicitlySet(config.inspect(section))) {
      format[key] = config.get(section);
    }
  }
  return Object.keys(format).length === 0 ? undefined : { format };
}

// describeEffectiveFormat renders the policy the server echoes in
// capabilities.experimental.datamitsu.format, or undefined when it echoes none
// (an older server).
export function describeEffectiveFormat(experimental: unknown): string | undefined {
  const datamitsu = property(experimental, "datamitsu");
  const format = property(datamitsu, "format");
  if (typeof format !== "object" || format === null) {
    return undefined;
  }
  return JSON.stringify(format);
}

export function isExplicitlySet(inspection: SettingInspection | undefined): boolean {
  return (
    inspection !== undefined &&
    (inspection.globalValue !== undefined ||
      inspection.workspaceValue !== undefined ||
      inspection.workspaceFolderValue !== undefined)
  );
}

function property(value: unknown, key: string): unknown {
  if (typeof value !== "object" || value === null) {
    return undefined;
  }
  return (value as Record<string, unknown>)[key];
}
