import React from "react";
import { LogOutIcon } from "lucide-react";
import { Accent, AppStatus, Theme } from "../lib/types";
import ThemePicker from "./ThemePicker";

// Only the served build has a session to end; the desktop window has no door.
const served = Boolean((window as unknown as Record<string, unknown>).superaiServed);

export default function StatusBar({
  status,
  loading,
  theme,
  onTheme,
  accent,
  onAccent,
}: {
  status: AppStatus | null;
  loading: boolean;
  theme: Theme;
  onTheme: (t: Theme) => void;
  accent: Accent;
  onAccent: (a: Accent) => void;
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
        <ThemePicker theme={theme} onTheme={onTheme} accent={accent} onAccent={onAccent} />
        {served && (
          <button
            className="theme-toggle sign-out"
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
    </div>
  );
}
