/**
 * The orbit's geometry and its depth fade, shared by the canvas the inspector draws and the still
 * poster the documentation site shows until that canvas loads. One set of positions and one fade,
 * so the two never show different orbits.
 */

export const orbit = {
  angle: 0.25,
  camera: 850,
  radius: 240,
  tilt: -0.12,
} as const;

export interface OrbitInk {
  /**
   * How visible a point filtered out by the runtime strip stays.
   */
  filtered: number;
  /**
   * The palest a point in the sphere's far half is drawn.
   */
  floor: number;
  /**
   * Whether points and the center carry a glow.
   */
  glow: boolean;
  /**
   * How visible the spoke from the center to a point of the hovered runtime is.
   */
  spoke: number;
}

/**
 * How opaque a point at this depth is drawn: near the viewer it is solid, behind the sphere it
 * fades to `floor`.
 */
export function depthFade(depth: number, floor: number): number {
  return Math.max(floor, (500 - depth) / 800);
}

/**
 * The same, for a caller that knows its color mode without having a color to hand.
 */
export function inkFor(isLightMode: boolean): OrbitInk {
  return isLightMode
    ? { filtered: 0.14, floor: 0.78, glow: false, spoke: 0.34 }
    : { filtered: 0.045, floor: 0.3, glow: true, spoke: 0.12 };
}

/**
 * Whether a surface color is light enough to need the light-theme treatment. An unreadable or
 * missing color counts as dark, which is what the inspector's own default is.
 */
export function isLight(surface: string): boolean {
  const channels = rgb(surface);
  if (!channels) {
    return false;
  }
  const [red, green, blue] = channels.map((value) => {
    const part = value / 255;
    return part <= 0.03928 ? part / 12.92 : ((part + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue > 0.35;
}

/**
 * How the orbit is inked on the given surface. A faint point disappears on cream long before it
 * does on dark brown, and the glow that carries a dark-mode point is only haze on a light one — so
 * the light theme keeps its points nearly solid and drops the glow instead of tinting the same
 * colors paler. The rings and the halo are not here: they are one color each, `line.orbit` in the
 * theme file, which carries its own opacity.
 */
export function orbitInk(surface: string): OrbitInk {
  return inkFor(isLight(surface));
}

/**
 * The index-th of `count` points spread evenly over a sphere, by the Fibonacci spiral.
 */
export function spherePoint(
  index: number,
  count: number,
  radius: number = orbit.radius,
): { x: number; y: number; z: number } {
  const y = 1 - ((index + 0.5) * 2) / count;
  const turn = index * Math.PI * (3 - Math.sqrt(5));
  return {
    x: Math.cos(turn) * Math.sqrt(1 - y * y) * radius,
    y: y * radius,
    z: Math.sin(turn) * Math.sqrt(1 - y * y) * radius,
  };
}

function rgb(color: string): [number, number, number] | undefined {
  const digits = color.trim().replace("#", "");
  const full =
    digits.length === 3
      ? [...digits].map((digit) => `${digit}${digit}`).join("")
      : digits.slice(0, 6);
  if (!/^[\da-f]{6}$/i.test(full)) {
    return undefined;
  }
  return [0, 2, 4].map((offset) => Number.parseInt(full.slice(offset, offset + 2), 16)) as [
    number,
    number,
    number,
  ];
}
