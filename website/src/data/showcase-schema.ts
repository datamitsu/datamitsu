/* eslint-disable new-cap -- TypeBox names its schema builders like constructors. */
import { type Static, Type } from "typebox";

/**
 * The shape of `showcases.json`, the curated half of the showcase. It is the single source for the
 * published JSON Schema (`static/schemas/showcase.schema.json`), for the pull-request check, and
 * for the page's types — a listing cannot be accepted in a shape the page cannot render.
 */

export const schemaID = "https://datamitsu.com/schemas/showcase.schema.json";

/**
 * The stacks a configuration covers. A closed list, so `ts` and `TypeScript` cannot both appear.
 */
export const tagVocabulary = [
  "docker",
  "github-actions",
  "go",
  "helm",
  "java",
  "kubernetes",
  "node",
  "python",
  "ruby",
  "rust",
  "shell",
  "sql",
  "terraform",
  "typescript",
  "typst",
] as const;

const sha256 = Type.String({
  description: "SHA-256 of the file, as 64 hexadecimal characters.",
  pattern: "^[0-9a-f]{64}$",
});

const registryKind = Type.Union(["npm", "pypi", "gem"].map((kind) => Type.Literal(kind)));
const httpsURL = Type.String({ format: "uri", pattern: "^https://" });
const optionalURL = Type.Optional(httpsURL);
const tagName = Type.Union(tagVocabulary.map((tag) => Type.Literal(tag)));

const consumeEntry = Type.Union([
  Type.Object(
    {
      kind: registryKind,
      package: Type.String({ minLength: 1 }),
    },
    { additionalProperties: false },
  ),
  Type.Object(
    {
      // A reader executes what this points at, so the hash is not optional.
      hash: sha256,
      kind: Type.Literal("remote"),
      url: httpsURL,
    },
    { additionalProperties: false },
  ),
  Type.Object(
    { kind: Type.Literal("oci"), ref: Type.String({ minLength: 1 }) },
    { additionalProperties: false },
  ),
]);

export const showcaseEntrySchema = Type.Object(
  {
    added: Type.String({ pattern: "^\\d{4}-\\d{2}-\\d{2}$" }),
    author: Type.Object(
      {
        github: Type.String({ pattern: "^[A-Za-z0-9](?:[A-Za-z0-9]|-(?=[A-Za-z0-9])){0,38}$" }),
        name: Type.String({ minLength: 1 }),
      },
      { additionalProperties: false },
    ),
    consume: Type.Array(consumeEntry, { minItems: 1 }),
    // A plain URL, deliberately not pinned: it is display data, refetched weekly,
    // and a hash here would make every refresh a mismatch. See the guide.
    dataset: optionalURL,
    description: Type.String({ maxLength: 200, minLength: 1 }),
    id: Type.String({ pattern: "^[a-z0-9][a-z0-9-]{0,63}$" }),
    links: Type.Object(
      {
        inspector: optionalURL,
        repository: httpsURL,
        site: optionalURL,
      },
      { additionalProperties: false },
    ),
    name: Type.String({ minLength: 1 }),
    reference: Type.Optional(Type.Boolean()),
    tags: Type.Array(tagName, { minItems: 1 }),
  },
  { additionalProperties: false },
);

export const showcaseFileSchema = Type.Object(
  {
    $schema: Type.Optional(Type.String()),
    configs: Type.Array(showcaseEntrySchema),
  },
  {
    $id: schemaID,
    additionalProperties: false,
    description:
      "Configurations listed on the datamitsu showcase. Each entry is maintained by its author; the derived half lives in showcases.generated.json.",
    title: "datamitsu showcase",
  },
);

/**
 * What the refresh job derives. Every field is optional: a fetch that failed leaves it out.
 */
export interface ShowcaseDerived {
  composition?: { apps: number; runtimes: Record<string, number> };
  dataset?: { capturedAt?: string; etag?: string; sha256: string };
  errors: string[];
  fetchedAt: string;
  repository?: { etag?: string; lastCommit?: string; lastRelease?: string; stars?: number };
}
export type ShowcaseEntry = Static<typeof showcaseEntrySchema>;

export type ShowcaseFile = Static<typeof showcaseFileSchema>;
