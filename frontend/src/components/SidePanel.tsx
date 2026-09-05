import React, { useEffect, useRef, useState } from "react";
import {
  ActivityIcon,
  AtSignIcon,
  LayoutDashboardIcon,
  PanelRightCloseIcon,
  ScanEyeIcon,
  WrenchIcon,
} from "lucide-react";
import { AskSummary, TraceItem } from "../lib/types";
import { TraceBody } from "./TracePanel";
import DashboardsPanel from "./DashboardsPanel";
import { PromptPreviewBody } from "./PromptPreviewPanel";
import { ActivityBody } from "./ActivityPanel";
import { useRoom } from "../lib/useViewport";
import { AgentsBody } from "./AgentsPanel";

/**
 * The rail down the right of a conversation, and whichever panel it is showing.
 *
 * It used to be the tool trace and nothing else, so the trace owned the rail,
 * the collapse and the remembering. Now the saved dashboards live there too and
 * that shell belongs to neither of them — a panel should know what it draws and
 * not whether it is the one on screen.
 *
 * Collapsed, the rail is a column of icons rather than a single toggle: what a
 * click opens is now a choice, and a rail that opened "whatever was last shown"
 * would make the trace icon sometimes mean dashboards.
 */

type Tab = "trace" | "dashboards" | "preview" | "activity" | "agents";

const OPEN_KEY = "superai-trace-open";
const TAB_KEY = "superai-side-tab";
const WIDTH_KEY = "superai-side-width";

/** How wide the panel may be dragged.
 *
 *  The floor is the width it has always had, and the ceiling leaves enough of
 *  the conversation visible to still be a conversation — a panel that can eat
 *  the whole window is a worse full screen than the real one, which a dashboard
 *  has its own button for. */
const MIN_WIDTH = 300;
const MAX_WIDTH = 900;
const DEFAULT_WIDTH = 330;

const TABS: { key: Tab; label: string; Icon: typeof WrenchIcon }[] = [
  { key: "trace", label: "Tool trace", Icon: WrenchIcon },
  { key: "dashboards", label: "Dashboards", Icon: LayoutDashboardIcon },
  // What the model is about to be told. It was a modal over the whole window,
  // which covers the message you are editing — the one thing you want in front
  // of you while reading the turn it produces.
  { key: "preview", label: "Prompt preview", Icon: ScanEyeIcon },
  // The Stats page's live stream. The question it answers — what is it doing
  // right now — comes up while a turn is running, which is when you are here
  // and not there.
  { key: "activity", label: "Activity", Icon: ActivityIcon },
  // Who else can be asked. The @ menu in the composer only helps someone who
  // already knows a name to type; this is where the names come from.
  { key: "agents", label: "Agents", Icon: AtSignIcon },
];

function read(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

/** Keeps a dragged width inside what the layout can actually hold. */
function clamp(px: number): number {
  return Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, Math.round(px)));
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // A private window. The panel simply opens on its default next time.
  }
}

export default function SidePanel({
  trace,
  asks = [],
  sessionId = "",
  draft = "",
  onMention,
}: {
  trace: TraceItem[];
  asks?: AskSummary[];
  /** The conversation and the message being written, for the preview tab. */
  sessionId?: string;
  draft?: string;
  /** Put an agent's name in the composer. The panel picks who; the person
   *  still writes what and presses send. */
  onMention?: (name: string) => void;
}) {
  // The key is the one the trace panel has always used, so an existing install
  // opens the way it was left rather than resetting because the code moved.
  const [open, setOpen] = useState(() => read(OPEN_KEY, "1") !== "0");
  // In a narrow window the panel cannot have a column of its own: 232px of
  // sidebar plus 330 of panel left the conversation about 300 wide, with the
  // message box narrower than its own placeholder. So it starts on its rail
  // and, if opened, floats over the conversation rather than beside it —
  // which is the same answer the sidebar already gives on a phone.
  const room = useRoom();
  const floats = room !== "wide";
  const [peeked, setPeeked] = useState(false);
  const shown = floats ? peeked : open;
  // Every tab reopens where it was left except the preview, which assembles a
  // whole turn — persona, recalled memory, the tool catalogue — the moment it
  // mounts. That is a thing to ask for, not a thing to walk back into.
  const [tab, setTab] = useState<Tab>(() => {
    const saved = read(TAB_KEY, "trace") as Tab;
    return saved !== "preview" && TABS.some((t) => t.key === saved) ? saved : "trace";
  });
  const [width, setWidth] = useState(() => {
    const saved = Number(read(WIDTH_KEY, ""));
    return saved >= MIN_WIDTH && saved <= MAX_WIDTH ? saved : DEFAULT_WIDTH;
  });

  useEffect(() => write(OPEN_KEY, open ? "1" : "0"), [open]);
  useEffect(() => write(TAB_KEY, tab), [tab]);

  // The draft, settled. Assembling a turn is a round trip through the persona,
  // the recalled memory and the whole tool catalogue; per keystroke would put
  // the backend under a load this does not justify.
  const [settledDraft, setSettledDraft] = useState(draft);
  useEffect(() => {
    const t = setTimeout(() => setSettledDraft(draft), 500);
    return () => clearTimeout(t);
  }, [draft]);

  /**
   * Dragging the left edge.
   *
   * Pointer capture rather than window listeners: the pointer leaves the 5px
   * handle on the first millimetre of any real drag, and without capture the
   * events stop arriving the moment it does — the panel would jump once and
   * then stick. Capture also ends the drag correctly when the button is
   * released outside the window.
   *
   * The width is written to the element during the drag and to state only at
   * the end: resizing re-renders a live tool trace or a chart, and doing that
   * on every pointer move is what makes a drag feel like it is fighting back.
   */
  const panelRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<number | null>(null);

  const onResizeStart = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    dragRef.current = width;
  };

  const onResizeMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (dragRef.current === null || !panelRef.current) return;
    // Measured from the right edge of the window, which is where the panel is
    // anchored, so the handle stays under the pointer whatever the layout does.
    const next = clamp(window.innerWidth - e.clientX);
    dragRef.current = next;
    panelRef.current.style.width = `${next}px`;
  };

  const onResizeEnd = (e: React.PointerEvent<HTMLDivElement>) => {
    if (dragRef.current === null) return;
    e.currentTarget.releasePointerCapture(e.pointerId);
    const next = dragRef.current;
    dragRef.current = null;
    setWidth(next);
    write(WIDTH_KEY, String(next));
  };

  // Room came back while it was floating: hand it its column again and forget
  // the peek, or it would sit over a conversation that now has space for both.
  useEffect(() => {
    if (!floats) setPeeked(false);
  }, [floats]);

  if (!shown) {
    return (
      <div className="trace-panel collapsed">
        <div className="panel-rail">
          {TABS.map(({ key, label, Icon }) => (
            <button
              key={key}
              type="button"
              className="panel-toggle"
              onClick={() => {
                setTab(key);
                // A window too narrow for a column still opens on request; it
                // just floats. Not written to the preference, so dragging the
                // window wide again restores whatever was there before.
                if (floats) setPeeked(true);
                else setOpen(true);
              }}
              title={`Show ${label.toLowerCase()}`}
              aria-label={`Show ${label.toLowerCase()}`}
            >
              <Icon className="size-4" />
              {/* The count keeps activity from being silent while hidden. Only
                the trace has one worth watching go up mid-turn. */}
              {key === "trace" && trace.length > 0 && (
                <span className="panel-badge">{trace.length}</span>
              )}
            </button>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className={`trace-panel${floats ? " floating" : ""}`} ref={panelRef} style={{ width }} data-pet-spot="panel" data-pet-label="the panel down the right of the conversation">
      {/* The drag handle sits on the border, outside the scrolling content, so
          grabbing it never scrolls whatever is underneath. */}
      <div
        className="panel-resizer"
        onPointerDown={onResizeStart}
        onPointerMove={onResizeMove}
        onPointerUp={onResizeEnd}
        onPointerCancel={onResizeEnd}
        onDoubleClick={() => {
          setWidth(DEFAULT_WIDTH);
          write(WIDTH_KEY, String(DEFAULT_WIDTH));
        }}
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize panel (double-click to reset)"
        title="Drag to resize · double-click to reset"
      />
      <div className="panel-tabs">
        {TABS.map(({ key, label, Icon }) => (
          <button
            key={key}
            type="button"
            className={`panel-tab${tab === key ? " active" : ""}`}
            data-pet-spot={`tab-${key}`}
            data-pet-label={`the ${label.toLowerCase()} tab of the right-hand panel`}
            onClick={() => setTab(key)}
            title={label}
            aria-label={label}
            aria-selected={tab === key}
          >
            <Icon className="size-4" />
          </button>
        ))}
        <span style={{ flex: 1 }} />
        <button
          type="button"
          className="panel-toggle inline"
          onClick={() => (floats ? setPeeked(false) : setOpen(false))}
          title="Hide panel"
          aria-label="Hide panel"
        >
          <PanelRightCloseIcon className="size-4" />
        </button>
      </div>
      {tab === "trace" && <TraceBody trace={trace} asks={asks} />}
      {tab === "dashboards" && <DashboardsPanel />}
      {/* Mounted only while it is the visible tab, so nothing is assembled when
          nobody is looking at it. */}
      {tab === "preview" && <PromptPreviewBody sessionId={sessionId} goal={settledDraft} />}
      {/* Same rule: the meter is subscribed to only while it is being read. */}
      {tab === "activity" && <ActivityBody />}
      {tab === "agents" && <AgentsBody onMention={onMention} />}
    </div>
  );
}
