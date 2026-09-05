import React from "react";
import { PanelLeftCloseIcon, PanelLeftOpenIcon } from "lucide-react";
import { ViewKey } from "../lib/types";
import { useRoom } from "../lib/useViewport";

const NAV: { section: string; items: { key: ViewKey; label: string; icon: string }[] }[] = [
  {
    section: "Workspace",
    items: [
      { key: "chat", label: "Chat", icon: "💬" },
      { key: "stats", label: "Stats", icon: "📊" },
    ],
  },
  {
    section: "Knowledge",
    items: [
      // Next to Memory on purpose: it is the same store, seen whole instead of
      // one recall at a time.
      { key: "knowledge", label: "Knowledge", icon: "🧠" },
      { key: "skills", label: "Skills", icon: "🧩" },
      { key: "mcp", label: "MCP", icon: "🔌" },
      { key: "records", label: "Records", icon: "🗂️" },
    ],
  },
  {
    section: "System",
    items: [
      { key: "settings", label: "Settings", icon: "⚙️" },
    ],
  },
];

export default function Sidebar({
  current,
  onNavigate,
  badges,
  open,
  onToggle,
}: {
  current: ViewKey;
  onNavigate: (v: ViewKey) => void;
  /** Counts of things that happened on their own, per view. */
  badges?: Partial<Record<ViewKey, number>>;
  /** Expanded on a desktop; on a phone this is the drawer being out. The state
   *  lives in App because on a narrow screen the button that opens it cannot be
   *  in here — a closed drawer has translated itself off the screen, and its own
   *  toggle with it. */
  open: boolean;
  onToggle: () => void;
}) {
  // "Collapsed" means two different things depending on how much room there is,
  // and the component used to only know the desktop one. On a desktop it is the
  // 60px icon rail: navigation stays one click away, it just stops spending
  // width on labels. On a phone the very same flag means the drawer is shut —
  // and a shut drawer is off the screen entirely, so there is no width to save.
  // The stylesheet had already noticed this and put the label spacing back
  // under its 640px query, but CSS can only lay out text that was rendered, and
  // this tree rendered none: the drawer slid out 232px wide showing a column of
  // seven emoji and no words. So the room decides, not the flag.
  const room = useRoom();
  const labels = open || room === "phone";

  return (
    <>
      {/* The scrim behind the drawer. A real element rather than the sidebar's
          own ::before, because a pseudo-element cannot take a click — made that
          way it looks dismissable and is not, and the only way out of the
          drawer is a small button at the bottom of it. Display:none above the
          drawer breakpoint. */}
      {open && <div className="sidebar-scrim" onClick={onToggle} aria-hidden="true" />}
      <aside className={`sidebar${open ? "" : " collapsed"}`}>
      <div className="brand">
        <div className="brand-logo">S</div>
        {labels && (
          <div className="brand-text">
            <div className="brand-name">SuperAI</div>
            <div className="brand-sub">Desktop</div>
          </div>
        )}
      </div>
      <nav className="nav">
        {NAV.map((group) => (
          <div key={group.section}>
            {labels && <div className="nav-section">{group.section}</div>}
            {group.items.map((it) => {
              const badge = badges?.[it.key] ?? 0;
              return (
                <button
                  key={it.key}
                  className={`nav-item${current === it.key ? " active" : ""}`}
                  data-pet-spot={`nav-${it.key}`}
                  data-pet-label={`the ${it.label} link in the left sidebar`}
                  onClick={() => onNavigate(it.key)}
                  title={labels ? undefined : it.label}
                >
                  <span className="ic">{it.icon}</span>
                  {labels && it.label}
                  {/* Collapsed there is no room for a count, but "something
                      happened" still has to be visible. */}
                  {badge > 0 && (
                    <span className={`nav-badge${labels ? "" : " dot"}`}>{labels ? badge : ""}</span>
                  )}
                </button>
              );
            })}
          </div>
        ))}
      </nav>
      <div className="sidebar-footer">
        <button
          type="button"
          className="panel-toggle"
          onClick={onToggle}
          // On a phone this button is the way out of the drawer, not a width
          // control, and "Collapse sidebar" describes a rail that does not
          // exist there.
          title={room === "phone" ? "Close menu" : open ? "Collapse sidebar" : "Expand sidebar"}
          aria-label={room === "phone" ? "Close menu" : open ? "Collapse sidebar" : "Expand sidebar"}
        >
          {open ? <PanelLeftCloseIcon className="size-4" /> : <PanelLeftOpenIcon className="size-4" />}
        </button>
      </div>
      </aside>
    </>
  );
}
