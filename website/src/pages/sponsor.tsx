import type { ReactNode } from "react";

import Layout from "@theme/Layout";

const styles: Record<string, React.CSSProperties> = {
  container: {
    alignItems: "center",
    display: "flex",
    flexDirection: "column",
    justifyContent: "center",
    minHeight: "50vh",
    padding: "4rem 2rem",
    textAlign: "center",
  },
  heading: {
    fontSize: "2.5rem",
    marginBottom: "1rem",
  },
  message: {
    color: "var(--ifm-color-emphasis-700)",
    fontSize: "1.25rem",
  },
};

export default function Sponsor(): ReactNode {
  return (
    <Layout description="Support datamitsu development" title="Sponsor datamitsu">
      <main style={styles.container}>
        <h1 style={styles.heading}>Sponsor datamitsu</h1>
        <p style={styles.message}>Coming soon!</p>
        <p style={styles.message}>Check back soon for ways to support datamitsu development.</p>
      </main>
    </Layout>
  );
}
