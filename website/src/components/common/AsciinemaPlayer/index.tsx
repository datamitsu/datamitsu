import type { ReactNode } from "react";
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";

import useDocusaurusContext from "@docusaurus/useDocusaurusContext";
// CSS extracted at build time by webpack — safe at module level
import "asciinema-player/dist/bundle/asciinema-player.css";

import type {
  AsciinemaPlayerHandle,
  AsciinemaPlayerInstance,
  AsciinemaPlayerProps as AsciinemaPlayerProperties,
  LoadingState,
} from "./types";

import styles from "./AsciinemaPlayer.module.css";
import { parseAsciicastByLines } from "./lineFrames";

// Result of a single initialization run, tagged with the run that produced it.
interface LoadOutcome {
  error: Error | null;
  runKey: string;
  state: "error" | "loaded";
}

// Hook: Detect Docusaurus theme (light/dark)
function useDocusaurusTheme(): "dark" | "light" {
  const [theme, setTheme] = useState<"dark" | "light">(() => {
    if (typeof document === "undefined") {
      return "dark"; // SSR fallback
    }
    return document.documentElement.dataset.theme === "light" ? "light" : "dark";
  });

  useEffect(() => {
    // Listen for theme changes via MutationObserver
    const observer = new MutationObserver((mutations) => {
      for (const mutation of mutations) {
        // `return` here was a `forEach` early-exit, i.e. skip this mutation.
        if (mutation.type !== "attributes" || mutation.attributeName !== "data-theme") {
          continue;
        }

        const updatedTheme = document.documentElement.dataset.theme;
        setTheme(updatedTheme === "light" ? "light" : "dark");
      }
    });

    observer.observe(document.documentElement, {
      attributeFilter: ["data-theme"],
      attributes: true,
    });

    return () => observer.disconnect();
  }, []);

  return theme;
}

const AsciinemaPlayer = forwardRef<AsciinemaPlayerHandle, AsciinemaPlayerProperties>(
  ({ className, onError, onLoad, options = {}, src }, reference): ReactNode => {
    const { siteConfig } = useDocusaurusContext();
    const containerReference = useRef<HTMLDivElement>(null);
    const playerInstance = useRef<AsciinemaPlayerInstance | null>(null);
    const [attempt, setAttempt] = useState(0);
    // The outcome of one load, tagged with the run that produced it. Anything the
    // current run has not settled yet reads as "loading" during render, so the
    // effect never has to reset the state synchronously.
    const [outcome, setOutcome] = useState<LoadOutcome | null>(null);

    // Theme detection (fallback to Docusaurus theme)
    const docusaurusTheme = useDocusaurusTheme();
    const theme =
      options.theme ||
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ((siteConfig.themeConfig as any)?.asciinema?.themes?.[docusaurusTheme] ?? "datamitsu");

    // The canvas samples the theme class's custom properties once, when it mounts,
    // and one class serves both color modes — so the mode is part of a run's
    // identity: flipping it starts a new run rather than recoloring what is drawn.
    const runKey = `${attempt}\u{0}${theme}\u{0}${docusaurusTheme}\u{0}${src}`;
    const settled = outcome?.runKey === runKey ? outcome : null;
    const loadingState: LoadingState = settled?.state ?? "loading";
    const errorState = settled?.error ?? null;

    // Retry handler: a new attempt re-runs the initialization effect.
    const retryLoad = useCallback(() => {
      setAttempt((previous) => previous + 1);
    }, []);

    // Imperative handle
    useImperativeHandle(
      reference,
      () => ({
        getCurrentTime: () => playerInstance.current?.getCurrentTime() ?? null,
        getDuration: () => playerInstance.current?.getDuration() ?? null,
        getPlayerInstance: () => playerInstance.current,
        isPaused: () => playerInstance.current?.isPaused() ?? true,
        isPlaying: () => playerInstance.current?.isPlaying() ?? false,
        isReady: () => loadingState === "loaded",
        pause: () => playerInstance.current?.pause(),
        play: () => playerInstance.current?.play(),
        restart: () => {
          playerInstance.current?.seek(0);
          playerInstance.current?.play();
        },
        seek: (time: number) => playerInstance.current?.seek(time),
      }),
      [loadingState],
    );

    // Player initialization effect
    useEffect(() => {
      let isMounted = true;
      let player: AsciinemaPlayerInstance | undefined;

      import("asciinema-player")
        .then((module_) => {
          if (!isMounted || !containerReference.current) {
            return;
          }

          try {
            // A source object rather than a bare URL, so the recording goes
            // through the line-splitting parser: one output event per terminal
            // line, which is what the player's `.` / `,` frame stepping walks.
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            player = (module_ as any).create(
              { parser: parseAsciicastByLines, url: src },
              containerReference.current,
              // datamitsu colors its duration heatmap with xterm 256-color
              // codes; adaptivePalette derives those from the sixteen the theme
              // class sets, so a recording never shows a color the page did not
              // choose. See src/css/terminal.css.
              { adaptivePalette: true, ...options, theme },
            ) as AsciinemaPlayerInstance;

            playerInstance.current = player;

            // Player creation success
            if (isMounted) {
              setOutcome({ error: null, runKey, state: "loaded" });
              onLoad?.();
            }
          } catch (error) {
            if (isMounted) {
              const errorObject =
                error instanceof Error ? error : new Error("Failed to create player");
              setOutcome({ error: errorObject, runKey, state: "error" });
              onError?.(errorObject);
            }
          }
        })
        .catch((error) => {
          if (!isMounted) {
            return;
          }

          const errorObject =
            error instanceof Error ? error : new Error("Failed to load player module");
          setOutcome({ error: errorObject, runKey, state: "error" });
          onError?.(errorObject);
        });

      return () => {
        isMounted = false;
        player?.dispose?.();
        playerInstance.current = null;
      };
    }, [runKey, src, theme, options, onLoad, onError]);

    const containerOpacityStyle = useMemo(
      () => ({
        opacity: loadingState === "loaded" ? 1 : 0,
      }),
      [loadingState],
    );

    return (
      <div className={`${styles.playerContainer} ${className || ""}`}>
        <div ref={containerReference} style={containerOpacityStyle} />

        {loadingState === "loading" && (
          <div aria-live="polite" className={styles.loadingOverlay} role="status">
            <div aria-hidden="true" className={styles.spinner} />
            <div className={styles.loadingText}>Loading terminal recording...</div>
          </div>
        )}

        {loadingState === "error" && (
          <div aria-live="assertive" className={styles.errorContainer} role="alert">
            <div aria-hidden="true" className={styles.errorIcon}>
              ⚠️
            </div>
            <div className={styles.errorMessage}>
              {errorState?.message || "Failed to load terminal recording"}
            </div>
            <button className={styles.retryButton} onClick={retryLoad}>
              Retry
            </button>
          </div>
        )}
      </div>
    );
  },
);

AsciinemaPlayer.displayName = "AsciinemaPlayer";

export default AsciinemaPlayer;
