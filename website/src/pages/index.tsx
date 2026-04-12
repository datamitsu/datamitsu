import type { ReactNode } from "react";

import Link from "@docusaurus/Link";
import useDocusaurusContext from "@docusaurus/useDocusaurusContext";
import CodeBlock from "@theme/CodeBlock";
import HeadingLib from "@theme/Heading";
import Layout from "@theme/Layout";

import HexagonCard from "../components/homepage/HexagonCard";
import TerminalDemo from "../components/homepage/TerminalDemo";
import { CodeCard, CodeCardHeader } from "../components/ui/CodeCard";
import styles from "./index.module.css";

const Heading = HeadingLib as React.FC<{
  as: "h1" | "h2" | "h3" | "h4" | "h5" | "h6";
  children: React.ReactNode;
  className?: string;
}>;

interface FeatureItem {
  description: string;
  icon: ReactNode;
  title: string;
}

const FeatureList: FeatureItem[] = [
  {
    description:
      "Stable, verifiable, reproducible core. You bring the opinions. datamitsu ensures they travel with you.",
    icon: (
      <svg
        fill="none"
        height="28"
        viewBox="0 0 24 24"
        width="28"
        xmlns="http://www.w3.org/2000/svg"
      >
        <path
          d="M12 2L3 7v5c0 5.25 3.75 10.15 9 11.25C17.25 22.15 21 17.25 21 12V7L12 2z"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
      </svg>
    ),
    title: "No Opinions, Just Foundation",
  },
  {
    description:
      "JavaScript-powered configuration with chaining and inheritance. Build once, distribute everywhere via npm, gem, or pypi.",
    icon: (
      <svg
        fill="none"
        height="28"
        viewBox="0 0 24 24"
        width="28"
        xmlns="http://www.w3.org/2000/svg"
      >
        <polyline
          points="16 18 22 12 16 6"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
        <polyline
          points="8 6 2 12 8 18"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
      </svg>
    ),
    title: "Config as Code",
  },
  {
    description:
      "Manage binaries, Python (UV), Node.js (FNM), and JVM tools in isolated environments with reproducible installs.",
    icon: (
      <svg
        fill="none"
        height="28"
        viewBox="0 0 24 24"
        width="28"
        xmlns="http://www.w3.org/2000/svg"
      >
        <rect
          height="4"
          rx="1"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
          width="20"
          x="2"
          y="2"
        />
        <rect
          height="4"
          rx="1"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
          width="20"
          x="2"
          y="10"
        />
        <rect
          height="4"
          rx="1"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
          width="20"
          x="2"
          y="18"
        />
      </svg>
    ),
    title: "Multi-Runtime Support",
  },
  {
    description:
      "Every binary verified with SHA-256. No bypassing. No exceptions. Hash verification is mandatory for all downloads.",
    icon: (
      <svg
        fill="none"
        height="28"
        viewBox="0 0 24 24"
        width="28"
        xmlns="http://www.w3.org/2000/svg"
      >
        <path
          d="M12 2L3 7v5c0 5.25 3.75 10.15 9 11.25C17.25 22.15 21 17.25 21 12V7L12 2z"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
        <path
          d="M9 12l2 2 4-4"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
        />
      </svg>
    ),
    title: "Security First",
  },
];

interface SolutionStep {
  description: string;
  number: number;
  title: string;
}

const SolutionSteps: SolutionStep[] = [
  {
    description:
      "Write JavaScript config with tools, runtimes, and rules. As flexible as you need — conditions, inheritance, composition.",
    number: 1,
    title: "Describe your environment",
  },
  {
    description:
      'Share via npm, gem, or pypi. Versioned, cacheable, reproducible. No manual copying, no "how did I configure this last time".',
    number: 2,
    title: "Distribute as a package",
  },
  {
    description:
      "Change once, every dependent project moves forward together. No lost weeks before the first line of code. No configuration drift across teams.",
    number: 3,
    title: "Update in one place",
  },
];

const CODE_EXAMPLE = `/// <reference path=".datamitsu/datamitsu.config.d.ts" />

function getConfig(input) {
  return {
    ...input,
    apps: {
      ...input.apps,
      lefthook: { type: "binary", required: true },
      eslint:   { type: "fnm" },
      yamllint: { type: "uv" },
      ktlint:   { type: "jvm" },
    },
  };
}

globalThis.getConfig = getConfig;
globalThis.getMinVersion = () => "0.0.1";`;

export default function Home(): ReactNode {
  return (
    <Layout description="Stop paying the configuration tax">
      <HomepageHeader />
      <main>
        <CodeDemoSection />
        <TerminalDemoSection />
        <SolutionSection />
        <HomepageFeatures />
        <PhilosophyCallout />
        <FinalCTA />
      </main>
    </Layout>
  );
}

function CodeDemoSection() {
  return (
    <section className={styles.codeDemoSection}>
      <div className="container">
        <div className={styles.codeDemoInner}>
          <div className={styles.codeDemoText}>
            <Heading as="h2" className={`${styles.sectionHeading} ${styles.codeDemoHeading}`}>
              Configuration as code
            </Heading>
            <p className={styles.codeDemoDescription}>
              A single JavaScript file describes your entire toolchain. Extend it, override it,
              compose it. Publish it as an npm package, gem, or pypi release — or share it as a
              remote file with hash verification. Every team inherits your decisions automatically.
            </p>
          </div>
          <CodeCard header={<CodeCardHeader centerTitle title="datamitsu.config.js" />}>
            <div className={styles.codeWindowBody}>
              <CodeBlock language="javascript">{CODE_EXAMPLE}</CodeBlock>
            </div>
          </CodeCard>
        </div>
      </div>
    </section>
  );
}

function FinalCTA() {
  return (
    <section className={styles.finalCtaSection}>
      <div className="container">
        <Heading as="h2" className={styles.finalCtaHeading}>
          Your toolchain deserves a home
        </Heading>
        <p className={styles.finalCtaSubheading}>
          Not a boilerplate, not scattered across projects, not reinvented from scratch every time.
          datamitsu gives it one — versioned, composable, and always one command away.
        </p>
        <div className={styles.finalCtaButtons}>
          <Link className="button button--primary button--lg" to="/docs/category/getting-started">
            Get Started
          </Link>
          <Link className={`button button--lg ${styles.buttonSecondary}`} to="/docs/intro">
            Read the Docs
          </Link>
        </div>
      </div>
    </section>
  );
}

function HomepageFeatures() {
  return (
    <section className={styles.features}>
      <div className="container">
        <Heading as="h2" className={styles.sectionHeading}>
          A foundation, not a boilerplate
        </Heading>
        <div className={styles.featuresGrid}>
          {FeatureList.map((feature, idx) => (
            <HexagonCard
              description={feature.description}
              icon={feature.icon}
              key={idx}
              title={feature.title}
              variant="feature"
            />
          ))}
        </div>
      </div>
    </section>
  );
}

function HomepageHeader() {
  const { siteConfig } = useDocusaurusContext();

  return (
    <header className={styles.heroBanner}>
      <div aria-hidden="true" className={styles.heroHoneycomb} />
      <div className="container">
        <div className={styles.heroInner}>
          <div className={styles.heroLogoWrap}>
            <img alt={siteConfig.title} className={styles.heroLogo} src="/img/logo.png" />
          </div>
          <div className={styles.heroText}>
            <Heading as="h1" className={styles.heroTitle}>
              Every stack comes with a{" "}
              <span className={styles.heroTitleAccent}>configuration tax</span>
            </Heading>
            <p className={styles.heroSubtitle}>
              ESLint. Prettier. Git hooks. You configure them once. Then again. Then every update
              breaks something different in each repo.{" "}
              <strong>datamitsu exists so you pay this tax only once.</strong>
            </p>
            <div className={styles.buttons}>
              <Link
                className="button button--primary button--lg"
                to="/docs/category/getting-started"
              >
                Get Started
              </Link>
              <Link className={`button button--lg ${styles.buttonSecondary}`} to="/docs/intro">
                Read the Docs
              </Link>
            </div>
          </div>
        </div>
      </div>
    </header>
  );
}

function PhilosophyCallout() {
  return (
    <section className={styles.philosophySection}>
      <div className="container">
        <div className={styles.philosophyContent}>
          <p className={styles.philosophyQuote}>
            <strong>Data</strong> — configuration, everything needed for your environment to work.
            <br />
            <strong>Mitsu</strong> — honey (蜜) and light (光) in Japanese.
          </p>
          <p className={styles.philosophyExplanation}>
            Sweetness from configuration no longer being pain.
            <br />
            Clarity from everything being in its place.
          </p>
        </div>
      </div>
    </section>
  );
}

function SolutionSection() {
  return (
    <section className={styles.solutionSection}>
      <div className="container">
        <Heading as="h2" className={styles.sectionHeading}>
          Pay once. Inherit everywhere.
        </Heading>
        <div className={styles.stepsGrid}>
          {SolutionSteps.map((step) => (
            <HexagonCard
              description={step.description}
              key={step.number}
              number={step.number}
              title={step.title}
              variant="step"
            />
          ))}
        </div>
      </div>
    </section>
  );
}

function TerminalDemoSection() {
  return (
    <section className={styles.terminalDemoSection}>
      <div className="container">
        <Heading as="h2" className={styles.sectionHeading}>
          Real output. Real project.
        </Heading>
        <p className={styles.terminalDemoSubheading}>
          This is actual <code>datamitsu check</code> output from{" "}
          <a href="https://github.com/ovineko/ovineko" rel="noopener noreferrer" target="_blank">
            ovineko/ovineko
          </a>{" "}
          — a TypeScript monorepo. Cold start downloads and verifies all tools. Subsequent runs use
          cached binaries.
        </p>
        <div className={styles.terminalDemoCenter}>
          <TerminalDemo />
        </div>
      </div>
    </section>
  );
}
