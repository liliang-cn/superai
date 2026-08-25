import React, { useEffect, useState } from "react";
import { LogOutIcon, PanelLeftCloseIcon, PanelLeftOpenIcon } from "lucide-react";
import { ViewKey } from "../lib/types";

// Only the served build has a session to end; the desktop window has no door.
const served = Boolean((window as unknown as Record<string, unknown>).superaiServed);

const NAV: { section: string; items: { key: ViewKey; label: string; icon: string }[] }[] = [
  {
    section: "Workspace",
    items: [
      { key: "chat", label: "Chat", icon: "💬" },
      { key: "schedules", label: "Schedules", icon: "⏰" },
    ],
  },
  {
    section: "Knowledge",
    items: [
      { key: "memory", label: "Memory", icon: "🧠" },
      { key: "skills", label: "Skills", icon: "🧩" },
      { key: "mcp", label: "MCP", icon: "🔌" },
      { key: "data", label: "Data", icon: "🗄️" },
      { key: "life", label: "Life", icon: "🌱" },
    ],
  },
  {
    section: "System",
    items: [
      { key: "avatar", label: "Avatar", icon: "🎭" },
      { key: "settings", label: "Settings", icon: "⚙️" },
    ],
  },
];

const OPEN_KEY = "superai-sidebar-open";

export default function Sidebar({
  current,
  onNavigate,
  badges,
}: {
  current: ViewKey;
  onNavigate: (v: ViewKey) => void;
  /** Counts of things that happened on their own, per view. */
  badges?: Partial<Record<ViewKey, number>>;
}) {
  const [open, setOpen] = useState(() => localStorage.getItem(OPEN_KEY) !== "0");

  useEffect(() => {
    localStorage.setItem(OPEN_KEY, open ? "1" : "0");
  }, [open]);

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
          onClick={() => setOpen((v) => !v)}
          title={open ? "Collapse sidebar" : "Expand sidebar"}
          aria-label={open ? "Collapse sidebar" : "Expand sidebar"}
        >
          {open ? <PanelLeftCloseIcon className="size-4" /> : <PanelLeftOpenIcon className="size-4" />}
        </button>
        {served && (
          <button
            type="button"
            className="panel-toggle sign-out"
            // Reload rather than route: the cookie is gone, so the shell asks
            // /api/session on the way back up and lands on the password box.
            onClick={() => {
              void fetch("/api/logout", { method: "POST" }).finally(() => window.location.reload());
            }}
            title="Sign out"
            aria-label="Sign out"
          >
            <LogOutIcon className="size-4" />
          </button>
        )}
      </div>
    </aside>
  );
}
