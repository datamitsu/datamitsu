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
    const [loadingState, setLoadingState] = useState<LoadingState>("idle");
    const [errorState, setErrorState] = useState<Error | null>(null);

    // Theme detection (fallback to Docusaurus theme)
    const docusaurusTheme = useDocusaurusTheme();
    const theme =
      options.theme ||
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ((siteConfig.themeConfig as any)?.asciinema?.themes?.[docusaurusTheme] ??
        (docusaurusTheme === "light" ? "solarized-light" : "monokai")); // cspell:disable-line

    // Retry handler
    const retryLoad = useCallback(() => {
      setErrorState(null);
      setLoadingState("loading");
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

      setLoadingState("loading");
      setErrorState(null);

      import("asciinema-player")
        .then((module_) => {
          if (!isMounted || !containerReference.current) {
            return;
          }

          try {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            player = (module_ as any).create(src, containerReference.current, {
              ...options,
              theme,
            }) as AsciinemaPlayerInstance;

            playerInstance.current = player;

            // Player creation success
            if (isMounted) {
              setLoadingState("loaded");
              onLoad?.();
            }
          } catch (error) {
            if (isMounted) {
              const errorObject =
                error instanceof Error ? error : new Error("Failed to create player");
              setErrorState(errorObject);
              setLoadingState("error");
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
          setErrorState(errorObject);
          setLoadingState("error");
          onError?.(errorObject);
        });

      return () => {
        isMounted = false;
        player?.dispose?.();
        playerInstance.current = null;
      };
    }, [src, theme, options, onLoad, onError]);

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
