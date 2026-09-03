import React from "react";
import { LogOutIcon, MenuIcon } from "lucide-react";
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
  petOpen,
  onTogglePet,
  onOpenNav,
}: {
  status: AppStatus | null;
  loading: boolean;
  theme: Theme;
  onTheme: (t: Theme) => void;
  accent: Accent;
  onAccent: (a: Accent) => void;
  petOpen: boolean;
  onTogglePet: () => void;
  /** Opens or closes the sidebar. Only reachable on a narrow screen, where the
   *  sidebar is a drawer and has taken its own toggle off the screen with it.
   *  A toggle, not an open: a button that can only open is half a control. */
  onOpenNav: () => void;
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
        <button
          type="button"
          className="nav-open panel-toggle"
          onClick={onOpenNav}
          title="Menu"
          aria-label="Open navigation"
        >
          <MenuIcon className="size-4" />
        </button>
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
            <span className="pill">skills <b>{status.skills.length}</b></span>
            {/* What the agent can reach that is not built in. It replaced a
                `browser on/off` pill that had been hardcoded to off ever since
                agent-go dropped its own browser — browsing now arrives as an
                MCP server like everything else, so this is where it shows. */}
            <span className="pill" title={`${status.mcpTools} tools`}>
              MCP <b>{status.mcp}</b>
            </span>
            {/* Not a readout: the avatar port was only ever interesting as a
                place to go, and now there is somewhere to go without leaving
                the window. */}
            {status.avatarPort > 0 && (
              <button
                className={`pill-btn${petOpen ? " on" : ""}`}
                onClick={onTogglePet}
                title={petOpen ? "Send the avatar away" : "Let the avatar out into the window"}
                aria-pressed={petOpen}
              >
                avatar <b>{petOpen ? "out" : ":" + status.avatarPort}</b>
              </button>
            )}
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
