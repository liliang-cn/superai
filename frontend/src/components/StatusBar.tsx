import React from "react";
import { LogOutIcon, MenuIcon, PawPrintIcon } from "lucide-react";
import { Accent, AppStatus, Theme } from "../lib/types";
import ThemePicker from "./ThemePicker";
import NotificationCenter from "./NotificationCenter";

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
  onOpenConversation,
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
  /** Where a notification that names a conversation goes when it is clicked. */
  onOpenConversation?: (session: string) => void;
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
        {/* The memory / skills / MCP readouts used to sit here. They were three
            numbers that never changed while you looked at them, in the one strip
            that is on screen in every view — and each already has a page of its
            own that says the same thing with the detail to act on. What is left
            here is only what is a control. */}
        {status && status.avatarPort > 0 && (
          <button
            className={`pill-btn icon-only${petOpen ? " on" : ""}`}
            onClick={onTogglePet}
            title={petOpen ? "Send the avatar away" : "Let the avatar out into the window"}
            aria-label="Avatar"
            aria-pressed={petOpen}
          >
            <PawPrintIcon className="size-4" />
          </button>
        )}
        {/* Here rather than in a view of its own: this strip is the only thing
            on screen in every view, and a centre you have to navigate to is one
            you check after you already found out the hard way. */}
        <NotificationCenter onOpenConversation={onOpenConversation} />
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
