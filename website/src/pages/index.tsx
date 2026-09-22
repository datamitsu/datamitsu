import { useEffect, useRef, useState } from "react";

import Head from "@docusaurus/Head";
import Link from "@docusaurus/Link";
import { useColorMode } from "@docusaurus/theme-common";
import useBaseUrl from "@docusaurus/useBaseUrl";
import LayoutProvider from "@theme/Layout/Provider";
import "@fontsource/ibm-plex-mono/latin-400.css";

import RuntimeFamilies from "../components/homepage/RuntimeFamilies";
import TerminalDemo from "../components/homepage/TerminalDemo";
import UniverseEmbed from "../components/homepage/UniverseEmbed";
import { installChannels, scatteredFiles } from "../data/landing";
import styles from "./index.module.css";

export default function Home() {
  // The page draws its own header, so it is not inside @theme/Layout — but the
  // theme toggle has to be the site's own colour mode, shared with the docs.
  return (
    <LayoutProvider>
      <Landing />
    </LayoutProvider>
  );
}

// The seven files every repository grows, and the one file they become. Static
// on purpose: the point is the arrow between the two, not a transition.
function Converge() {
  return (
    <div className={styles.converge}>
      <ul aria-hidden="true" className={styles.fileStack}>
        {scatteredFiles.map((file) => (
          <li className={styles.fileRow} key={file.name}>
            <span>↳</span>
            <code>{file.name}</code>
            <span className={styles.fileNote}>{file.note}</span>
          </li>
        ))}
      </ul>
      <div aria-hidden="true" className={styles.arrow} />
      <div className={styles.resolvedFile}>
        <span aria-hidden="true">◆</span>
        <code>datamitsu.config.js</code>
      </div>
      <p className={styles.srOnly}>
        {scatteredFiles.map((file) => `${file.name}: ${file.note}`).join("; ")} converge into
        datamitsu.config.js.
      </p>
    </div>
  );
}

function Install() {
  const [channel, setChannel] = useState("homebrew");
  const [copied, setCopied] = useState(false);
  const [manual, setManual] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>();
  const choice = installChannels.find((item) => item.id === channel)!;
  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    if (!manual) {
      return;
    }

    field.current?.focus();
    field.current?.select();
  }, [manual]);
  async function copy() {
    try {
      await navigator.clipboard.writeText(choice.command);
      setCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 2000);
    } catch {
      setManual(true);
    }
  }
  return (
    <div className={styles.install}>
      <fieldset className={styles.channels}>
        <legend>First, install the core binary</legend>
        <div className={styles.channelOptions}>
          {installChannels.map((item) => (
            <label className={styles.channelChoice} key={item.id}>
              <input
                checked={channel === item.id}
                name="install-channel"
                onChange={() => {
                  setChannel(item.id);
                  setCopied(false);
                  setManual(false);
                }}
                type="radio"
                value={item.id}
              />
              <span>{item.label}</span>
            </label>
          ))}
        </div>
      </fieldset>
      <div className={styles.installCommand}>
        {channel === "github-releases" ? (
          <a href={choice.command}>Download a verified release ↗</a>
        ) : (
          <code aria-live="polite">{choice.command}</code>
        )}
        <button aria-label="Copy installation command" onClick={copy} type="button">
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <span className={styles.srOnly} role="status">
        {copied ? "Copied to clipboard" : ""}
      </span>
      {manual && (
        <textarea
          aria-label="Select and copy installation command"
          readOnly
          ref={field}
          value={choice.command}
        />
      )}
      <p>
        {channel === "docker"
          ? "Docker runs the core in a container."
          : "One Go binary. No Node.js required."}{" "}
        Your repository supplies the config.{" "}
        <Link to={`/docs/getting-started/installation/${channel}`}>Install guide ↗</Link>
      </p>
    </div>
  );
}

function Landing() {
  const icon = useBaseUrl("/img/icon.png");
  const logo = useBaseUrl("/img/logo.png");
  return (
    <div className={styles.page}>
      <Head>
        <title>datamitsu — Your toolchain deserves a home.</title>
        <meta
          content="Define your toolchain once. Every repository inherits the tools, runtimes and configuration, delivered hash-verified by datamitsu."
          name="description"
        />
      </Head>
      <a className={styles.skip} href="#main">
        Skip to content
      </a>
      <header className={styles.header}>
        <Link className={styles.brand} to="/">
          <img alt="" height="36" src={icon} width="36" />
          <span>datamitsu</span>
        </Link>
        <nav aria-label="Main">
          <Link to="/docs/intro">Documentation ↗</Link>
          <a href="https://github.com/datamitsu/datamitsu">GitHub ↗</a>
          <ThemeToggle />
        </nav>
      </header>
      <main id="main">
        <section aria-labelledby="hero-title" className={styles.hero}>
          <div className={styles.heroCopy}>
            <p className={styles.eyebrow}>YOUR TOOLCHAIN DESERVES A HOME</p>
            <h1 id="hero-title">
              Stop setting up the same tools <em>in every repository.</em>
            </h1>
            <p className={styles.lead}>Define them once. Inherit them everywhere.</p>
            <p className={styles.sub}>
              Linters, formatters, git hooks, your own scripts and the runtimes they need — in one{" "}
              <code>datamitsu.config.js</code> you carry from project to project.
            </p>
          </div>
          <div className={styles.heroLogoWrap}>
            <img
              alt="datamitsu — the meditating bee"
              className={styles.heroLogo}
              fetchPriority="high"
              height="1818"
              src={logo}
              width="1250"
            />
          </div>
          <div className={styles.startRow}>
            <Install />
            <div className={styles.init}>
              <span>Then, inside a configured repository</span>
              <code>
                <span aria-hidden="true">$ </span>datamitsu init
              </code>
              <Link to="/docs/getting-started/quick-start">Configure your repository ↗</Link>
            </div>
          </div>
        </section>
        <section aria-labelledby="recognition-title" className={styles.recognition}>
          <div className={styles.recognitionHead}>
            <div>
              <p className={styles.eyebrow}>SOUND FAMILIAR?</p>
              <h2 id="recognition-title">
                The same setup, again,
                <br />
                in every new repository.
              </h2>
            </div>
            <p className={styles.recognitionStory}>
              By the next project, a plugin has changed, the old config is stale, and you are
              piecing it all together again. The only complete picture is in the head of whoever set
              it up.
            </p>
          </div>
          <Converge />
        </section>
        <section aria-labelledby="universe-title" className={styles.universe}>
          {/* The mechanism, not this dataset: counts belong to the frame below,
              which reads them from the snapshot and cannot fall out of step. */}
          <p className={styles.universeLead} id="universe-title">
            A real configuration. Every tool pinned to a version, verified by hash before it runs.{" "}
            <em>Hover one. Follow a runtime.</em>
          </p>
          <UniverseEmbed />
        </section>
        <section aria-labelledby="stacks-title" className={styles.stacks}>
          <h2 id="stacks-title">Any stack. One binary.</h2>
          <RuntimeFamilies />
          <p>
            A Go binary. No Node.js on the machine. Runtimes are pinned per tool and never touch the
            system ones.
          </p>
          <p>
            Your own scripts ship the same way — packed into the config, delivered with their
            runtime, run like any other tool.
          </p>
        </section>
        <section aria-label="What changes" className={styles.turn}>
          <p className={styles.coreLine}>
            The binary is the machinery. The config is your decisions.
          </p>
          <div className={styles.decisions}>
            <p>
              Define once.
              <span>Inherit shared defaults. Keep project-specific changes in a local layer.</span>
            </p>
            <p>
              Upgrade on your terms.
              <span>
                Pin versions that agree with each other. Each repository adopts an update when you
                change its pin.
              </span>
            </p>
            <p className={styles.decisionPeak}>
              One evening of tuning. Every repository benefits.
              <span>
                Get a linter right once. Every repository that inherits the config gets it right
                too.
              </span>
            </p>
          </div>
        </section>
        <section aria-labelledby="proof-title" className={styles.proof}>
          <div>
            <p className={styles.eyebrow}>SEE IT AT WORK</p>
            <h2 id="proof-title">
              Three runs.
              <br />
              One configuration.
            </h2>
            <p>
              Recorded in a repository that inherits a datamitsu config: the first run that installs
              everything, the same command again, and a narrowed run that says what it will not do.
            </p>
          </div>
          <div className={styles.recording}>
            <TerminalDemo />
          </div>
        </section>
        <section aria-labelledby="honesty-title" className={styles.honesty}>
          <p className={styles.eyebrow} id="honesty-title">
            WHERE THINGS STAND
          </p>
          <p>Alpha. The config API is not stable yet.</p>
          <p>One optional reference wrapper. You can write your own config.</p>
          <p>Not a task runner or a runtime manager for application code.</p>
        </section>
      </main>
      <footer className={styles.footer}>
        <nav aria-label="Read further">
          <Link to="/docs/getting-started/quick-start">Get started ↗</Link>
          <Link to="/docs/guides/using-wrappers">Choose a wrapper ↗</Link>
          <Link to="/docs/guides/config-inspector">Inspect a config ↗</Link>
          <Link to="/docs/reference/cli-commands">CLI reference ↗</Link>
        </nav>
        <p>
          Pin a full version. Review upgrades by hand. The hash in your config is the root of trust.
        </p>
        <span>datamitsu — Your toolchain deserves a home.</span>
      </footer>
    </div>
  );
}

function ThemeToggle() {
  const { colorMode, setColorMode } = useColorMode();
  const next = colorMode === "dark" ? "light" : "dark";
  return (
    <button
      aria-label={`Switch to the ${next} theme`}
      className={styles.themeToggle}
      onClick={() => setColorMode(next)}
      type="button"
    >
      <span aria-hidden="true">{colorMode === "dark" ? "☀" : "☾"}</span>
      {next === "dark" ? "Dark" : "Light"}
    </button>
  );
}
