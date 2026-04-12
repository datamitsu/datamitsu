import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";

import BrowserOnly from "@docusaurus/BrowserOnly";

import type { AsciinemaPlayerHandle } from "../common/AsciinemaPlayer/types";
import type { CodeCardTab } from "../ui/CodeCard";

import AsciinemaPlayer from "../common/AsciinemaPlayer";
import { CodeCard, CodeCardHeader, CodeCardTabs } from "../ui/CodeCard";
import styles from "./TerminalDemo.module.css";

type RunKind = "cold" | "warm";

const CAST_FILES: Record<RunKind, string> = {
  cold: "/cold.cast",
  warm: "/warm.cast",
};

const LABELS: Record<RunKind, string> = {
  cold: "cold start",
  warm: "cached",
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

// Helper: Debounce function to avoid excessive re-renders
function debounce<T extends (...args: any[]) => any>(
  func: T,
  wait: number,
): (...args: Parameters<T>) => void {
  let timeoutId: null | ReturnType<typeof setTimeout> = null;

  return (...args: Parameters<T>) => {
    if (timeoutId !== null) {
      clearTimeout(timeoutId);
    }
    timeoutId = setTimeout(() => func(...args), wait);
  };
}

// Helper: Get terminal dimensions based on viewport width
function getBreakpointDimensions(width: number): { cols: number; rows: number } {
  if (width <= 480) {
    return { cols: 60, rows: 18 };
  } else if (width <= 768) {
    return { cols: 80, rows: 24 };
  } else if (width <= 1024) {
    return { cols: 100, rows: 28 };
  }
  return { cols: 120, rows: 30 };
}

function TerminalDemoInner(): ReactNode {
  const [activeKind, setActiveKind] = useState<RunKind>("cold");
  const [announcement, setAnnouncement] = useState("");
  const { cols, rows } = useTerminalDimensions();
  const docusaurusTheme = useDocusaurusTheme();
  const playerRef = useRef<AsciinemaPlayerHandle>(null);
  const isFirstRender = useRef(true);

  const playerOptions = useMemo(
    () => ({
      autoPlay: false,
      cols,
      controls: "auto" as const,
      fit: "width" as const,
      loop: false,
      preload: true,
      rows,
      speed: 1,
    }),
    [cols, rows],
  );

  // Announce tab changes to screen readers
  useEffect(() => {
    if (activeKind) {
      // Use setTimeout to defer state update and avoid synchronous setState in effect
      const announceTimer = setTimeout(() => {
        setAnnouncement(`Switched to ${LABELS[activeKind]} demo`);
      }, 0);
      const clearTimer = setTimeout(() => setAnnouncement(""), 1000);

      return () => {
        clearTimeout(announceTimer);
        clearTimeout(clearTimer);
      };
    }
  }, [activeKind]);

  // Auto-play when tab changes (but not on initial render)
  useEffect(() => {
    if (isFirstRender.current) {
      isFirstRender.current = false;
      return;
    }

    const timer = setTimeout(() => {
      if (playerRef.current?.isReady()) {
        playerRef.current.play();
      }
    }, 100);

    return () => clearTimeout(timer);
  }, [activeKind]);

  // Keyboard navigation handler
  const handleKeyDown = useCallback((e: React.KeyboardEvent, currentKind: string) => {
    const kinds: RunKind[] = ["cold", "warm"];
    const currentIndex = kinds.indexOf(currentKind as RunKind);

    switch (e.key) {
      case "ArrowLeft": {
        e.preventDefault();
        const prevIndex = (currentIndex - 1 + kinds.length) % kinds.length;
        setActiveKind(kinds[prevIndex]);
        setTimeout(() => {
          globalThis.document.getElementById(`tab-${kinds[prevIndex]}`)?.focus();
        }, 0);

        break;
      }
      case "ArrowRight": {
        e.preventDefault();
        const nextIndex = (currentIndex + 1) % kinds.length;
        setActiveKind(kinds[nextIndex]);
        setTimeout(() => {
          globalThis.document.getElementById(`tab-${kinds[nextIndex]}`)?.focus();
        }, 0);

        break;
      }
      case "End": {
        e.preventDefault();
        const lastKind = kinds.at(-1)!; // Non-null assertion: kinds array is always non-empty
        setActiveKind(lastKind);
        setTimeout(() => {
          globalThis.document.getElementById(`tab-${lastKind}`)?.focus();
        }, 0);

        break;
      }
      case "Home": {
        e.preventDefault();
        setActiveKind(kinds[0]);
        setTimeout(() => {
          globalThis.document.getElementById(`tab-${kinds[0]}`)?.focus();
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
    (): CodeCardTab[] => [
      { active: activeKind === "cold", id: "cold", label: LABELS.cold },
      { active: activeKind === "warm", id: "warm", label: LABELS.warm },
    ],
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

  const headerRightContent = <span aria-label="Repository name">ovineko/ovineko</span>;

  return (
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
      {/* Screen reader announcements */}
      <div aria-atomic="true" aria-live="polite" className={styles.srOnly} role="status">
        {announcement}
      </div>

      {/* key forces remount+replay when switching tabs or dimensions change */}
      <div
        aria-labelledby={`tab-${activeKind}`}
        aria-live="polite"
        id={`panel-${activeKind}`}
        role="tabpanel"
      >
        <AsciinemaPlayer
          key={`${activeKind}-${cols}-${rows}-${docusaurusTheme}`}
          options={playerOptions}
          ref={playerRef}
          src={CAST_FILES[activeKind]}
        />
      </div>
    </CodeCard>
  );
}

// Hook: Detect Docusaurus theme (light/dark)
function useDocusaurusTheme(): "dark" | "light" {
  const [theme, setTheme] = useState<"dark" | "light">(() => {
    if (typeof document === "undefined") {
      return "dark"; // SSR fallback
    }
    return globalThis.document.documentElement.dataset.theme === "light" ? "light" : "dark";
  });

  useEffect(() => {
    // Listen for theme changes via MutationObserver
    const observer = new MutationObserver((mutations) => {
      mutations.forEach((mutation) => {
        if (mutation.type === "attributes" && mutation.attributeName === "data-theme") {
          const updatedTheme = globalThis.document.documentElement.dataset.theme;
          setTheme(updatedTheme === "light" ? "light" : "dark");
        }
      });
    });

    observer.observe(globalThis.document.documentElement, {
      attributeFilter: ["data-theme"],
      attributes: true,
    });

    return () => observer.disconnect();
  }, []);

  return theme;
}

// Hook: Responsive terminal dimensions based on viewport
function useTerminalDimensions(): { cols: number; rows: number } {
  const [dimensions, setDimensions] = useState(() => {
    if (globalThis.window === undefined) {
      return { cols: 80, rows: 24 }; // SSR fallback
    }
    return getBreakpointDimensions(globalThis.window.innerWidth);
  });

  const handleResize = useMemo(
    () =>
      debounce(() => {
        setDimensions(getBreakpointDimensions(globalThis.window.innerWidth));
      }, 300),
    [],
  );

  // eslint-disable-next-line fsecond/valid-event-listener -- Custom debounced handler requires direct addEventListener
  useEffect(() => {
    globalThis.window.addEventListener("resize", handleResize);
    return () => {
      globalThis.window.removeEventListener("resize", handleResize);
    };
  }, [handleResize]);

  return dimensions;
}
