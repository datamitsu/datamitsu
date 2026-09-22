import type { CSSProperties } from "react";

import useBaseUrl from "@docusaurus/useBaseUrl";

import { embedSizes, fallbackText, readLocation } from "../../../../inspector/src/embedding";
import snapshot from "../../data/reference-config.json";
import styles from "./styles.module.css";

export default function ConfigEmbed({
  projectType = "",
  showVersion = true,
  src,
  tool = "",
  view = "blueprints",
}: {
  projectType?: string;
  showVersion?: boolean;
  src?: string;
  tool?: string;
  view?: "blueprints" | "operations" | "universe";
}) {
  const query = new URLSearchParams({
    embed: view === "blueprints" ? "runtimes" : view,
    theme: "auto",
    view,
  });
  if (tool) {
    query.set("tool", tool);
  }
  if (projectType) {
    query.set("projectType", projectType);
  }
  const route = readLocation(`?${query}`, "", snapshot.manifest);
  const localAtlas = useBaseUrl("/atlas.html");
  const atlas = src ?? localAtlas;
  const size = embedSizes[view];
  return (
    <figure className={styles.figure}>
      <iframe
        className={styles.frame}
        loading="lazy"
        referrerPolicy="no-referrer"
        sandbox="allow-scripts"
        src={`${atlas}?${query}`}
        style={
          {
            "--embed-min": `${size.minHeight}px`,
            "--embed-ratio": size.ratio,
          } as CSSProperties
        }
        title={`Reference configuration — ${view}`}
      />
      {src ? (
        <p className={styles.fallback}>
          This section loads the published configuration. <a href={atlas}>Open its source HTML ↗</a>
        </p>
      ) : view === "blueprints" ? (
        <p className={styles.fallback}>{fallbackText(snapshot.manifest, route)}</p>
      ) : (
        <details className={styles.fallback}>
          <summary>Read the snapshot as text</summary>
          <p>{fallbackText(snapshot.manifest, route)}</p>
          {view === "universe" && (
            <p>{snapshot.manifest.apps.map((app) => `${app.name} (${app.runtime})`).join(" · ")}</p>
          )}
        </details>
      )}
      <figcaption className={styles.caption}>
        {!src && (
          <span>
            <code>{snapshot.manifest.name}</code>
            <br />
            {showVersion && (
              <>
                <code>
                  {snapshot.package} {snapshot.version}
                </code>{" "}
                ·{" "}
              </>
            )}
            Captured {snapshot.capturedAt.slice(0, 10)}
          </span>
        )}
        <a href={`${atlas}?view=universe`}>Open the full atlas ↗</a>
      </figcaption>
    </figure>
  );
}
