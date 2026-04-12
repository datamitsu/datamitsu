import type { ReactNode } from "react";

import HeadingLib from "@theme/Heading";
import clsx from "clsx";

import styles from "./HexagonCard.module.css";

const Heading = HeadingLib as React.FC<{
  as: "h1" | "h2" | "h3" | "h4" | "h5" | "h6";
  children: React.ReactNode;
  className?: string;
}>;

interface HexagonCardProps {
  description: string;
  icon?: ReactNode;
  number?: number;
  title: string;
  variant?: "feature" | "step";
}

export default function HexagonCard({
  description,
  icon,
  number,
  title,
  variant = "feature",
}: HexagonCardProps): ReactNode {
  return (
    <div className={clsx(styles.hexagonCard, styles[variant])}>
      <div className={styles.hexagonContent}>
        {number !== undefined && <div className={styles.stepNumber}>{number}</div>}
        {icon !== undefined && <div className={styles.iconWrapper}>{icon}</div>}
        <Heading as="h3" className={styles.title}>
          {title}
        </Heading>
        <p className={styles.description}>{description}</p>
      </div>
    </div>
  );
}
