declare global {
  /**
   * Color functions for terminal output. All functions accept any number of arguments,
   * join them with spaces, and return a string with ANSI color codes applied.
   * Respects NO_COLOR, FORCE_COLOR, and CLICOLOR environment variables.
   * Colors are composable: `colors.bold(colors.red("error"))`.
   */
  const colors: {
    // Background colors
    bgBlack(...args: unknown[]): string;
    bgBlue(...args: unknown[]): string;
    bgCyan(...args: unknown[]): string;
    bgGreen(...args: unknown[]): string;
    // Bright background colors
    bgHiBlack(...args: unknown[]): string;
    bgHiBlue(...args: unknown[]): string;
    bgHiCyan(...args: unknown[]): string;
    bgHiGreen(...args: unknown[]): string;

    bgHiMagenta(...args: unknown[]): string;
    bgHiRed(...args: unknown[]): string;
    bgHiWhite(...args: unknown[]): string;
    bgHiYellow(...args: unknown[]): string;
    bgMagenta(...args: unknown[]): string;
    bgRed(...args: unknown[]): string;
    bgWhite(...args: unknown[]): string;
    bgYellow(...args: unknown[]): string;

    // Foreground colors
    black(...args: unknown[]): string;
    blink(...args: unknown[]): string;
    blinkRapid(...args: unknown[]): string;
    blue(...args: unknown[]): string;
    // Text attributes
    bold(...args: unknown[]): string;
    concealed(...args: unknown[]): string;
    cyan(...args: unknown[]): string;
    faint(...args: unknown[]): string;
    green(...args: unknown[]): string;

    // Bright foreground colors
    hiBlack(...args: unknown[]): string;
    hiBlue(...args: unknown[]): string;
    hiCyan(...args: unknown[]): string;
    hiGreen(...args: unknown[]): string;
    hiMagenta(...args: unknown[]): string;
    hiRed(...args: unknown[]): string;
    hiWhite(...args: unknown[]): string;
    hiYellow(...args: unknown[]): string;

    italic(...args: unknown[]): string;
    magenta(...args: unknown[]): string;
    red(...args: unknown[]): string;
    reverse(...args: unknown[]): string;
    strikethrough(...args: unknown[]): string;
    underline(...args: unknown[]): string;
    white(...args: unknown[]): string;
    yellow(...args: unknown[]): string;
  };

  function getConfig(config: config.Config): config.Config;

  /**
   * Returns the minimum datamitsu version required by this config (semver format).
   * The tool validates this version during config loading and fails early with
   * upgrade instructions if the current version is too old.
   * Every config must export this function.
   * @example
   * function getMinVersion() {
   *   return "1.2.0";
   * }
   */
  function getMinVersion(): string;

  /**
   * Optional export. Declares remote parent configs to load before this config.
   * Remote configs are resolved depth-first and their results are chained as input.
   * Hash is REQUIRED for every entry — missing or empty hash causes an immediate error.
   * @example
   * function getRemoteConfigs() {
   *   return [{ url: "https://example.com/base-config.ts", hash: "sha256:abcdef..." }];
   * }
   */
  function getRemoteConfigs(): Array<{ hash: string; url: string }>;

  namespace SysList {
    type ArchType = "aarch64" | "amd64" | "arm64";

    type OsType = "darwin" | "freebsd" | "linux" | "openbsd" | "windows";
  }

  /**
   * Tools namespace with utility functions
   */
  namespace tools {
    /**
     * Path utilities for working with file paths
     */
    namespace Path {
      /**
       * Get absolute path from relative path
       * @param path - Path to convert to absolute
       * @returns Absolute path
       * @throws Error if path cannot be resolved
       * @example
       * const absPath = tools.Path.abs("./file.txt");
       * // Returns: "/current/working/directory/file.txt"
       */
      function abs(path: string): string;

      /**
       * Convert a relative path to an ES module import-compatible format.
       * Ensures the path starts with `./` or `../` as required by JavaScript/TypeScript imports.
       * The path is cleaned/normalized before processing.
       * Idempotent: paths already starting with `./` or `../` are returned unchanged.
       *
       * @param path - Relative path to convert
       * @returns Import-compatible relative path (always starts with `./` or `../`)
       * @throws TypeError if path is absolute (imports must be relative)
       * @throws TypeError if no argument is provided
       *
       * @example
       * // Basic usage with datamitsuDir context
       * const importPath = tools.Path.forImport(
       *   tools.Path.join(context.datamitsuDir, "eslint.config.js")
       * );
       * // ".datamitsu/eslint.config.js" → "./.datamitsu/eslint.config.js"
       *
       * @example
       * // Idempotent — already-valid paths are unchanged
       * tools.Path.forImport("./.datamitsu/file.js");   // "./.datamitsu/file.js"
       * tools.Path.forImport("../.datamitsu/file.js");  // "../.datamitsu/file.js"
       *
       * @example
       * // Composing with linkPath for managed config imports
       * const configPath = tools.Config.linkPath("my-app", "eslint-config", context.cwdPath);
       * const importPath = tools.Path.forImport(configPath);
       * // Use in generated config: `import config from "${importPath}";`
       */
      function forImport(path: string): string;

      /**
       * Join path segments together using the OS-specific separator
       * @param paths - Path segments to join
       * @returns Joined path
       * @example
       * const fullPath = tools.Path.join("/home", "user", "file.txt");
       * // Returns: "/home/user/file.txt" on Unix, "\\home\\user\\file.txt" on Windows
       */
      function join(...paths: string[]): string;

      /**
       * Get relative path from base to target
       * @param targetPath - Target path
       * @param basePath - Base path (defaults to git repository root, or cwd if not in a git repo)
       * @returns Relative path from base to target
       * @throws Error if relative path cannot be computed
       * @example
       * // Relative to rootPath (git root)
       * const relPath = tools.Path.rel("/home/user/project/file.txt");
       * // If rootPath is "/home/user/project", returns: "file.txt"
       *
       * @example
       * // Relative to custom base
       * const relPath = tools.Path.rel("/home/user/project/file.txt", "/home");
       * // Returns: "user/project/file.txt"
       */
      function rel(targetPath: string, basePath?: string): string;
    }

    /**
     * .gitignore parser and stringifier
     */
    namespace Ignore {
      type IgnoreMap<T extends string = string> = Partial<Record<T, string[]>>;

      interface ParseResult<T extends string = string> {
        groupOrder: T[];
        groups: IgnoreMap<T>;
      }

      /**
       * Parse .gitignore file to grouped structure with preserved order
       * @param content - .gitignore file content
       * @returns Object with groups and their original order
       * @example
       * const { groups, groupOrder } = tools.Ignore.parse(fileContent);
       * // groups: { "Dependencies": ["node_modules/"], "Build": ["dist/"] }
       * // groupOrder: ["Dependencies", "Build"]
       *
       * // Use the original order when saving back
       * const updated = tools.Ignore.stringify(groups, groupOrder);
       */
      function parse<T extends string = string>(content: string): ParseResult<T>;

      /**
       * Convert grouped structure back to .gitignore format
       * @param groups - Object with group names as keys and rule arrays as values
       * @param groupOrder - Optional partial array specifying the priority order of groups.
       *                     Groups in this array will appear first in the specified order.
       *                     Remaining groups will follow in alphabetical order.
       * @returns Formatted .gitignore content
       * @example
       * // All groups in alphabetical order
       * const content1 = tools.Ignore.stringify({
       *   "Testing": ["coverage/"],
       *   "Build": ["dist/"],
       *   "Dependencies": ["node_modules/"]
       * });
       * // Result order: Build, Dependencies, Testing
       *
       * @example
       * // Priority groups first, then alphabetical
       * const content2 = tools.Ignore.stringify(
       *   {
       *     "Testing": ["coverage/"],
       *     "Build": ["dist/"],
       *     "Dependencies": ["node_modules/"],
       *     "IDE": [".vscode/"]
       *   },
       *   ["Dependencies", "Build"]
       * );
       * // Result order: Dependencies, Build, IDE, Testing
       *
       * @example
       * // Preserve original order
       * const { groups, groupOrder } = tools.Ignore.parse(original);
       * groups["New Group"] = ["new/rule"];
       * const updated = tools.Ignore.stringify(groups, groupOrder);
       */
      function stringify<T extends string>(groups: IgnoreMap<T>, groupOrder?: T[]): string;
    }

    /**
     * Config link utilities for referencing managed config files in .datamitsu/
     */
    namespace Config {
      /**
       * Get relative path from a directory to a managed config link in .datamitsu/
       * @param ownerName - The app or bundle that owns the link
       * @param linkName - The link name in .datamitsu/
       * @param fromPath - The directory to compute the relative path from
       * @returns Relative path from fromPath to .datamitsu/linkName
       * @throws TypeError if linkName is not found or doesn't belong to ownerName
       */
      function linkPath(ownerName: string, linkName: string, fromPath: string): string;
    }
  }

  namespace config {
    // ========================================
    // Project Type Detection
    // ========================================

    interface Config {
      /**
       * Binary/app definitions
       */
      apps?: BinManager.MapOfApps;

      /**
       * Bundle definitions for managed content (files/archives with symlinks).
       * Bundles are not executable — they store files in a hash-keyed directory
       * and create symlinks in .datamitsu/ for zero-conflict content updates.
       */
      bundles?: BinManager.MapOfBundles;

      /**
       * Ignore rules in .datamitsuignore syntax.
       * Applied alongside file-based .datamitsuignore rules.
       * Rules from multiple configs are concatenated (append).
       * @example ["**\/*.generated.ts: eslint, prettier", "vendor/**: *"]
       */
      ignoreRules?: string[];

      /**
       * Config file initialization
       */
      init?: MapOfConfigInit;

      /**
       * Init commands to run after setup
       */
      initCommands?: MapOfInitCommands;

      /**
       * Project type definitions
       */
      projectTypes?: MapOfProjectTypes;

      /**
       * Runtime definitions for managed package managers (UV, PNPM)
       */
      runtimes?: BinManager.MapOfRuntimes;

      /**
       * Arbitrary key-value storage that flows through the config chain.
       * Any config layer can read/write values via input.sharedStorage.
       * Useful for passing data between config layers that doesn't fit
       * the typed config structure.
       * @example
       * return { ...input, sharedStorage: { ...input.sharedStorage, "my-key": "my-value" } };
       */
      sharedStorage?: Record<string, string>;

      /**
       * Tool definitions
       */
      tools?: MapOfTools;
    }

    /**
     * Context passed to config content generator
     */
    interface ConfigContext {
      /**
       * Current working directory
       */
      cwdPath: string;

      /**
       * Relative path from cwdPath to the .datamitsu/ directory at git root.
       * Simplifies referencing managed config links in content generators.
       * @example "../../.datamitsu" // from packages/frontend/
       * @example ".datamitsu" // from git root
       */
      datamitsuDir: string;

      /**
       * Content of existing file (if it exists)
       * This may be modified content from previous merge operations
       */
      existingContent?: string;

      /**
       * Path to existing file (if it exists)
       */
      existingPath?: string;

      /**
       * Whether the current working directory is the git root
       */
      isRoot: boolean;

      /**
       * Original content of the file as it exists on disk
       * This is always the unmodified content, even when existingContent has been transformed
       */
      originalContent?: string;

      /**
       * Detected project types
       */
      projectTypes: string[];

      /**
       * Git root path
       */
      rootPath: string;
    }

    // ========================================
    // Tool Execution Configuration
    // ========================================

    /**
     * Configuration file initialization (enhanced from existing)
     */
    interface ConfigInit {
      /**
       * Function that generates file content
       * Receives context about the project including existing file content if present
       * Optional when deleteOnly is true or linkTarget is set
       */
      content?: (context: ConfigContext) => string;

      /**
       * If true, only delete files from otherFileNameList without creating any new file
       * Content function is ignored when deleteOnly is true
       * @default false
       */
      deleteOnly?: boolean;

      /**
       * Relative path to the symlink target, resolved from the directory of the symlink itself.
       * When set, creates a symlink instead of writing file content.
       * Content function is ignored when linkTarget is set.
       * @example "AGENTS.md" // symlink in same directory
       * @example "../AGENTS.md" // symlink to parent directory
       */
      linkTarget?: string;

      /**
       * All other known filenames for this config
       * These will be DELETED during install to avoid conflicts
       * @example [".eslintrc.js", ".eslintrc.json", ".eslintrc.yml", "eslint.config.js"]
       */
      otherFileNameList?: string[];

      /**
       * Which project type this config applies to
       * If not specified, applies to all projects
       */
      projectTypes?: string[];

      /**
       * Controls where the config file is created.
       * - "project" (default): creates in the current project directory
       * - "git-root": creates only at the git repository root (runs once)
       */
      scope?: "git-root" | "project";
    }

    /**
     * Initialization command to run after clone/install
     */
    interface InitCommand {
      /**
       * Arguments
       */
      args: string[];

      /**
       * Command to execute (app name from MapOfApps)
       */
      command: string;

      /**
       * Description
       */
      description?: string;

      /**
       * Which project types this applies to
       * Empty = all project types
       */
      projectTypes?: string[];

      /**
       * Only run if this file/directory exists
       */
      when?: string;
    }

    /**
     * Map of configuration init with mainFilename as key
     * @example
     * {
     *   ".gitignore": { content: () => "..." },
     *   "lefthook.yaml": { content: () => "..." },
     *   ".vscode/settings.json": { content: () => "..." }
     * }
     */
    type MapOfConfigInit = Record<string, ConfigInit>;

    // ========================================
    // Init Commands
    // ========================================

    type MapOfInitCommands = Record<string, InitCommand>;

    type MapOfProjectTypes = Record<string, ProjectType>;

    // ========================================
    // Config File Management (ENHANCED)
    // ========================================

    type MapOfTools = Record<string, Tool>;

    /**
     * Operation type
     */
    type OperationType = "fix" | "lint";

    /**
     * Project type detector - defines when this project type is detected
     */
    interface ProjectType {
      /**
       * Human-readable description
       */
      description?: string;

      /**
       * Marker files that indicate this project type
       * If ANY of these files exist in the root, project type is detected
       * Supports glob patterns in repo root
       * @example ["package.json", "yarn.lock"]
       * @example ["go.mod"]
       * @example ["*.tf"]
       */
      markers: string[];
    }

    // ========================================
    // Git Hook Configuration
    // ========================================

    /**
     * Tool definition - what operations a tool supports
     */
    interface Tool {
      /**
       * Tool name/description
       */
      name: string;

      /**
       * Operations this tool supports
       */
      operations: Partial<Record<OperationType, ToolOperation>>;

      /**
       * Which project types this tool applies to
       * Empty array or undefined = applies to all project types
       * @example ["npm-package", "typescript-project"]
       */
      projectTypes?: string[];
    }

    /**
     * Tool operation configuration - how to run a tool for a specific operation
     */
    interface ToolOperation {
      /**
       * App to execute (app name from MapOfApps)
       */
      app: string;

      /**
       * Arguments to pass to the command.
       * Template placeholders are expanded by the executor before execution:
       * - {file} - single file path (per-file scope)
       * - {files} - space-separated file list (batch mode)
       * - {root} - git repository root (or cwd if not in a git repo)
       * - {cwd} - per-project working directory
       * - {toolCache} - per-project, per-tool cache directory (cache/{projectPath}/{toolName}/)
       * @example ["--fix", "{file}"]
       * @example ["--noEmit", "--project", "{root}"]
       * @example ["--cache-location", "{toolCache}/eslint"]
       */
      args: string[];

      /**
       * Batch mode - process files in groups or one at a time
       * Only used for scope: "per-project" or "repository"
       * @default true for "per-project" and "repository", false for "per-file"
       */
      batch?: boolean;

      /**
       * Extra environment variables for this operation
       * Merge priority: OS env < app env < tool operation env
       * @example { "NODE_ENV": "production", "ESLINT_USE_FLAT_CONFIG": "true" }
       */
      env?: Record<string, string>;

      /**
       * File glob patterns this tool operates on
       * Uses gitignore-style patterns
       * @example ["**\/*.ts", "**\/*.tsx"]
       * @example ["**\/*.md"]
       */
      globs: string[];

      /**
       * Files that should invalidate the cache when changed
       * Paths are relative to project root
       * @example ["eslint.config.js", "tsconfig.json"]
       * @example [".prettierrc", "package.json"]
       */
      invalidateOn?: string[];

      /**
       * Priority/order when tools have overlapping globs
       * Lower number = runs first
       * Tools with same priority and overlapping globs run sequentially in definition order
       * Tools with different globs or no overlap run in parallel
       * @default 0
       */
      priority?: number;

      /**
       * Scope defines the execution area and working directory
       * - "repository": run once from git root for the entire repository
       * - "per-project": run for each detected project in its directory
       * - "per-file": run for each file in its directory
       * @default "per-project"
       * @example
       * // ESLint - runs in each project
       * {
       *   scope: "per-project",
       *   app: "eslint",
       *   args: ["--quiet", "{files}"],
       *   globs: ["**\/*.ts", "**\/*.js"]
       * }
       * @example
       * // syncpack - runs once for the entire monorepo
       * {
       *   scope: "repository",
       *   app: "syncpack",
       *   args: ["lint"],
       *   globs: ["**\/package.json"]
       * }
       * @example
       * // shfmt - formats each shell file separately
       * {
       *   scope: "per-file",
       *   app: "shfmt",
       *   args: ["-w", "{file}"],
       *   globs: ["**\/*.sh"]
       * }
       */
      scope: ToolScope;
    }

    /**
     * Scope defines the execution area and automatically determines the working directory
     */
    type ToolScope =
      | "per-file" // Run for each file in its directory
      | "per-project" // Run for each detected project in its directory
      | "repository"; // Run once from git root for the entire repository

    // ========================================
    // Main Config (ENHANCED)
    // ========================================
  }

  namespace BinManager {
    interface App {
      /**
       * Named archives to extract into the app's install directory.
       * Archive names can be referenced in Links to create symlinks.
       * Archives are extracted before Files are written, allowing Files to override.
       */
      archives?: Record<string, ArchiveSpec>;
      binary?: AppConfigBinary;
      /**
       * Human-readable description of the app, shown in exec listing.
       */
      description?: string;
      files?: Record<string, string>;
      fnm?: AppConfigFNM;
      jvm?: AppConfigJVM;
      /**
       * Symlinks to create in .datamitsu/ directory, mapping link name to relative path in install directory.
       */
      links?: Record<string, string>;
      required?: boolean;
      shell?: AppConfigShell;
      uv?: AppConfigUV;
      /**
       * Version check configuration for verify-all command.
       * Absent: use default ["--version"] args.
       * { disabled: true }: skip version check.
       * { args: ["version"] }: use custom args.
       */
      versionCheck?: AppVersionCheck;
    }

    interface AppConfigBinary {
      binaries: MapOfBinaries;
      /**
       * Version string for display purposes (e.g. from GitHub release tag).
       */
      version?: string;
    }

    interface AppConfigFNM {
      binPath: string;
      dependencies?: Record<string, string>;
      /**
       * Lock file content for reproducible installs.
       * Required for all FNM apps. Validation fails if omitted.
       * When prefixed with "br:", the content is brotli-compressed and base64-encoded.
       * Plain text is also accepted for backward compatibility.
       * Generate via: datamitsu config lockfile <appName>
       */
      lockFile: string;
      packageName: string;
      runtime?: string;
      version: string;
    }

    interface AppConfigJVM {
      jarHash: string;
      jarUrl: string;
      /**
       * Optional main class for JARs that aren't executable.
       */
      mainClass?: string;
      runtime?: string;
      version: string;
    }

    interface AppConfigShell {
      args?: string[];
      env?: Record<string, string>;
      name: string;
    }

    interface AppConfigUV {
      /**
       * Lock file content for reproducible installs.
       * Required for all UV apps. Validation fails if omitted.
       * When prefixed with "br:", the content is brotli-compressed and base64-encoded.
       * Plain text is also accepted for backward compatibility.
       * Generate via: datamitsu config lockfile <appName>
       */
      lockFile: string;
      packageName: string;
      /**
       * Python version constraint for pyproject.toml requires-python field.
       * If not set, defaults to ">=3.12".
       * @example ">=3.10"
       */
      requiresPython?: string;
      runtime?: string;
      version: string;
    }

    interface AppVersionCheck {
      /**
       * Custom arguments for version check. Defaults to ["--version"] if absent.
       */
      args?: string[];
      /**
       * Skip version check for this app.
       */
      disabled?: boolean;
    }

    /**
     * Archive specification for bundling directory trees with apps.
     * Supports inline (brotli-compressed tar) and external (URL) formats.
     * Archives are extracted before Files are written, allowing Files to override.
     */
    interface ArchiveSpec {
      /** Archive format (tar-based only) */
      format?: "tar" | "tar.bz2" | "tar.gz" | "tar.xz" | "tar.zst";

      /**
       * SHA-256 hash (64 lowercase hex characters).
       * Required for external archives per security policy.
       */
      hash?: string;

      /**
       * Inline archive: brotli-compressed tar + base64 with "tar.br:" prefix.
       * Maximum decompressed size: 50 MiB.
       * @example "tar.br:GxsAACBdU6xBxGN0YXIg..."
       */
      inline?: string;

      /**
       * External archive URL (requires hash and format).
       * Downloaded and SHA-256 verified before extraction.
       */
      url?: string;
    }

    interface BinaryOsArchInfo {
      binaryPath?: string;
      contentType: BinContentType;
      /**
       * When true, extracts the entire archive to a directory instead of a single binary.
       * Used for runtimes like JDK that need the full directory tree (bin/, lib/, etc.).
       */
      extractDir?: boolean;

      hash: string;
      /** @default sha256 */
      hashType?: BinHashType;
      url: string;
    }

    type BinContentType =
      | "binary"
      | "bz2"
      | "gz"
      | "tar"
      | "tar.bz2"
      | "tar.gz"
      | "tar.xz"
      | "tar.zst"
      | "xz"
      | "zip"
      | "zst";

    type BinHashType = "md5" | "sha1" | "sha256" | "sha384" | "sha512";

    interface Bundle {
      /**
       * Named archives to extract into the bundle's install directory.
       * Supports inline (brotli-compressed tar) and external (URL with hash) formats.
       */
      archives?: Record<string, ArchiveSpec>;

      /**
       * Static file contents to write into the bundle's install directory.
       */
      files?: Record<string, string>;

      /**
       * Symlinks to create in .datamitsu/ directory, mapping link name to relative path in install directory.
       * Values can point to files or directories within the bundle.
       * Use "." to link the entire bundle directory.
       */
      links?: Record<string, string>;

      /**
       * Version string for cache invalidation. Changing this produces a new hash directory.
       */
      version?: string;
    }

    type LibcType = "glibc" | "musl" | "unknown";

    type MapOfApps = Record<string, App>;

    type MapOfBinaries = Partial<
      Record<
        SysList.OsType,
        Partial<Record<SysList.ArchType, Partial<Record<LibcType, BinaryOsArchInfo>>>>
      >
    >;

    type MapOfBundles = Record<string, Bundle>;

    type MapOfRuntimes = Record<string, RuntimeConfig>;

    interface RuntimeConfig {
      /**
       * FNM-specific runtime configuration (nodeVersion, pnpmVersion).
       * Required when kind is "fnm".
       */
      fnm?: RuntimeConfigFNM;
      /**
       * JVM-specific runtime configuration (javaVersion).
       * Required when kind is "jvm".
       */
      jvm?: RuntimeConfigJVM;
      kind: RuntimeKind;
      managed?: RuntimeConfigManaged;
      mode: RuntimeMode;
      system?: RuntimeConfigSystem;
      /**
       * UV-specific runtime configuration (pythonVersion).
       * Optional when kind is "uv".
       */
      uv?: RuntimeConfigUV;
    }

    interface RuntimeConfigFNM {
      nodeVersion: string;
      /**
       * SHA-256 hash of the PNPM tarball for integrity verification.
       * Required per security policy: all downloads must have a pinned hash.
       */
      pnpmHash: string;
      pnpmVersion: string;
    }

    interface RuntimeConfigJVM {
      javaVersion: string;
    }

    interface RuntimeConfigManaged {
      binaries: MapOfBinaries;
    }

    interface RuntimeConfigSystem {
      command: string;
      /**
       * Optional version string for manual cache invalidation when system runtime version changes.
       */
      systemVersion?: string;
    }

    interface RuntimeConfigUV {
      pythonVersion?: string;
    }

    type RuntimeKind = "fnm" | "jvm" | "uv";

    type RuntimeMode = "managed" | "system";
  }

  /**
   * Facts about the project environment.
   * Collected automatically on engine initialization.
   *
   * Path-related fields have been removed. Use template placeholders in tool
   * operation args instead:
   * - `{cwd}` - current working directory
   * - `{root}` - git repository root (or cwd if not in git)
   * - `{toolCache}` - per-project, per-tool cache directory (cache/{projectPath}/{toolName}/)
   */
  interface Facts {
    /**
     * CPU architecture (amd64, arm64, aarch64)
     */
    arch: SysList.ArchType;

    /**
     * Command to run this binary (can be overridden via --binary-command flag or DATAMITSU_BINARY_COMMAND env var)
     * Useful for npm package wrappers that need to call the actual binary
     */
    binaryCommand: string;

    /**
     * Absolute path to the currently running binary
     */
    binaryPath: string;

    /**
     * Environment variables with the package prefix (e.g., CHANGE_ME_*)
     * Only includes variables that start with the prefix defined in ldflags.EnvPrefix
     * @example { "CHANGE_ME_DEBUG": "true", "CHANGE_ME_LOG_LEVEL": "info" }
     */
    env: Record<string, string>;

    /**
     * Whether we're inside a git repository
     */
    isInGitRepo: boolean;

    /**
     * Whether we're in a subdirectory of git root (potential monorepo)
     */
    isMonorepo: boolean;

    /**
     * Libc implementation on the host system.
     * "glibc" or "musl" on Linux, "unknown" on non-Linux systems.
     */
    libc: "glibc" | "musl" | "unknown";

    /**
     * Operating system (darwin, linux, windows, freebsd, openbsd)
     */
    os: SysList.OsType;

    /**
     * Package name from ldflags
     */
    packageName: string;
  }

  /**
   * Function that returns Facts object
   * Facts are collected once during engine initialization
   */
  const facts: () => Facts;

  /**
   * YAML parser and stringifier
   */
  namespace YAML {
    /**
     * Parse YAML string to JavaScript object
     * @param text - YAML string to parse
     * @returns Parsed object
     * @throws Error if YAML is invalid
     */
    function parse(text: string): any;

    /**
     * Convert JavaScript object to YAML string
     * @param value - Object to stringify
     * @returns YAML string
     * @throws Error if object cannot be serialized
     */
    function stringify(value: any): string;
  }

  /**
   * TOML parser and stringifier
   */
  namespace TOML {
    /**
     * Parse TOML string to JavaScript object
     * @param text - TOML string to parse
     * @returns Parsed object
     * @throws Error if TOML is invalid
     */
    function parse(text: string): any;

    /**
     * Convert JavaScript object to TOML string
     * @param value - Object to stringify
     * @returns TOML string
     * @throws Error if object cannot be serialized
     */
    function stringify(value: any): string;
  }

  /**
   * INI parser and stringifier
   */
  namespace INI {
    /**
     * INI section structure
     */
    type Section = Record<string, string>;

    /**
     * INI section entry with name and properties
     */
    interface SectionEntry {
      name: string;
      properties: Section;
    }

    /**
     * Parse INI string to array of sections
     * @param text - INI string to parse
     * @returns Array of sections (preserves order and allows duplicate section names)
     * @throws Error if INI is invalid
     * @example
     * const ini = INI.parse(`
     * [database]
     * host = localhost
     * port = 5432
     * [*.py]
     * indent_size = 4
     * [*.py]
     * indent_size = 2
     * `);
     * // Returns: [
     * //   { name: "database", properties: { host: "localhost", port: "5432" } },
     * //   { name: "*.py", properties: { indent_size: "4" } },
     * //   { name: "*.py", properties: { indent_size: "2" } }
     * // ]
     */
    function parse(text: string): Array<SectionEntry>;

    /**
     * Convert array of sections to INI string
     * @param sections - Array of section entries
     * @returns INI string
     * @throws Error if object cannot be serialized
     * @example
     * const sections = [
     *   { name: "database", properties: { host: "localhost", port: "5432" } },
     *   { name: "*.py", properties: { indent_size: "2" } }
     * ];
     * console.log(INI.stringify(sections));
     */
    function stringify(sections: Array<SectionEntry>): string;

    /**
     * Convert array of sections to a record, merging sections with the same name
     * @param sections - Array of section entries from INI.parse
     * @returns Record mapping section names to their merged properties
     * @example
     * const sections = INI.parse(`
     * [*.py]
     * indent_size = 4
     * [*.py]
     * indent_size = 2
     * `);
     * const record = INI.toRecord(sections);
     * // Returns: { "*.py": { indent_size: "2" } }
     * // Later values override earlier ones for the same section name
     */
    function toRecord(sections: Array<SectionEntry>): Record<string, Section>;
  }
}

export {};
