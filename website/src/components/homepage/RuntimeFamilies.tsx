import { runtimeColor, runtimeNames } from "../../../../inspector/src/model";
import { showcaseTools } from "../../data/landing";
import snapshot from "../../data/reference-config.json";
import styles from "./RuntimeFamilies.module.css";

// The runtime families the reference configuration manages, with a few of the
// tools each one carries. The families, their sizes and every name here come
// from the snapshot the orbit above draws; the only editorial input is which
// names are worth showing (data/landing.ts), because a reader deciding whether
// this fits their stack has to recognise what they are looking at. Among those,
// the ones the configuration's own tool definitions reach for most often come
// first, ties broken by name, so the row is the same on every build.
const manifest = snapshot.manifest;
const examplesShown = 3;
// Shell apps resolve through the host PATH instead of being installed, so a name
// here would claim something the configuration does not do.
const systemResolved = "shell";

const uses = new Map<string, number>();
for (const tool of manifest.tools) {
  for (const operation of tool.operations) {
    uses.set(operation.app, (uses.get(operation.app) ?? 0) + 1);
  }
}

const families = [...new Set(manifest.apps.map((app) => app.runtime))]
  .map((runtime) => {
    const apps = manifest.apps.filter((app) => app.runtime === runtime);
    return {
      examples:
        runtime === systemResolved
          ? []
          : apps
              .map((app) => app.name)
              .filter((name) => showcaseTools.includes(name))
              .sort((a, b) => (uses.get(b) ?? 0) - (uses.get(a) ?? 0) || a.localeCompare(b))
              .slice(0, examplesShown),
      label: runtimeNames[runtime] ?? runtime,
      runtime,
      size: apps.length,
    };
  })
  .sort((a, b) => b.size - a.size || a.label.localeCompare(b.label));

export default function RuntimeFamilies() {
  return (
    <ul className={styles.families}>
      {families.map((family) => (
        <li className={styles.family} key={family.runtime}>
          <p className={styles.familyName}>
            <i aria-hidden="true" style={{ background: runtimeColor(family.runtime) }} />
            {family.label}
            <span>{family.size === 1 ? "1 app" : `${family.size} apps`}</span>
          </p>
          <ul className={styles.tools}>
            {family.examples.map((name) => (
              <li key={name}>{name}</li>
            ))}
          </ul>
        </li>
      ))}
    </ul>
  );
}
