import React, { useCallback, useEffect, useState } from "react";
import Sidebar from "./components/Sidebar";
import StatusBar from "./components/StatusBar";
import ChatView from "./views/ChatView";
import SettingsView from "./views/SettingsView";
import AvatarView from "./views/AvatarView";
import MemoryView from "./views/MemoryView";
import SkillsView from "./views/SkillsView";
import MCPView from "./views/MCPView";
import DataView from "./views/DataView";
import LifeView from "./views/LifeView";
import { AppStatus, ViewKey, normalizeStatus } from "./lib/types";
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
    <div className="app">
      <Sidebar current={view} onNavigate={setView} />
      <div className="main">
        <StatusBar status={status} loading={loading} theme={theme} onToggleTheme={toggleTheme} />
        <div className="content">
          {view === "chat" && <ChatView status={status} />}
          {view === "settings" && <SettingsView onSaved={refreshStatus} />}
          {view === "avatar" && <AvatarView status={status} />}
          {view === "memory" && <MemoryView />}
          {view === "skills" && <SkillsView />}
          {view === "mcp" && <MCPView />}
          {view === "data" && <DataView />}
          {view === "life" && <LifeView />}
        </div>
      </div>
    </div>
  );
}
