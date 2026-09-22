import { useColorMode } from "@docusaurus/theme-common";

import { depthFade, inkFor, orbit, spherePoint } from "../../../../inspector/src/orbit";
import snapshot from "../../data/reference-config.json";

// The still that stands in until the live frame loads. Geometry, fade and
// palette all come from the orbit module and the theme tokens the frame itself
// reads, so the poster and the canvas that replaces it draw the same orbit.
const center = { x: 400, y: 300 };
const runtimes = new Set(["binary", "bun", "go", "jvm", "node", "python", "shell"]);

const dots = snapshot.manifest.apps
  .map((app, index) => {
    const point = place(spherePoint(index, snapshot.manifest.apps.length));
    return {
      depth: point.depth,
      runtime: runtimes.has(app.runtime) ? app.runtime : "unknown",
      scale: point.scale,
      x: point.x,
      y: point.y,
    };
  })
  .sort((a, b) => b.depth - a.depth);

const rings = [0, 1, 2].map((ring) =>
  Array.from({ length: 101 }, (_, step) => {
    const stepAngle = (step / 100) * Math.PI * 2;
    const ringRadius = 290 + ring * 24;
    const flat = ring === 1 ? 0.8 : 0.2;
    return place({
      x: Math.cos(stepAngle) * ringRadius,
      y: Math.sin(stepAngle) * ringRadius * flat,
      z: Math.sin(stepAngle) * ringRadius * (ring === 1 ? 0.2 : 0.8),
    });
  })
    .map((point) => `${point.x.toFixed(1)},${point.y.toFixed(1)}`)
    .join(" "),
);

export default function OrbitPoster({ className }: { className?: string }) {
  const { colorMode } = useColorMode();
  const ink = inkFor(colorMode === "light");
  return (
    <svg
      aria-hidden="true"
      className={className}
      preserveAspectRatio="xMidYMid meet"
      viewBox="0 0 800 600"
    >
      {rings.map((points) => (
        <polyline
          fill="none"
          key={points}
          points={points}
          stroke="var(--line-orbit)"
          strokeWidth="1"
        />
      ))}
      {dots.map((dot, index) => (
        <circle
          cx={dot.x}
          cy={dot.y}
          fill={`var(--runtime-${dot.runtime})`}
          key={index}
          opacity={depthFade(dot.depth, ink.floor)}
          r={Math.max(1, 3 * dot.scale)}
        />
      ))}
      <circle cx={center.x} cy={center.y} fill="var(--accent-base)" r="12" />
      <circle
        cx={center.x}
        cy={center.y}
        fill="none"
        r="27"
        stroke="var(--line-orbit)"
        strokeWidth="1"
      />
    </svg>
  );
}

function place(position: { x: number; y: number; z: number }) {
  const x = position.x * Math.cos(orbit.angle) + position.z * Math.sin(orbit.angle);
  const z = -position.x * Math.sin(orbit.angle) + position.z * Math.cos(orbit.angle);
  const y = position.y * Math.cos(orbit.tilt) - z * Math.sin(orbit.tilt);
  const depth = position.y * Math.sin(orbit.tilt) + z * Math.cos(orbit.tilt);
  const scale = orbit.camera / (orbit.camera + depth);
  return { depth, scale, x: center.x + x * scale, y: center.y + y * scale };
}
