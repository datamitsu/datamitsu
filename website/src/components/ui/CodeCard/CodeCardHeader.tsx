import type { ReactNode } from "react";

import styles from "./CodeCardHeader.module.css";

export interface CodeCardHeaderProps {
  centerTitle?: boolean;
  className?: string;
  leftContent?: ReactNode;
  rightContent?: ReactNode;
  theme?: "auto" | "dark" | "light";
  title?: string;
}

export default function CodeCardHeader({
  centerTitle = false,
  className = "",
  leftContent,
  rightContent,
  theme = "auto",
  title,
}: CodeCardHeaderProps): ReactNode {
  const baseClass = centerTitle ? styles.headerCenter : styles.header;
  const themeClass =
    theme === "auto" ? "" : styles[`header${theme.charAt(0).toUpperCase() + theme.slice(1)}`];
  const headerClass = `${baseClass} ${themeClass}`.trim();

  return (
    <div className={`${headerClass} ${className}`}>
      {leftContent && <div className={styles.headerLeft}>{leftContent}</div>}
      {title && <span className={styles.headerTitle}>{title}</span>}
      {rightContent && <div className={styles.headerRight}>{rightContent}</div>}
    </div>
  );
}
