import { useEffect, useRef, useState, useSyncExternalStore } from "react";

import { useColorMode } from "@docusaurus/theme-common";
import useBaseUrl from "@docusaurus/useBaseUrl";

import { readLocation, themeMessage } from "../../../../inspector/src/embedding";
import { runtimeCounts } from "../../../../inspector/src/selection";
import snapshot from "../../data/reference-config.json";
import OrbitPoster from "./OrbitPoster";
import styles from "./UniverseEmbed.module.css";

function frameQuery(theme: string) {
  return new URLSearchParams({ embed: "universe", preset: "minimal", theme, view: "universe" });
}
const query = frameQuery("auto");
const route = readLocation(`?${query}`, "", snapshot.manifest);
const groups = runtimeCounts(snapshot.manifest, route);
const distribution = groups.map((group) => `${group.label} ${group.count}`).join(" · ");
const compactQuery = "(max-width: 900px), (hover: none)";
const loadTimeout = 12_000;
const themeAckTimeout = 600;

export default function UniverseEmbed() {
  const atlas = useBaseUrl("/atlas.html");
  const isCompact = useMedia(compactQuery);
  const { colorMode } = useColorMode();
  const [isRequested, setRequested] = useState(false);
  const [isLoaded, setLoaded] = useState(false);
  const [hasFailed, setFailed] = useState(false);
  // Changing the URL reloads the frame, so it carries the theme the frame first
  // loads with and only moves when a theme message went unanswered. `shown` is
  // what the frame is actually displaying.
  const [frameTheme, setFrameTheme] = useState("");
  const shown = useRef("");
  const mode = useRef(colorMode);
  const frame = useRef<HTMLIFrameElement>(null);
  const srcTheme = frameTheme || colorMode;
  const stage = useRef<HTMLDivElement>(null);
  useEffect(() => {
    mode.current = colorMode;
  }, [colorMode]);
  const request = () => {
    setFrameTheme(mode.current);
    setRequested(true);
  };
  useEffect(() => {
    const element = stage.current;
    if (isCompact || isRequested || !element) {
      return;
    }
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (!entry.isIntersecting) {
          return;
        }

        setFrameTheme(mode.current);
        setRequested(true);
      },
      { rootMargin: "300px" },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [isCompact, isRequested]);
  useEffect(() => {
    if (!isRequested || isLoaded || hasFailed) {
      return;
    }
    const timer = setTimeout(() => setFailed(true), loadTimeout);
    return () => clearTimeout(timer);
  }, [isRequested, isLoaded, hasFailed]);
  useEffect(() => {
    const target = frame.current?.contentWindow;
    if (!isLoaded || !target || shown.current === colorMode) {
      return;
    }
    let isAcknowledged = false;
    const stopListening = whileAcknowledging(() => {
      isAcknowledged = true;
      shown.current = colorMode;
    });
    target.postMessage({ type: themeMessage.request, value: colorMode }, "*");
    // A frame built before the message existed stays in its loaded theme, so
    // reload it into the new one instead of leaving a dark orbit on a light page.
    const timer = setTimeout(() => {
      if (!isAcknowledged) {
        setFrameTheme(colorMode);
      }
    }, themeAckTimeout);
    return () => {
      clearTimeout(timer);
      stopListening();
    };
  }, [colorMode, frameTheme, isLoaded]);
  return (
    <figure className={styles.embed}>
      <div className={styles.stage} data-live={isLoaded} ref={stage}>
        {hasFailed ? (
          <div className={styles.fallback}>
            <p>The live orbit did not load. Its runtime distribution, from the same snapshot:</p>
            <div aria-hidden="true" className={styles.bar}>
              {groups.map((group) => (
                <span
                  key={group.runtime}
                  style={{
                    background: `var(--runtime-${group.runtime}, var(--runtime-unknown))`,
                    flexGrow: group.count,
                  }}
                />
              ))}
            </div>
            <p className={styles.counts}>{distribution}</p>
          </div>
        ) : (
          <>
            <OrbitPoster className={styles.poster} />
            {isRequested && (
              <iframe
                className={styles.frame}
                loading="lazy"
                onError={() => setFailed(true)}
                onLoad={() => {
                  shown.current = srcTheme;
                  setLoaded(true);
                }}
                ref={frame}
                referrerPolicy="no-referrer"
                sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"
                src={`${atlas}?${frameQuery(srcTheme)}`}
                title="Reference configuration — app orbit"
              />
            )}
            {isCompact && !isRequested && (
              <button className={styles.invite} onClick={request} type="button">
                Tap to explore the orbit
              </button>
            )}
          </>
        )}
      </div>
    </figure>
  );
}

function useMedia(condition: string) {
  return useSyncExternalStore(
    (callback) => {
      const media = matchMedia(condition);
      media.addEventListener("change", callback);
      return () => media.removeEventListener("change", callback);
    },
    () => matchMedia(condition).matches,
    () => false,
  );
}

function whileAcknowledging(onAcknowledged: () => void) {
  const listen = (event: MessageEvent) => {
    if ((event.data as null | { type?: string })?.type === themeMessage.ack) {
      onAcknowledged();
    }
  };
  window.addEventListener("message", listen);
  return () => window.removeEventListener("message", listen);
}
