import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";

import BrowserOnly from "@docusaurus/BrowserOnly";
import Link from "@docusaurus/Link";

import type { CodeCardTab } from "../ui/CodeCard";

import recordings from "../../data/recordings.json";
import AsciinemaPlayer from "../common/AsciinemaPlayer";
import { CodeCard, CodeCardHeader, CodeCardTabs } from "../ui/CodeCard";
import styles from "./TerminalDemo.module.css";

type RunKind = "cold" | "scope" | "warm";
const runKinds: RunKind[] = ["cold", "warm", "scope"];

const CAST_FILES: Record<RunKind, string> = {
  cold: "/cold.cast",
  scope: "/scope.cast",
  warm: "/warm.cast",
};

const LABELS: Record<RunKind, string> = {
  cold: "cold start",
  scope: "scope plan",
  warm: "cached",
};

const CAPTIONS: Record<RunKind, ReactNode> = {
  cold: "A first run against an empty store: every pinned tool is downloaded and verified before anything runs.",
  scope: (
    <>
      From a subdirectory, ask about one file. The plan includes prettier and skips syncpack: its
      verdict needs the whole repository.{" "}
      <Link to="/docs/reference/cli-commands#narrowed-runs">How narrowing works ↗</Link>
    </>
  ),
  warm: "The same command immediately after, reusing that store: nothing to download, and the tools whose verdicts still hold are not run again.",
};

const FALLBACK_STYLE = { minHeight: 320 };
const FALLBACK_INNER_STYLE = { minHeight: 320 };

export default function TerminalDemo(): ReactNode {
  return (
    <BrowserOnly
      fallback={
        <div style={FALLBACK_STYLE}>
          <CodeCard theme="auto">
            <div style={FALLBACK_INNER_STYLE} />
          </CodeCard>
        </div>
      }
    >
      {() => <TerminalDemoInner />}
    </BrowserOnly>
  );
}

function TerminalDemoInner(): ReactNode {
  const [activeKind, setActiveKind] = useState<RunKind>("cold");
  const [announcement, setAnnouncement] = useState("");

  const playerOptions = useMemo(
    () => ({
      autoPlay: false,
      // The size the run was recorded at, from the capture itself: a player
      // narrower than the recording wraps lines that never wrapped.
      cols: recordings[activeKind].cols,
      controls: true,
      fit: "width" as const,
      loop: false,
      // The last frame of each recording, so a tab opens on its result rather
      // than on an empty prompt. The cold start's result carries a red 55s
      // beside eslint; that is what a first run costs, and the tab beside it
      // says what the second one costs.
      poster: recordings[activeKind].poster,
      preload: true,
      rows: recordings[activeKind].rows,
      speed: 1,
    }),
    [activeKind],
  );

  useEffect(() => {
    if (!activeKind) {
      return;
    }

    const announceTimer = setTimeout(() => {
      setAnnouncement(`Switched to ${LABELS[activeKind]} demo`);
    }, 0);
    const clearTimer = setTimeout(() => setAnnouncement(""), 1000);

    return () => {
      clearTimeout(announceTimer);
      clearTimeout(clearTimer);
    };
  }, [activeKind]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent, currentKind: string) => {
    const kinds = runKinds;
    const currentIndex = kinds.indexOf(currentKind as RunKind);

    switch (e.key) {
      case "ArrowLeft": {
        e.preventDefault();
        const previousIndex = (currentIndex - 1 + kinds.length) % kinds.length;
        setActiveKind(kinds[previousIndex]);
        setTimeout(() => {
          document.getElementById(`tab-${kinds[previousIndex]}`)?.focus();
        }, 0);

        break;
      }
      case "ArrowRight": {
        e.preventDefault();
        const nextIndex = (currentIndex + 1) % kinds.length;
        setActiveKind(kinds[nextIndex]);
        setTimeout(() => {
          document.getElementById(`tab-${kinds[nextIndex]}`)?.focus();
        }, 0);

        break;
      }
      case "End": {
        e.preventDefault();
        const lastKind = kinds.at(-1)!; // Non-null assertion: kinds array is always non-empty
        setActiveKind(lastKind);
        setTimeout(() => {
          document.getElementById(`tab-${lastKind}`)?.focus();
        }, 0);

        break;
      }
      case "Home": {
        e.preventDefault();
        setActiveKind(kinds[0]);
        setTimeout(() => {
          document.getElementById(`tab-${kinds[0]}`)?.focus();
        }, 0);

        break;
      }
      // No default
    }
  }, []);

  const handleTabSelect = useCallback((id: string) => {
    setActiveKind(id as RunKind);
  }, []);

  const tabs = useMemo(
    (): CodeCardTab[] =>
      runKinds.map((id) => ({ active: activeKind === id, id, label: LABELS[id] })),
    [activeKind],
  );

  const headerLeftContent = (
    <CodeCardTabs
      ariaLabel="Demo recordings"
      onKeyDown={handleKeyDown}
      onTabSelect={handleTabSelect}
      tabs={tabs}
    />
  );

  const recording = recordings[activeKind];
  const headerRightContent = <span aria-label="Recording source">ovineko/ovineko</span>;

  return (
    <>
      <CodeCard
        header={
          <CodeCardHeader
            leftContent={headerLeftContent}
            rightContent={headerRightContent}
            theme="auto"
          />
        }
        theme="auto"
      >
        <div aria-atomic="true" aria-live="polite" className={styles.srOnly} role="status">
          {announcement}
        </div>

        <div
          aria-labelledby={`tab-${activeKind}`}
          aria-live="polite"
          id={`panel-${activeKind}`}
          role="tabpanel"
        >
          <AsciinemaPlayer key={activeKind} options={playerOptions} src={CAST_FILES[activeKind]} />
        </div>
      </CodeCard>
      <p className={styles.caption}>{CAPTIONS[activeKind]}</p>
      <p className={styles.meta}>
        Recorded output, not live · <code>{recording.command}</code>
        {recording.cwd ? (
          <>
            {" "}
            in <code>{recording.cwd}</code>
          </>
        ) : null}{" "}
        · ovineko/ovineko@{recording.revision.slice(0, 7)} · {recording.recordedAt}
      </p>
    </>
  );
}
