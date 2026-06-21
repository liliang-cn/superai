import React from "react";
import { ViewKey } from "../lib/types";

const NAV: { section: string; items: { key: ViewKey; label: string; icon: string }[] }[] = [
  {
    section: "Workspace",
    items: [
      { key: "chat", label: "Chat", icon: "💬" },
      { key: "agent", label: "Agent", icon: "🤖" },
    ],
  },
  {
    section: "Knowledge",
    items: [
      { key: "memory", label: "Memory", icon: "🧠" },
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

export default function Sidebar({
  current,
  onNavigate,
}: {
  current: ViewKey;
  onNavigate: (v: ViewKey) => void;
}) {
  return (
    <aside className="sidebar">
      <div className="brand">
        <div className="brand-logo">S</div>
        <div>
          <div className="brand-name">SuperAI</div>
          <div className="brand-sub">Desktop</div>
        </div>
      </div>
      <nav className="nav">
        {NAV.map((group) => (
          <div key={group.section}>
            <div className="nav-section">{group.section}</div>
            {group.items.map((it) => (
              <button
                key={it.key}
                className={`nav-item${current === it.key ? " active" : ""}`}
                onClick={() => onNavigate(it.key)}
              >
                <span className="ic">{it.icon}</span>
                {it.label}
              </button>
            ))}
          </div>
        ))}
      </nav>
      <div className="sidebar-footer">AgentGo framework</div>
    </aside>
  );
}
