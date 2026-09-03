import React, { useCallback, useEffect, useState } from "react";
import Sidebar from "./components/Sidebar";
import Pet from "./components/Pet";
import StatusBar from "./components/StatusBar";
import ChatView from "./views/ChatView";
import DashboardView from "./views/DashboardView";
import SettingsView from "./views/SettingsView";
import KnowledgeView from "./views/KnowledgeView";
import SkillsView from "./views/SkillsView";
import MCPView from "./views/MCPView";
import RecordsView from "./views/RecordsView";
import { ScheduleRunToasts } from "./components/ScheduleRuns";
import { Toaster } from "./components/Toaster";
import { useBackendToasts } from "./lib/toasts";
import { EventsOff, EventsOn } from "../wailsjs/runtime/runtime";
import ToolApprovals from "./components/ToolApprovals";
import YoloBanner from "./components/YoloBanner";
import { Accent, AppStatus, Theme, ViewKey, normalizeStatus } from "./lib/types";
import { useScheduleRuns } from "./lib/useScheduleRuns";
import { useToolApprovals } from "./lib/useToolApprovals";
import { uiRules } from "./lib/aigui";
import { GetStatus, SetUIRules, SetWindowTheme } from "../wailsjs/go/main/App";

/** Where the sidebar remembers being expanded. Shared with the drawer on a
 *  phone, which is the same state seen through a different layout. */
const NAV_OPEN_KEY = "superai-sidebar-open";

export default function App() {
  // Draw every notice the backend publishes. Mounted here so it survives every
  // view change: a toast about a run that just failed must not disappear
  // because the user clicked to another screen to look into it.
  useBackendToasts();

  const [view, setView] = useState<ViewKey>("chat");
  const [status, setStatus] = useState<AppStatus | null>(null);
  // Off until asked for. Something walking across the window is charming when
  // you let it out and an interruption when it arrives on its own.
  const [petOpen, setPetOpen] = useState(false);
  // The sidebar: expanded on a desktop, a drawer on a phone. It starts closed
  // on a narrow screen whatever was stored, because a drawer covering the app
  // on arrival is not a navigation aid.
  const [navOpen, setNavOpen] = useState(() => {
    try {
      if (window.matchMedia("(max-width: 640px)").matches) return false;
      return localStorage.getItem(NAV_OPEN_KEY) !== "0";
    } catch {
      return true;
    }
  });
  useEffect(() => {
    try {
      // Only a desktop writes this down. On a phone the same flag is a drawer
      // being out, and closing a drawer — which navigating does on its own — is
      // not a statement about how you like your sidebar. Sharing the key
      // without this guard means one visit on a phone leaves the desktop
      // sidebar collapsed for good.
      if (window.matchMedia("(max-width: 640px)").matches) return;
      localStorage.setItem(NAV_OPEN_KEY, navOpen ? "1" : "0");
    } catch {
      // A private window. It simply opens on its default next time.
    }
  }, [navOpen]);
  const [loading, setLoading] = useState(true);
  const [theme, setTheme] = useState<Theme>(
    () => (localStorage.getItem("superai-theme") as Theme) || "dark"
  );
  // The accent finish. Remembered per browser rather than in settings.json:
  // it changes nothing the agent does, and a preference the backend has to be
  // rebuilt to apply is a preference nobody flips twice.
  const [accent, setAccent] = useState<Accent>(
    () => (localStorage.getItem("superai-accent") as Accent) || "copper"
  );
  // The conversation a run belongs to, handed to the chat view to open. Cleared
  // as soon as it has been taken so asking for the same one twice works.
  const [pendingSession, setPendingSession] = useState("");

  const openConversation = useCallback((session: string) => {
    if (!session) return;
    setPendingSession(session);
    setView("chat");
  }, []);

  // Scheduled runs are listened for here, not in the Schedules view: a timer
  // fires while the user is somewhere else, which is the whole point of a timer.
  // The opener goes with it so a clicked notification lands in the right place.
  const runs = useScheduleRuns(openConversation);

  // A click on a native banner. The desktop build carries the conversation on
  // the notification itself and asks for it back here, because a banner that
  // says a run finished and leaves you to find it is a dead end — a reminder
  // that fired hours ago means scrolling a list to work out which one it meant.
  useEffect(() => {
    EventsOn("open:conversation", (payload: { session?: string }) => {
      if (payload?.session) openConversation(payload.session);
    });
    return () => EventsOff("open:conversation");
  }, [openConversation]);

  // Tool approvals live at the root for a stronger version of the same reason:
  // the agent's turn is blocked until one is answered, and the prompt has to
  // find the user wherever they are — including on the Settings page they went
  // to in order to look at the audit log.
  const approvals = useToolApprovals();

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem("superai-theme", theme);
    // The window chrome is drawn by the OS, not by this stylesheet. Without
    // this the title bar keeps whatever colour the app started with, and a
    // light page sits under a dark bar looking half-converted. Runs on mount
    // too, so a remembered theme is matched before the first paint rather than
    // only after the next toggle. Harmless in a browser tab, which has no
    // chrome of its own to paint.
    void SetWindowTheme(theme === "dark").catch(() => {});
  }, [theme]);

  useEffect(() => {
    document.documentElement.dataset.accent = accent;
    localStorage.setItem("superai-accent", accent);
  }, [accent]);

  // Tell the agent which rich blocks this transcript can render. The rules come
  // from the same registry + plugins the renderer uses, so they cannot drift;
  // the backend only rebuilds when they actually changed.
  useEffect(() => {
    SetUIRules(uiRules()).catch(() => {});
  }, []);
  const refreshStatus = useCallback(async () => {
    try {
      const raw = await GetStatus();
      setStatus(normalizeStatus(raw));
    } catch (e: any) {
      setStatus(normalizeStatus({ ready: false, error: String(e?.message || e) }));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refreshStatus();
    const t = setInterval(refreshStatus, 15000);
    return () => clearInterval(t);
  }, [refreshStatus]);

  return (
    <>
      <div className="app">
        <Sidebar
          current={view}
          onNavigate={(v) => {
            setView(v);
            // On a phone the sidebar is a drawer over the page, so navigating
            // has to close it or the destination is behind it.
            if (window.matchMedia("(max-width: 640px)").matches) setNavOpen(false);
          }}
          badges={{ records: runs.unseen }}
          open={navOpen}
          onToggle={() => setNavOpen((v) => !v)}
        />
        <div className="main">
          <StatusBar
            status={status}
            loading={loading}
            theme={theme}
            onTheme={setTheme}
            accent={accent}
            onAccent={setAccent}
            petOpen={petOpen}
            onTogglePet={() => setPetOpen((open) => !open)}
            onOpenNav={() => setNavOpen(true)}
          />
          <div className="content">
            {view === "chat" && (
              <ChatView
                status={status}
                openSession={pendingSession}
                onSessionOpened={() => setPendingSession("")}
              />
            )}
            {view === "dashboard" && <DashboardView />}
            {view === "settings" && <SettingsView onSaved={refreshStatus} status={status} />}
            {view === "knowledge" && <KnowledgeView />}
            {view === "skills" && <SkillsView />}
            {view === "mcp" && <MCPView />}
            {view === "records" && (
              <RecordsView status={status} log={runs} onOpenConversation={openConversation} />
            )}
          </div>
        </div>
      </div>
      {/* Loose in the window, over everything, catching nothing but its own
          clicks. Needs the avatar server for its sprites and its state, which
          is also the only condition under which the pill that opens it shows. */}
      {petOpen && status && status.avatarPort > 0 && (
        <Pet port={status.avatarPort} onDismiss={() => setPetOpen(false)} />
      )}
      {/* Records lists the same runs, so a toast there would only repeat what
          is already on screen. */}
      {view !== "records" && (
        <ScheduleRunToasts log={runs} onOpenConversation={openConversation} />
      )}
      {/* Everything the backend publishes, whatever raised it. */}
      <Toaster onOpenConversation={openConversation} />
      <YoloBanner />
      <ToolApprovals
        pending={approvals.pending}
        note={approvals.note}
        onResolve={approvals.resolve}
        onDismissNote={approvals.dismissNote}
      />
    </>
  );
}
