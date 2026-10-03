import React, { createContext, useContext, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  BookOpenIcon, BotIcon, ChartColumnIcon, HexagonIcon, MessageSquareIcon, MoonIcon, NotebookTabsIcon, PlugIcon,
  PuzzleIcon, SlidersHorizontalIcon, SunIcon, TerminalIcon,
} from "lucide-react";
import Sky, { Lane } from "../canvas/Sky";
import { CanvasTheme, loadTheme, saveTheme, themeVars } from "../canvas/theme";
import { useCodingRuns } from "../canvas/data";
import { PATHS } from "../lib/routes";
import { ViewKey } from "../lib/types";
import { SetWindowTheme } from "../../wailsjs/go/app/App";
import "../canvas/canvas.css";
import "./desk.css";

const served = Boolean((window as unknown as Record<string, unknown>).superaiServed);
export const drag = { "--wails-draggable": "drag" } as React.CSSProperties;
export const noDrag = { "--wails-draggable": "no-drag" } as React.CSSProperties;

/** The desktop's look, shared by the shell and the page in it. */
const ThemeCtx = createContext<{ theme: CanvasTheme; setTheme: (t: CanvasTheme) => void }>({ theme: loadTheme(), setTheme: () => {} });
export const useDeskTheme = () => useContext(ThemeCtx);

const RAIL: { key: ViewKey; label: string; Icon: typeof HexagonIcon; cls: string }[] = [
  { key: "home", label: "The queen", Icon: MessageSquareIcon, cls: "dk-a-chat" },
  { key: "hive", label: "Hive", Icon: HexagonIcon, cls: "dk-a-hive" },
  { key: "agents", label: "Bees", Icon: BotIcon, cls: "dk-a-bees" },
  { key: "coding", label: "Coding", Icon: TerminalIcon, cls: "dk-a-code" },
  { key: "knowledge", label: "Knowledge", Icon: BookOpenIcon, cls: "dk-a-know" },
  { key: "skills", label: "Skills", Icon: PuzzleIcon, cls: "dk-a-later" },
  { key: "mcp", label: "MCP", Icon: PlugIcon, cls: "dk-a-mcp" },
  { key: "stats", label: "Stats", Icon: ChartColumnIcon, cls: "dk-a-stats" },
  { key: "records", label: "Records", Icon: NotebookTabsIcon, cls: "dk-a-rec" },
];

export const TITLES: Record<ViewKey, string> = {
  home: "", chat: "Chat", hive: "Hive", agents: "Bees", coding: "Coding", knowledge: "Knowledge",
  skills: "Skills", mcp: "MCP", stats: "Stats", records: "Records", settings: "Settings",
};

/**
 * The desktop window around every page: the hive's sky behind, a bar the
 * window is moved by, and the rail of places down the left. The page itself
 * is the shell's child — the home's three panes, or any other screen on a
 * pane of glass.
 */
export default function DeskShell({ view, badges, children }: { view: ViewKey; badges?: Partial<Record<ViewKey, number>>; children: React.ReactNode }) {
  const navigate = useNavigate();
  const [theme, setThemeState] = useState<CanvasTheme>(loadTheme);
  const setTheme = (t: CanvasTheme) => { setThemeState(t); saveTheme(t); };
  const runs = useCodingRuns();
  const liveRuns = runs.filter((r) => r.state === "running").length;

  // The older screens paint with the app-wide tokens; desk.css gives them the
  // hive's while this shell is up.
  useEffect(() => {
    document.documentElement.dataset.desk = "1";
    return () => { delete document.documentElement.dataset.desk; };
  }, []);

  // The older screens read the app-wide light/dark flag; keep it in step.
  useEffect(() => {
    document.documentElement.dataset.theme = theme.mode;
    try { localStorage.setItem("superai-theme", theme.mode); } catch { /* fine */ }
    if (!served) void SetWindowTheme(theme.mode === "dark").catch(() => {});
  }, [theme.mode]);

  const lanes = useMemo<Lane[]>(() => {
    const palette: [number, number, number][] = [[242, 165, 22], [255, 120, 90], [70, 180, 170], [60, 170, 100], [80, 150, 240]];
    return Array.from({ length: 5 }, (_, i) => ({ key: "d" + i, busy: 0.12, rgb: palette[i] }));
  }, []);

  const count = (k: ViewKey) => (k === "coding" ? liveRuns : badges?.[k] ?? 0);

  return (
    <ThemeCtx.Provider value={{ theme, setTheme }}>
      <div className={`cv-root dk-shell ${theme.mode === "dark" ? "dk-dark" : ""}`} style={themeVars(theme)}>
        <Sky at={new Date()} lanes={lanes} base={theme.base} mode={theme.mode} />
        <div className="dk-bar" style={drag} />
        <nav className="dk-rail" style={drag}>
          {RAIL.map(({ key, label, Icon, cls }) => (
            <button key={key} title={label} style={noDrag} className={`dk-app ${cls} ${view === key || (key === "home" && view === "chat") ? "on" : ""}`}
              onClick={() => navigate(PATHS[key])}>
              <Icon size={21} strokeWidth={2} />
              {count(key) > 0 && <span className="dk-badge">{count(key)}</span>}
            </button>
          ))}
          <span className="dk-sp" />
          <button title={theme.mode === "dark" ? "Light" : "Dark"} style={noDrag} className="dk-app dk-a-mode"
            onClick={() => setTheme({ ...theme, mode: theme.mode === "dark" ? "light" : "dark" })}>
            {theme.mode === "dark" ? <SunIcon size={19} /> : <MoonIcon size={19} />}
          </button>
          <button title="Settings" style={noDrag} className={`dk-app dk-a-set ${view === "settings" ? "on" : ""}`} onClick={() => navigate(PATHS.settings)}>
            <SlidersHorizontalIcon size={20} />
          </button>
        </nav>
        {view === "home" ? children : (
          <main className="cv-glass dk-page">
            <div className="content">{children}</div>
          </main>
        )}
      </div>
    </ThemeCtx.Provider>
  );
}
