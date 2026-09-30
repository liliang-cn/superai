import { forwardRef, lazy, Suspense, useImperativeHandle, useRef, useState } from "react";
import HiveStage, { type Look as FlowLook } from "./HiveStage";
import HiveHex from "./HiveHex";
import type { StageHandle, StageProps } from "./hiveFx";

// The hive stage, in whichever look and dimension the viewer picked.
//
// Four looks — orbits, mycelium, a tree and the hexagon cells — each flat or in three
// dimensions. Both are
// the viewer's choice and are remembered in this browser. The three.js stage is
// loaded only when 3D is picked: it is most of a megabyte, and someone who never
// switches should not pay for it.
const HiveStage3D = lazy(() => import("./HiveStage3D"));
const HiveHex3D = lazy(() => import("./HiveHex3D"));

/** The two drawn-with-light looks, and the hexagon cells that came first. */
type Look = FlowLook | "hex";

const KEY = "superai-hive-stage";

type Dim = "2d" | "3d";
type Choice = { look: Look; dim: Dim };

const LOOKS: { key: Look; label: string }[] = [
  { key: "orbit", label: "轨道" },
  { key: "mycelium", label: "菌丝" },
  { key: "tree", label: "树" },
  { key: "hex", label: "六边形" },
];

// Asked once, for the whole page, and the context it makes is let go at once. A
// browser gives a page about sixteen WebGL contexts, and probing on every render
// spends them all.
let webglAnswer: boolean | null = null;
function webgl(): boolean {
  if (webglAnswer !== null) return webglAnswer;
  try {
    const c = document.createElement("canvas");
    const gl = (c.getContext("webgl2") || c.getContext("webgl")) as WebGLRenderingContext | null;
    webglAnswer = !!gl;
    gl?.getExtension("WEBGL_lose_context")?.loseContext();
  } catch {
    webglAnswer = false;
  }
  return webglAnswer;
}

function initial(can3d: boolean): Choice {
  const fallback: Choice = { look: "orbit", dim: can3d ? "3d" : "2d" };
  try {
    const v = JSON.parse(localStorage.getItem(KEY) || "null") as Partial<Choice> | null;
    if (!v || typeof v !== "object") return fallback;
    const look: Look = v.look === "mycelium" || v.look === "tree" || v.look === "hex" ? v.look : "orbit";
    const dim: Dim = v.dim === "3d" && can3d ? "3d" : v.dim === "2d" ? "2d" : fallback.dim;
    return { look, dim };
  } catch {
    // Storage unavailable, or the value from an older version: start fresh.
    return fallback;
  }
}

const HEIGHT_KEY = "superai-hive-stage-height";

function initialHeight(): number {
  try {
    const v = Number(localStorage.getItem(HEIGHT_KEY));
    return v >= 240 && v <= 1200 ? v : 520;
  } catch {
    return 520;
  }
}

const HiveStageView = forwardRef<StageHandle, StageProps>(function HiveStageView(props, ref) {
  // The stage's height, dragged by its bottom edge and kept in this browser.
  const [height, setHeight] = useState(initialHeight);
  const frame = useRef<HTMLDivElement>(null);
  const grab = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    const top = frame.current?.getBoundingClientRect().top;
    if (top === undefined) return;
    // Captured once: React clears currentTarget when the handler returns.
    const el = e.currentTarget;
    el.setPointerCapture(e.pointerId);
    let last = height;
    const move = (ev: PointerEvent) => {
      last = Math.round(Math.min(1200, Math.max(240, ev.clientY - top)));
      setHeight(last);
    };
    const up = (ev: PointerEvent) => {
      el.releasePointerCapture?.(ev.pointerId);
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", up);
      try {
        localStorage.setItem(HEIGHT_KEY, String(last));
      } catch {
        /* not worth failing over */
      }
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", up);
  };
  const can3d = webgl();
  const [choice, setChoice] = useState<Choice>(() => initial(can3d));
  const child = useRef<StageHandle>(null);
  useImperativeHandle(ref, () => ({ pulse: (p) => child.current?.pulse(p) }), []);

  const pick = (next: Partial<Choice>) => {
    const c = { ...choice, ...next };
    setChoice(c);
    try {
      localStorage.setItem(KEY, JSON.stringify(c));
    } catch {
      /* not worth failing over */
    }
  };

  return (
    <div className="hive-stage" ref={frame} style={{ height }}>
      {choice.dim === "3d" ? (
        <Suspense fallback={null}>
          {/* Keyed by look so a switch builds the new scene from scratch. */}
          {choice.look === "hex" ? (
            <HiveHex3D ref={child} {...props} />
          ) : (
            <HiveStage3D key={choice.look} ref={child} look={choice.look} {...props} />
          )}
        </Suspense>
      ) : (
        choice.look === "hex" ? (
          <HiveHex ref={child} {...props} />
        ) : (
          <HiveStage ref={child} look={choice.look} {...props} />
        )
      )}
      <div
        className="hive-stage-grip"
        role="separator"
        aria-orientation="horizontal"
        aria-label="Resize the hive stage"
        title="Drag to resize · double-click to reset"
        onPointerDown={grab}
        onDoubleClick={() => {
          setHeight(520);
          try {
            localStorage.removeItem(HEIGHT_KEY);
          } catch {
            /* not worth failing over */
          }
        }}
      />
      <div className="hive-stage-mode" role="group" aria-label="Stage view">
        {LOOKS.map((l) => (
          <button key={l.key} type="button" aria-pressed={choice.look === l.key} onClick={() => pick({ look: l.key })}>
            {l.label}
          </button>
        ))}
        {can3d ? (
          <>
            <span className="hive-stage-sep" aria-hidden="true" />
            {(["2d", "3d"] as const).map((d) => (
              <button key={d} type="button" aria-pressed={choice.dim === d} onClick={() => pick({ dim: d })}>
                {d.toUpperCase()}
              </button>
            ))}
          </>
        ) : null}
      </div>
    </div>
  );
});

export default HiveStageView;
