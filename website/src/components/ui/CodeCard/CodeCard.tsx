import type { ReactNode } from "react";

import styles from "./CodeCard.module.css";

export interface CodeCardProps {
  children: ReactNode;
  className?: string;
  header?: ReactNode;
  theme?: "auto" | "dark" | "light";
}

export default function CodeCard({
  children,
  className = "",
  header,
  theme = "auto",
}: CodeCardProps): ReactNode {
  const themeClass =
    theme === "auto" ? "" : styles[`theme${theme.charAt(0).toUpperCase() + theme.slice(1)}`];

  return (
    <div className={`card ${styles.card} ${themeClass} ${className}`}>
      {header}
      <div className={styles.cardBody}>{children}</div>
    </div>
  );
}
