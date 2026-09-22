import { depthFade, orbit, orbitInk, spherePoint } from "./orbit.ts";

const sceneSettings = {
  camera: orbit.camera,
  featured: new Set([
    "bun",
    "cspell",
    "d2",
    "eslint",
    "gitleaks",
    "go",
    "knip",
    "mmdc",
    "node",
    "oxlint",
    "prettier",
    "ruff",
    "shfmt",
    "trivy",
    "tsc",
    "typst",
    "zensical",
  ]),
  radius: orbit.radius,
  rotationSpeed: 0.00009,
};

/**
 * @type {(name: string, x: number, y: number) => void}
 */
const ignoreHover = () => {};

export function createScene(
  canvas,
  labels,
  onSelect,
  onMotion,
  { autoRotate = true, centerX = 0.58, onHover = ignoreHover } = {},
) {
  const context = canvas.getContext("2d");
  if (!context) {
    throw new Error("Canvas rendering is unavailable");
  }
  const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
  const model = {
    active: true,
    angle: 0.25,
    frame: 0,
    height: 1,
    hovered: "",
    ink: orbitInk(""),
    lastTime: 0,
    layout: "sphere",
    moved: false,
    nodes: [],
    onScreen: true,
    pointer: null,
    projection: [],
    rotating: autoRotate && !reducedMotion.matches,
    selected: "",
    tilt: -0.12,
    visible: new Set(),
    width: 1,
    zoom: 1,
  };
  const events = new AbortController();
  const colors = {};
  const styles = getComputedStyle(document.documentElement);

  // How solid the points are drawn depends on what they are drawn on, so this
  // is re-read whenever the theme changes.
  function readInk() {
    model.ink = orbitInk(styles.getPropertyValue("--surface-base"));
  }

  readInk();

  function target(node, index, runtimes) {
    if (model.layout === "clusters") {
      const members = model.nodes.filter((item) => item.app.runtime === node.app.runtime);
      const angle = (runtimes.indexOf(node.app.runtime) / runtimes.length) * Math.PI * 2;
      const offset = point(
        members.indexOf(node),
        members.length,
        30 + Math.sqrt(members.length) * 8,
      );
      return {
        x: Math.cos(angle) * 225 + offset.x,
        y: Math.sin(angle) * 205 + offset.y,
        z: offset.z,
      };
    }
    if (model.layout === "helix") {
      const angle = index * 0.35;
      return {
        x: Math.cos(angle) * 170,
        y: (index / Math.max(1, model.nodes.length - 1) - 0.5) * 510,
        z: Math.sin(angle) * 170,
      };
    }
    return point(index, model.nodes.length);
  }

  function targets() {
    const runtimes = [...new Set(model.nodes.map((node) => node.app.runtime))];
    for (const [index, node] of model.nodes.entries()) {
      node.target = target(node, index, runtimes);
      if (!node.position || reducedMotion.matches) {
        node.position = { ...node.target };
      }
    }
  }

  function project(position) {
    const x = position.x * Math.cos(model.angle) + position.z * Math.sin(model.angle);
    const z = -position.x * Math.sin(model.angle) + position.z * Math.cos(model.angle);
    const y = position.y * Math.cos(model.tilt) - z * Math.sin(model.tilt);
    const depth = position.y * Math.sin(model.tilt) + z * Math.cos(model.tilt);
    const perspective = sceneSettings.camera / (sceneSettings.camera + depth);
    const isMobile = model.width < 700;
    // The sphere and its outer ring fit the frame the host chose: an embed is
    // as tall as the page embedding it decided, never a fixed height.
    const scale =
      Math.min(model.width / (isMobile ? 650 : 1200), model.height / 640, 1.25) * model.zoom;
    return {
      depth,
      scale: perspective * scale,
      x: model.width * (isMobile ? 0.5 : centerX) + x * perspective * scale,
      y: model.height * (isMobile ? 0.47 : 0.49) + y * perspective * scale,
    };
  }

  function line(from, to, color, opacity, width = 1) {
    context.globalAlpha = opacity;
    context.strokeStyle = color;
    context.lineWidth = width;
    const path = new Path2D();
    path.moveTo(from.x, from.y);
    path.lineTo(to.x, to.y);
    context.stroke(path);
  }

  function draw(time = 0) {
    model.frame = 0;
    if (!model.active || !model.onScreen || document.hidden) {
      return;
    }
    const delta = Math.min(time - (model.lastTime || time), 40);
    model.lastTime = time;
    if (model.rotating && !model.pointer && !model.hovered) {
      model.angle += delta * sceneSettings.rotationSpeed;
    }
    context.clearRect(0, 0, model.width, model.height);
    const origin = project({ x: 0, y: 0, z: 0 });
    const canvasStyles = getComputedStyle(canvas);
    const gold = canvasStyles.getPropertyValue("--accent-glow").trim();
    // The rings and the halo are one color that carries its own opacity, so a
    // theme can make them read on cream without the canvas deciding anything.
    const guide = canvasStyles.getPropertyValue("--line-orbit").trim();
    const glow = context.createRadialGradient(
      origin.x,
      origin.y,
      0,
      origin.x,
      origin.y,
      370 * origin.scale,
    );
    glow.addColorStop(0, `${gold}29`);
    glow.addColorStop(0.55, `${gold}14`);
    glow.addColorStop(1, `${gold}00`);
    context.globalAlpha = 1;
    context.fillStyle = glow;
    context.fillRect(0, 0, model.width, model.height);
    for (let ring = 0; ring < 3; ring++) {
      let previous;
      for (let step = 0; step <= 100; step++) {
        const angle = (step / 100) * Math.PI * 2;
        const radius = 290 + ring * 24;
        const position = {
          x: Math.cos(angle) * radius,
          y: Math.sin(angle) * radius * (ring === 1 ? 0.8 : 0.2),
          z: Math.sin(angle) * radius * (ring === 1 ? 0.2 : 0.8),
        };
        const current = project(position);
        if (previous) {
          line(previous, current, guide, 1);
        }
        previous = current;
      }
    }
    let isMoving = false;
    model.projection = model.nodes
      .map((node) => {
        for (const axis of ["x", "y", "z"]) {
          const distance = node.target[axis] - node.position[axis];
          if (Math.abs(distance) > 0.1) {
            isMoving = true;
          }
          node.position[axis] += distance * Math.min(delta / 120, 1);
        }
        return { ...project(node.position), node };
      })
      .sort((a, b) => b.depth - a.depth);
    const selected = model.nodes.find(
      (node) => node.app.name === (model.hovered || model.selected),
    );
    const active = model.projection.filter(({ node }) => model.visible.has(node.app.name));
    for (const item of model.projection) {
      const { node } = item;
      const visible = model.visible.has(node.app.name);
      const connected = visible && selected && node.app.runtime === selected.app.runtime;
      const color = colors[node.app.runtime];
      if (connected) {
        line(origin, item, color, model.ink.spoke);
        if (model.rotating) {
          const travel = (time / 3500 + model.nodes.indexOf(node) * 0.17) % 1;
          context.globalAlpha = 0.6;
          context.fillStyle = color;
          context.beginPath();
          context.arc(
            origin.x + (item.x - origin.x) * travel,
            origin.y + (item.y - origin.y) * travel,
            1.3,
            0,
            Math.PI * 2,
          );
          context.fill();
        }
      }
      const isSelected = node.app.name === model.selected;
      context.globalAlpha = visible ? depthFade(item.depth, model.ink.floor) : model.ink.filtered;
      context.shadowColor = color;
      context.shadowBlur = model.ink.glow ? (isSelected ? 24 : 9) : 0;
      context.fillStyle = color;
      context.beginPath();
      context.arc(item.x, item.y, Math.max(1, (isSelected ? 6 : 3) * item.scale), 0, Math.PI * 2);
      context.fill();
      context.shadowBlur = 0;
      if (isSelected && visible) {
        context.strokeStyle = color;
        context.lineWidth = 1;
        context.beginPath();
        context.arc(item.x, item.y, 12 * item.scale, 0, Math.PI * 2);
        context.stroke();
      }
      const showLabel =
        visible &&
        (isSelected ||
          node.app.name === model.hovered ||
          active.length < 15 ||
          (sceneSettings.featured.has(node.app.name) && item.depth < 90));
      node.label.hidden = !showLabel;
      if (showLabel) {
        node.label.style.transform = `translate(${item.x + 12}px, ${item.y - 12}px)`;
        node.label.style.opacity = isSelected
          ? "1"
          : String(Math.max(model.ink.floor, (500 - item.depth) / 650));
        node.label.classList.toggle("selected", isSelected);
      }
    }
    context.globalAlpha = 1;
    context.shadowColor = gold;
    context.shadowBlur = model.ink.glow ? 35 : 0;
    context.fillStyle = styles.getPropertyValue("--accent-base").trim();
    context.beginPath();
    context.arc(origin.x, origin.y, 12 * origin.scale, 0, Math.PI * 2);
    context.fill();
    context.shadowBlur = 0;
    context.strokeStyle = guide;
    context.beginPath();
    context.arc(origin.x, origin.y, 27 * origin.scale, 0, Math.PI * 2);
    context.stroke();
    if (isMoving || model.rotating) {
      wake();
    }
  }

  function wake() {
    if (!model.frame && model.active && model.onScreen && !document.hidden) {
      model.frame = requestAnimationFrame(draw);
    }
  }

  function resize() {
    const bounds = canvas.getBoundingClientRect();
    model.width = bounds.width;
    model.height = bounds.height;
    const ratio = Math.min(devicePixelRatio || 1, 2);
    canvas.width = Math.round(bounds.width * ratio);
    canvas.height = Math.round(bounds.height * ratio);
    context.setTransform(ratio, 0, 0, ratio, 0, 0);
    wake();
  }

  function setHover(name) {
    if (model.hovered === name) {
      return;
    }
    model.hovered = name;
    const item = model.projection.find((entry) => entry.node.app.name === name);
    onHover(name, item?.x ?? 0, item?.y ?? 0);
    wake();
  }

  function hit(event) {
    const bounds = canvas.getBoundingClientRect();
    return model.projection.findLast(
      (item) =>
        model.visible.has(item.node.app.name) &&
        Math.hypot(item.x - event.clientX + bounds.left, item.y - event.clientY + bounds.top) < 15,
    );
  }

  canvas.addEventListener(
    "pointerdown",
    (event) => {
      model.pointer = { x: event.clientX, y: event.clientY };
      model.moved = false;
      canvas.setPointerCapture(event.pointerId);
    },
    { signal: events.signal },
  );
  canvas.addEventListener(
    "pointermove",
    (event) => {
      if (model.pointer) {
        const dx = event.clientX - model.pointer.x;
        const dy = event.clientY - model.pointer.y;
        model.moved ||= Math.abs(dx) + Math.abs(dy) > 2;
        model.angle += dx * 0.006;
        model.tilt = Math.max(-1.2, Math.min(1.2, model.tilt + dy * 0.004));
        model.pointer = { x: event.clientX, y: event.clientY };
      } else {
        setHover(hit(event)?.node.app.name ?? "");
      }
      canvas.style.cursor = model.pointer ? "grabbing" : model.hovered ? "pointer" : "grab";
      wake();
    },
    { signal: events.signal },
  );
  canvas.addEventListener(
    "pointerup",
    (event) => {
      if (!model.moved) {
        const item = hit(event);
        if (item) {
          onSelect(item.node.app.name);
        }
      }
      model.pointer = null;
      wake();
    },
    { signal: events.signal },
  );
  canvas.addEventListener(
    "pointercancel",
    () => {
      model.pointer = null;
    },
    { signal: events.signal },
  );
  canvas.addEventListener(
    "pointerleave",
    () => {
      setHover("");
    },
    { signal: events.signal },
  );
  document.addEventListener(
    "visibilitychange",
    () => {
      model.lastTime = 0;
      wake();
    },
    { signal: events.signal },
  );
  reducedMotion.addEventListener(
    "change",
    () => {
      model.rotating = autoRotate && !reducedMotion.matches;
      onMotion(model.rotating);
      targets();
      wake();
    },
    { signal: events.signal },
  );
  const observer = new ResizeObserver(resize);
  observer.observe(canvas);
  const intersection = new IntersectionObserver(([entry]) => {
    model.onScreen = entry.isIntersecting;
    model.lastTime = 0;
    wake();
  });
  intersection.observe(canvas);

  return {
    destroy() {
      model.active = false;
      cancelAnimationFrame(model.frame);
      events.abort();
      observer.disconnect();
      intersection.disconnect();
      labels.replaceChildren();
    },
    isMoving() {
      return model.rotating;
    },
    layout(name) {
      model.layout = name;
      targets();
      wake();
    },
    motion() {
      model.rotating = !model.rotating;
      wake();
      return model.rotating;
    },
    reset() {
      model.angle = 0.25;
      model.tilt = -0.12;
      model.zoom = 1;
      wake();
    },
    setData(apps) {
      labels.replaceChildren();
      model.nodes = apps.map((app) => {
        colors[app.runtime] =
          styles.getPropertyValue(`--runtime-${app.runtime}`).trim() ||
          styles.getPropertyValue("--runtime-unknown").trim();
        const label = document.createElement("button");
        label.type = "button";
        label.className = "scene-label";
        label.textContent = app.name;
        label.style.setProperty("--runtime-color", colors[app.runtime]);
        label.addEventListener("click", () => onSelect(app.name));
        label.addEventListener("focus", () => {
          setHover(app.name);
        });
        label.addEventListener("blur", () => {
          setHover("");
        });
        labels.append(label);
        return { app, label, position: null, target: null };
      });
      targets();
      wake();
    },
    theme() {
      readInk();
      for (const node of model.nodes) {
        colors[node.app.runtime] =
          styles.getPropertyValue(`--runtime-${node.app.runtime}`).trim() ||
          styles.getPropertyValue("--runtime-unknown").trim();
        node.label.style.setProperty("--runtime-color", colors[node.app.runtime]);
      }
      wake();
    },
    update(apps, selected, active) {
      model.visible = new Set(apps.map((app) => app.name));
      model.selected = selected;
      model.active = active;
      for (const node of model.nodes) {
        node.label.setAttribute("aria-pressed", String(node.app.name === selected));
      }
      wake();
    },
    zoom(delta) {
      model.zoom = Math.max(0.65, Math.min(1.65, model.zoom + delta));
      wake();
    },
  };
}

function point(index, count, radius = sceneSettings.radius) {
  return spherePoint(index, count, radius);
}
