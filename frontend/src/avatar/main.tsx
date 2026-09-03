// The avatar page: a character that reacts to the agent, and the agent's reply
// underneath it.
//
// It used to be a hand-written HTML string inside backend/avatar.go, and the
// reply was set with `textContent`. That printed exactly what the model wrote —
// including the markdown, so a reply came out as "- **天气**: 晴转多云" with the
// asterisks in it, and a ```chart block arrived as a wall of JSON. The chat
// window has rendered this properly all along, through AIGUI; this page now
// uses the same renderer, so the same reply reads the same in both places.
//
// It is a separate Vite entry rather than a route inside the app: an external
// renderer is pointed at this page by URL on the avatar port, which serves it
// with no session and no bindings. Vite splits the vendor chunks the two
// entries share, so the second entry costs the page's own code and nothing it
// has in common with the app.
import React, { useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { AIRenderer } from "@ai-gui/react";
import { CardRegistry } from "@ai-gui/core";
import { primitives } from "@ai-gui/plugin-primitives";
import { katex } from "@ai-gui/plugin-katex";
import { mermaid } from "@ai-gui/plugin-mermaid";
import { chart } from "@ai-gui/plugin-chart";
import { citation } from "@ai-gui/plugin-citation";
import { bigscreen } from "@ai-gui/plugin-bigscreen";
import { normalizeOneLineBlocks } from "../lib/blocks";
import "./avatar.css";

/** The sheets that ship. */
const CHARS = ["cat", "bunny", "robot"];

/**
 * The sheet's rows, in the order gen-avatars.py wrote them.
 *
 * A name that is not in this list leaves the row where it is rather than
 * falling back to row 0: the mood tag is optional and free-form enough that a
 * model will eventually invent one, and snapping the face to neutral every time
 * it does would look like the avatar had broken.
 */
const ROWS: Record<string, number> = {
  neutral: 0, happy: 1, sad: 2, thinking: 3, excited: 4,
  sleepy: 5, confused: 6, love: 7, angry: 8, surprised: 9,
};

/** 144px per row at 6x. */
const ROW_HEIGHT = 144;

const STORAGE_KEY = "superai-avatar-char";

type AvatarEvent = {
  type: "state" | "emotion" | "speech";
  state?: string;
  emotion?: string;
  text?: string;
};

/**
 * What this page may draw.
 *
 * Deliberately not the app's plugin list. `ui` is missing because its actions
 * are App methods reached over the Wails bridge, and this page is served by the
 * avatar server on its own port with no bindings and no session — a button that
 * cannot run is worse than a block that was never offered, and the plugin
 * invalidates a whole block whose action is unregistered anyway. Everything
 * else here is presentation and needs nothing from the host.
 *
 * Built once, outside the component: rebuilding the array on every render would
 * tear down and remount every diagram and chart mid-stream.
 */
const PLUGINS = [
  primitives(),                 // list / table / key-value / layout
  katex(),                      // $…$ and $$…$$
  mermaid(),                    // ```mermaid diagrams
  chart({ interactive: true }), // ```chart ECharts options
  citation(),                   // ```sources
  bigscreen(),                  // ```bigscreen KPI / gauge / rank walls
];

/** No app cards: this page renders replies, and cards are the app's own UI. */
const REGISTRY = new CardRegistry();

function readSavedChar(): string {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved && CHARS.includes(saved)) return saved;
  } catch {
    // A private window, or site data switched off. The default is fine.
  }
  return CHARS[0];
}

function Avatar() {
  const [character, setCharacter] = useState(readSavedChar);
  const [state, setState] = useState("idle");
  const [emotion, setEmotion] = useState("neutral");
  const [speech, setSpeech] = useState("");
  // Held separately from `emotion` so an unknown mood leaves the face where it
  // is while the tag still reports what actually arrived.
  const [row, setRow] = useState(0);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, character);
    } catch {
      // Not worth telling anyone about: the choice simply will not persist.
    }
  }, [character]);

  useEffect(() => {
    const es = new EventSource("/avatar/events");
    es.onmessage = (e) => {
      let ev: AvatarEvent;
      try {
        ev = JSON.parse(e.data);
      } catch {
        return;
      }
      if (ev.type === "state") {
        setState(ev.state || "idle");
      } else if (ev.type === "emotion") {
        const name = ev.emotion || "neutral";
        setEmotion(name);
        if (ROWS[name] !== undefined) setRow(ROWS[name]);
      } else if (ev.type === "speech") {
        setSpeech(ev.text || "");
      }
    };
    es.onerror = () => setState("disconnected");
    return () => es.close();
  }, []);

  const spriteStyle = useMemo(
    () => ({
      backgroundImage: `url("/avatar/sprites/${character}.png")`,
      // Negative because the window moves down the sheet.
      backgroundPositionY: `${-row * ROW_HEIGHT}px`,
    }),
    [character, row],
  );

  return (
    <>
      <div id="stage" className={state}>
        <div id="glow" />
        <div id="sprite" style={spriteStyle} />
      </div>
      <div className="tags">
        <span className="tag">state: {state}</span>
        <span className="tag">emotion: {emotion}</span>
      </div>
      <Speech text={speech} />
      <div id="pick">
        {CHARS.map((name) => (
          <button
            key={name}
            className={name === character ? "on" : ""}
            onClick={() => setCharacter(name)}
          >
            {name}
          </button>
        ))}
      </div>
    </>
  );
}

/**
 * The reply, rendered.
 *
 * AIRenderer is controlled by `text` and pushes only the delta when the string
 * grows, which is what a streamed reply looks like. A speech event today
 * carries a whole finished reply, so the delta path is not exercised — but
 * handing it the full string on every update is also what makes replacing one
 * reply with the next work, and it costs nothing to be right about both.
 *
 * The text is repaired on the way in for the same reason the chat window
 * repairs it: models regularly write a whole block on one line, which is not a
 * fenced block in CommonMark and renders as raw JSON mid-sentence.
 */
function Speech({ text }: { text: string }) {
  if (!text.trim()) return null;
  return (
    <div className="speech">
      <AIRenderer
        text={normalizeOneLineBlocks(text)}
        registry={REGISTRY}
        plugins={PLUGINS}
        theme="dark"
        locale="zh-CN"
      />
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <Avatar />
  </React.StrictMode>,
);
