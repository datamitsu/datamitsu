import { type ReactNode, useCallback } from "react";

import styles from "./CodeCardTabs.module.css";

export interface CodeCardTab {
  active: boolean;
  id: string;
  label: string;
}

export interface CodeCardTabsProps {
  ariaLabel?: string;
  onKeyDown?: (e: React.KeyboardEvent, id: string) => void;
  onTabSelect: (id: string) => void;
  tabs: CodeCardTab[];
}

interface CodeCardTabButtonProps {
  active: boolean;
  id: string;
  label: string;
  onKeyDown?: (e: React.KeyboardEvent, id: string) => void;
  onSelect: (id: string) => void;
}

export default function CodeCardTabs({
  ariaLabel = "Tabs",
  onKeyDown,
  onTabSelect,
  tabs,
}: CodeCardTabsProps): ReactNode {
  return (
    <div aria-label={ariaLabel} className={styles.tabs} role="tablist">
      {tabs.map((tab) => (
        <CodeCardTabButton
          active={tab.active}
          id={tab.id}
          key={tab.id}
          label={tab.label}
          onKeyDown={onKeyDown}
          onSelect={onTabSelect}
        />
      ))}
    </div>
  );
}

function CodeCardTabButton({
  active,
  id,
  label,
  onKeyDown,
  onSelect,
}: CodeCardTabButtonProps): ReactNode {
  const handleClick = useCallback(() => onSelect(id), [id, onSelect]);
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => onKeyDown?.(e, id),
    [id, onKeyDown],
  );

  return (
    <button
      aria-controls={`panel-${id}`}
      aria-selected={active}
      className={`${styles.tab} ${active ? styles.tabActive : ""}`}
      id={`tab-${id}`}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      role="tab"
      tabIndex={active ? 0 : -1}
      type="button"
    >
      {label}
    </button>
  );
}
