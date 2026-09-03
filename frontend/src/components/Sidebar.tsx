import React from "react";
import { PanelLeftCloseIcon, PanelLeftOpenIcon } from "lucide-react";
import { ViewKey } from "../lib/types";

const NAV: { section: string; items: { key: ViewKey; label: string; icon: string }[] }[] = [
  {
    section: "Workspace",
    items: [
      { key: "chat", label: "Chat", icon: "💬" },
      { key: "dashboard", label: "Dashboard", icon: "📊" },
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

  // Collapsed keeps the icons rather than hiding the nav outright — navigation
  // stays one click away, it just stops spending width on labels.
  return (
    <aside className={`sidebar${open ? "" : " collapsed"}`}>
      <div className="brand">
        <div className="brand-logo">S</div>
        {open && (
          <div className="brand-text">
            <div className="brand-name">SuperAI</div>
            <div className="brand-sub">Desktop</div>
          </div>
        )}
      </div>
      <nav className="nav">
        {NAV.map((group) => (
          <div key={group.section}>
            {open && <div className="nav-section">{group.section}</div>}
            {group.items.map((it) => {
              const badge = badges?.[it.key] ?? 0;
              return (
                <button
                  key={it.key}
                  className={`nav-item${current === it.key ? " active" : ""}`}
                  onClick={() => onNavigate(it.key)}
                  title={open ? undefined : it.label}
                >
                  <span className="ic">{it.icon}</span>
                  {open && it.label}
                  {/* Collapsed there is no room for a count, but "something
                      happened" still has to be visible. */}
                  {badge > 0 && (
                    <span className={`nav-badge${open ? "" : " dot"}`}>{open ? badge : ""}</span>
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
          title={open ? "Collapse sidebar" : "Expand sidebar"}
          aria-label={open ? "Collapse sidebar" : "Expand sidebar"}
        >
          {open ? <PanelLeftCloseIcon className="size-4" /> : <PanelLeftOpenIcon className="size-4" />}
        </button>
      </div>
    </aside>
  );
}
