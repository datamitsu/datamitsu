import { useState } from "react";

import Link from "@docusaurus/Link";
import Layout from "@theme/Layout";

import type { ShowcaseDerived, ShowcaseEntry } from "../data/showcase-schema";

import { runtimeNames } from "../../../inspector/src/model";
import { tagVocabulary } from "../data/showcase-schema";
import derived from "../data/showcases.generated.json";
import curated from "../data/showcases.json";
import styles from "./showcase.module.css";

const editURL = "https://github.com/datamitsu/datamitsu/edit/main/website/src/data/showcases.json";
// Searching, sorting and tag chips are all answers to a long list; below that
// they are furniture around a page a reader can take in at once. Stars are
// collected for the refresh job's own checks and are deliberately neither shown
// nor sorted on: this is a directory of configurations, not a ranking.
const listControlsThreshold = 6;
const staleAfterDays = 365;

const sorts = {
  name: {
    compare: (a: ShowcaseEntry, b: ShowcaseEntry) => a.name.localeCompare(b.name),
    label: "Name",
  },
  updated: {
    compare: (a: ShowcaseEntry, b: ShowcaseEntry) => released(b.id).localeCompare(released(a.id)),
    label: "Recently released",
  },
};

// What a reader is looking at: an npm package and a remote config by URL are
// not interchangeable, and an unlabelled pair of snippets does not say which is
// which.
const consumeLabels: Record<ShowcaseEntry["consume"][number]["kind"], string> = {
  gem: "RubyGems package",
  npm: "npm package",
  oci: "OCI image",
  pypi: "PyPI package",
  remote: "Remote config, by URL and hash",
};

const entries = curated.configs as ShowcaseEntry[];

export default function Showcase() {
  const [tag, setTag] = useState("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<keyof typeof sorts>("updated");
  const needle = query.trim().toLowerCase();
  const shown = entries
    .filter((entry) => !tag || entry.tags.includes(tag))
    .filter(
      (entry) =>
        !needle ||
        `${entry.name} ${entry.description} ${entry.author.name} ${entry.tags.join(" ")}`
          .toLowerCase()
          .includes(needle),
    )
    .sort((a, b) => {
      // The reference wrapper leads every ordering; it is a starting point, not a
      // recommendation, and burying it helps nobody.
      if (Boolean(a.reference) !== Boolean(b.reference)) {
        return a.reference ? -1 : 1;
      }
      return sorts[sort].compare(a, b);
    });
  const tags = tagVocabulary.filter((name) => entries.some((entry) => entry.tags.includes(name)));
  return (
    <Layout
      description="Configurations you can inherit, with what each one pins and how to consume it."
      title="Showcase"
    >
      <main className={styles.page}>
        <h1>Configurations in the wild</h1>
        <p className={styles.lead}>
          Every configuration here is maintained by its author. Inheriting one means running its
          choices and the binaries it pins — read it before you adopt it.
        </p>
        {entries.length >= listControlsThreshold && (
          <div className={styles.controls}>
            <label className={styles.search}>
              <span>Find a configuration</span>
              <input
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Name, author or stack"
                type="search"
                value={query}
              />
            </label>
            <label className={styles.sort}>
              <span>Sort by</span>
              <select
                onChange={(event) => setSort(event.target.value as keyof typeof sorts)}
                value={sort}
              >
                {Object.entries(sorts).map(([id, option]) => (
                  <option key={id} value={id}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
          </div>
        )}
        {entries.length >= listControlsThreshold && (
          <div aria-label="Filter by stack" className={styles.filters} role="group">
            <button aria-pressed={tag === ""} onClick={() => setTag("")} type="button">
              All
            </button>
            {tags.map((name) => (
              <button
                aria-pressed={tag === name}
                key={name}
                onClick={() => setTag(tag === name ? "" : name)}
                type="button"
              >
                {name}
              </button>
            ))}
          </div>
        )}
        {shown.length === 0 && (
          <p className={styles.empty}>Nothing matches that. Clear the search to see every entry.</p>
        )}
        <ul className={styles.grid}>
          {shown.map((entry) => (
            <Entry entry={entry} key={entry.id} />
          ))}
          <li className={styles.invite}>
            <h2>Your config?</h2>
            <p>Publish your datamitsu configuration here.</p>
            <p className={styles.inviteNote}>
              A repository, one way to consume it, and a sentence about the repositories it is built
              for. A dataset, if you publish one, adds the runtime fingerprint.
            </p>
            <p>
              <a href={editURL}>Add it to showcases.json ↗</a>
            </p>
            <p>
              <Link to="/docs/contributing/showcase">What an entry needs →</Link>
            </p>
          </li>
        </ul>
      </main>
    </Layout>
  );
}

function Entry({ entry }: { entry: ShowcaseEntry }) {
  const facts = (derived as Record<string, ShowcaseDerived>)[entry.id];
  const composition = facts?.composition;
  const release = facts?.repository?.lastRelease;
  return (
    <li className={styles.entry}>
      <div className={styles.entryHead}>
        <h2>
          <a href={entry.links.repository}>{entry.name}</a>
        </h2>
        {entry.reference && <span className={styles.reference}>Reference wrapper</span>}
      </div>
      <p className={styles.author}>
        by <a href={`https://github.com/${entry.author.github}`}>{entry.author.name}</a>
      </p>
      <p className={styles.description}>{entry.description}</p>
      <ul className={styles.tags}>
        {entry.tags.map((tag) => (
          <li key={tag}>{tag}</li>
        ))}
      </ul>
      {composition ? (
        <div className={styles.fingerprint}>
          <div aria-hidden="true" className={styles.bar}>
            {Object.entries(composition.runtimes).map(([runtime, count]) => (
              <span
                key={runtime}
                style={{
                  background: `var(--runtime-${runtime}, var(--runtime-unknown))`,
                  flexGrow: count,
                }}
                title={`${runtimeNames[runtime] ?? runtime}: ${count}`}
              />
            ))}
          </div>
          <p>
            {composition.apps} managed apps ·{" "}
            {Object.entries(composition.runtimes)
              .map(([runtime, count]) => `${runtimeNames[runtime] ?? runtime} ${count}`)
              .join(" · ")}
          </p>
        </div>
      ) : (
        <p className={styles.noFingerprint}>
          No published dataset, so this entry shows no composition.
        </p>
      )}
      <div className={styles.consume}>
        <p className={styles.consumeTitle}>How to use it</p>
        {entry.consume.map((option) => (
          <Snippet
            key={snippetFor(option)}
            label={consumeLabels[option.kind]}
            text={snippetFor(option)}
          />
        ))}
      </div>
      <p className={styles.links}>
        {entry.links.site && <a href={entry.links.site}>Site ↗</a>}
        {entry.links.inspector && <a href={entry.links.inspector}>Inspector ↗</a>}
        <a href={entry.links.repository}>Repository ↗</a>
      </p>
      <p className={styles.freshness}>
        {release ? (
          isStale(release) ? (
            <span className={styles.stale}>No release since {release.slice(0, 10)}</span>
          ) : (
            <>Updated {release.slice(0, 10)}</>
          )
        ) : (
          <>No releases yet</>
        )}
        {facts?.errors.length ? ` · last refresh: ${facts.errors.join("; ")}` : ""}
      </p>
    </li>
  );
}

function isStale(published: string): boolean {
  return Date.now() - new Date(published).getTime() > staleAfterDays * 24 * 60 * 60 * 1000;
}

function released(id: string): string {
  return (derived as Record<string, ShowcaseDerived>)[id]?.repository?.lastRelease ?? "";
}

function Snippet({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className={styles.snippet}>
      {/* The button sits in the header row: a remote config snippet is wider
          than the card, and the code below it scrolls under nothing. */}
      <div className={styles.snippetHead}>
        <span>{label}</span>
        <button
          onClick={() => {
            navigator.clipboard
              .writeText(text)
              .then(() => setCopied(true))
              .catch(() => setCopied(false));
          }}
          type="button"
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre>
        <code>{text}</code>
      </pre>
    </div>
  );
}

function snippetFor(option: ShowcaseEntry["consume"][number]): string {
  switch (option.kind) {
    case "gem": {
      return `gem install ${option.package}`;
    }
    case "npm": {
      return `pnpm add -D ${option.package}`;
    }
    case "oci": {
      return `oci: {\n  ref: "${option.ref}",\n}`;
    }
    case "pypi": {
      return `uv add --dev ${option.package}`;
    }
    default: {
      // What you paste into your own config, hash included: the core refuses a
      // remote config without one.
      return `function getRemoteConfigs() {\n  return [\n    {\n      url: "${option.url}",\n      hash: "${option.hash}",\n    },\n  ];\n}`;
    }
  }
}
