import React, { createContext, useContext, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ListTodoIcon, LayoutDashboardIcon, ChartColumnIcon, PanelRightOpenIcon, BookOpenIcon, BotIcon, HexagonIcon, MessageSquareIcon, PaletteIcon,
  NotebookTabsIcon, PuzzleIcon, SlidersHorizontalIcon, TerminalIcon,
} from "lucide-react";
import DeskStatusBar from "./DeskStatusBar";
import Sky, { Lane } from "../canvas/Sky";
import { CanvasTheme, loadTheme, saveTheme, themeVars } from "../canvas/theme";
import { useCodingRuns } from "../canvas/data";
import { PATHS } from "../lib/routes";
import { AppStatus, ViewKey } from "../lib/types";
import { SetWindowTheme } from "../../wailsjs/go/app/App";
import { useI18n } from "../lib/i18n";
import NotificationCenter from "../components/NotificationCenter";
import BackendSwitcher from "./BackendSwitcher";
import ThemePanel from "../canvas/ThemePanel";
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
  { key: "tasks", label: "Needs your attention", Icon: ListTodoIcon, cls: "dk-a-later" },
  { key: "hive", label: "Hive", Icon: HexagonIcon, cls: "dk-a-hive" },
  { key: "agents", label: "Bees", Icon: BotIcon, cls: "dk-a-bees" },
  { key: "coding", label: "Coding", Icon: TerminalIcon, cls: "dk-a-code" },
  { key: "knowledge", label: "Knowledge", Icon: BookOpenIcon, cls: "dk-a-know" },
  { key: "records", label: "Records", Icon: NotebookTabsIcon, cls: "dk-a-rec" },
  { key: "skills", label: "Extensions", Icon: PuzzleIcon, cls: "dk-a-later" },
];

/** The pages the right column opens; it stays on every page but the home,
 *  which has its own, and the open one's button closes it. */
const SIDE: ViewKey[] = ["dashboards", "stats"];

export const TITLES: Record<ViewKey, string> = {
  home: "", chat: "Chat", tasks: "Needs your attention", dashboards: "Dashboards", hive: "Hive", agents: "Bees", coding: "Coding", knowledge: "Knowledge",
  skills: "Skills", mcp: "MCP", stats: "Stats", records: "Records", settings: "Settings",
};

/**
 * The desktop window around every page: the hive's sky behind, a bar the
 * window is moved by, and the rail of places down the left. The page itself
 * is the shell's child — the home's three panes, or any other screen on a
 * pane of glass.
 */
export default function DeskShell({ view, badges, children, onOpenConversation, status, loading }: { view: ViewKey; badges?: Partial<Record<ViewKey, number>>; children: React.ReactNode; onOpenConversation?: (session: string) => void; status:AppStatus|null; loading:boolean }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [theme, setThemeState] = useState<CanvasTheme>(loadTheme);
  const setTheme = (t: CanvasTheme) => { setThemeState(t); saveTheme(t); };
  const [theming, setTheming] = useState(false);
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
        <div className="dk-corner" style={noDrag}>
          <NotificationCenter variant="desk" onOpenConversation={onOpenConversation} />
        </div>
        <nav className="dk-rail" style={drag}>
          {RAIL.map(({ key, label, Icon, cls }) => (
            <button key={key} title={t(label)} style={noDrag} className={`dk-app ${cls} ${view === key || (key === "skills" && view === "mcp") || (key === "home" && view === "chat") ? "on" : ""}`}
              onClick={() => navigate(PATHS[key])}>
              <Icon size={21} strokeWidth={2} />
              {count(key) > 0 && <span className="dk-badge">{count(key)}</span>}
            </button>
          ))}
          <span className="dk-sp" />
          <BackendSwitcher />
          <button title={t("Theme")} style={noDrag} className={`dk-app dk-a-mode${theming ? " on" : ""}`} onClick={() => setTheming((v) => !v)}>
            <PaletteIcon size={19} />
          </button>
          <button title={t("Settings")} style={noDrag} className={`dk-app dk-a-set ${view === "settings" ? "on" : ""}`} onClick={() => navigate(PATHS.settings)}>
            <SlidersHorizontalIcon size={20} />
          </button>
        </nav>
        {theming && <ThemePanel className="dk-theme" theme={theme} onChange={setTheme} onClose={() => setTheming(false)} />}
        {view !== "home" && (
          <nav className="dk-live-strip dk-side-strip" style={noDrag}>
            <button className="cv-glass dk-unfold" title={t("Show the hive")} onClick={() => navigate(PATHS.home)}><PanelRightOpenIcon size={18} /></button>
            {SIDE.map((k) => {
              const Icon = k === "dashboards" ? LayoutDashboardIcon : ChartColumnIcon;
              // The page's own button closes it, back to the home it was opened from.
              return (
                <button key={k} className={`cv-glass dk-unfold${view === k ? " on" : ""}`} title={t(TITLES[k])} onClick={() => navigate(view === k ? PATHS.home : PATHS[k])}>
                  <Icon size={18} />
                </button>
              );
            })}
          </nav>
        )}
        {view === "home" ? children : (
          <main className="cv-glass dk-page with-side">
            <div className="content">{children}</div>
          </main>
        )}
        <DeskStatusBar status={status} loading={loading} codingRuns={liveRuns}/>
      </div>
    </ThemeCtx.Provider>
  );
}
