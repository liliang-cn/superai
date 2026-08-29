import React, { useCallback, useEffect, useState } from "react";
import Sidebar from "./components/Sidebar";
import StatusBar from "./components/StatusBar";
import ChatView from "./views/ChatView";
import SettingsView from "./views/SettingsView";
import AvatarView from "./views/AvatarView";
import MemoryView from "./views/MemoryView";
import GraphView from "./views/GraphView";
import SkillsView from "./views/SkillsView";
import MCPView from "./views/MCPView";
import DataView from "./views/DataView";
import LifeView from "./views/LifeView";
import { ScheduleRunToasts } from "./components/ScheduleRuns";
import ToolApprovals from "./components/ToolApprovals";
import YoloBanner from "./components/YoloBanner";
import { AppStatus, ViewKey, normalizeStatus } from "./lib/types";
import { useScheduleRuns } from "./lib/useScheduleRuns";
import { useToolApprovals } from "./lib/useToolApprovals";
import { uiRules } from "./lib/aigui";
import { GetStatus, SetUIRules } from "../wailsjs/go/main/App";

type Theme = "dark" | "light";

export default function App() {
  const [view, setView] = useState<ViewKey>("chat");
  const [status, setStatus] = useState<AppStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [theme, setTheme] = useState<Theme>(
    () => (localStorage.getItem("superai-theme") as Theme) || "dark"
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

  // Tool approvals live at the root for a stronger version of the same reason:
  // the agent's turn is blocked until one is answered, and the prompt has to
  // find the user wherever they are — including on the Settings page they went
  // to in order to look at the audit log.
  const approvals = useToolApprovals();

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem("superai-theme", theme);
  }, [theme]);

  // Tell the agent which rich blocks this transcript can render. The rules come
  // from the same registry + plugins the renderer uses, so they cannot drift;
  // the backend only rebuilds when they actually changed.
  useEffect(() => {
    SetUIRules(uiRules()).catch(() => {});
  }, []);
  const toggleTheme = useCallback(
    () => setTheme((t) => (t === "dark" ? "light" : "dark")),
    []
  );

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
        <Sidebar current={view} onNavigate={setView} badges={{ life: runs.unseen }} />
        <div className="main">
          <StatusBar status={status} loading={loading} theme={theme} onToggleTheme={toggleTheme} />
          <div className="content">
            {view === "chat" && (
              <ChatView
                status={status}
                openSession={pendingSession}
                onSessionOpened={() => setPendingSession("")}
              />
            )}
            {view === "settings" && <SettingsView onSaved={refreshStatus} />}
            {view === "avatar" && <AvatarView status={status} />}
            {view === "memory" && <MemoryView />}
            {view === "graph" && <GraphView />}
            {view === "skills" && <SkillsView />}
            {view === "mcp" && <MCPView />}
            {view === "data" && <DataView />}
            {view === "life" && (
              <LifeView status={status} log={runs} onOpenConversation={openConversation} />
            )}
          </div>
        </div>
      </div>
      {/* Records lists the same runs, so a toast there would only repeat what
          is already on screen. */}
      {view !== "life" && (
        <ScheduleRunToasts log={runs} onOpenConversation={openConversation} />
      )}
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
