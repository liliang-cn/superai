import React from "react";
import { AppStatus } from "../lib/types";

export default function StatusBar({
  status,
  loading,
  theme,
  onToggleTheme,
}: {
  status: AppStatus | null;
  loading: boolean;
  theme: "dark" | "light";
  onToggleTheme: () => void;
}) {
  let dotClass = "unknown";
  let label = "Connecting…";
  if (!loading && status) {
    if (status.ready) {
      dotClass = "ok";
      label = "Ready";
    } else {
      dotClass = "bad";
      label = "Not ready";
    }
  }

  return (
    <div className="statusbar">
      <div className="status-main">
        <span className={`status-dot ${dotClass}`} />
        {label}
        {status && !status.ready && status.error && (
          <span className="status-err" title={status.error}>
            — {status.error.length > 60 ? status.error.slice(0, 57) + "…" : status.error}
          </span>
        )}
      </div>
      <div className="status-pills">
        {status && (
          <>
            <span className="pill">memory <b>{status.memoryMode || "—"}</b></span>
            <span className="pill">browser <b>{status.browser ? "on" : "off"}</b></span>
            <span className="pill">skills <b>{status.skills.length}</b></span>
            {status.avatarPort > 0 && <span className="pill">avatar <b>:{status.avatarPort}</b></span>}
          </>
        )}
        <button
          className="theme-toggle"
          onClick={onToggleTheme}
          title={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
        >
          {theme === "dark" ? "☀️" : "🌙"}
        </button>
      </div>
    </div>
  );
}
