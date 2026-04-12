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
  AsciinemaPlayerProps,
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
      mutations.forEach((mutation) => {
        if (mutation.type === "attributes" && mutation.attributeName === "data-theme") {
          const updatedTheme = document.documentElement.dataset.theme;
          setTheme(updatedTheme === "light" ? "light" : "dark");
        }
      });
    });

    observer.observe(document.documentElement, {
      attributeFilter: ["data-theme"],
      attributes: true,
    });

    return () => observer.disconnect();
  }, []);

  return theme;
}

const AsciinemaPlayer = forwardRef<AsciinemaPlayerHandle, AsciinemaPlayerProps>(
  ({ className, onError, onLoad, options = {}, src }, ref): ReactNode => {
    const { siteConfig } = useDocusaurusContext();
    const containerRef = useRef<HTMLDivElement>(null);
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
      ref,
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
      let mounted = true;
      let player: AsciinemaPlayerInstance | undefined;

      setLoadingState("loading");
      setErrorState(null);

      import("asciinema-player")
        .then((mod) => {
          if (!mounted || !containerRef.current) {
            return;
          }

          try {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            player = (mod as any).create(src, containerRef.current, {
              ...options,
              theme,
            }) as AsciinemaPlayerInstance;

            playerInstance.current = player;

            // Player creation success
            if (mounted) {
              setLoadingState("loaded");
              onLoad?.();
            }
          } catch (error) {
            if (mounted) {
              const errorObj =
                error instanceof Error ? error : new Error("Failed to create player");
              setErrorState(errorObj);
              setLoadingState("error");
              onError?.(errorObj);
            }
          }
        })
        .catch((error) => {
          if (mounted) {
            const errorObj =
              error instanceof Error ? error : new Error("Failed to load player module");
            setErrorState(errorObj);
            setLoadingState("error");
            onError?.(errorObj);
          }
        });

      return () => {
        mounted = false;
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
        <div ref={containerRef} style={containerOpacityStyle} />

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
