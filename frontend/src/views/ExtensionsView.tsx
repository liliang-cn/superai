import { useLocation, useNavigate } from "react-router-dom";
import { PlugIcon, PuzzleIcon } from "lucide-react";
import { useI18n } from "../lib/i18n";
import { PATHS } from "../lib/routes";
import SkillsView from "./SkillsView";
import MCPView from "./MCPView";

/** One desktop destination for the capabilities the assistant can use. */
export default function ExtensionsView() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const isMCP = useLocation().pathname === PATHS.mcp;
  const tabs = [
    { key: "skills", label: "Skills", Icon: PuzzleIcon, path: PATHS.skills },
    { key: "mcp", label: "MCP", Icon: PlugIcon, path: PATHS.mcp },
  ];
  return <div className="extensions-view">
    <header className="extensions-header">
      <h1>{t("Extensions")}</h1>
      <div className="extensions-tabs" role="tablist" aria-label={t("Extensions")}>
        {tabs.map(({key, label, Icon, path}, index) => {
          const selected = isMCP ? key === "mcp" : key === "skills";
          return <button key={key} id={`extension-tab-${key}`} role="tab"
            aria-selected={selected} aria-controls="extension-panel" tabIndex={selected ? 0 : -1}
            onClick={() => navigate(path)}
            onKeyDown={event => {
              if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
              event.preventDefault();
              const next = event.key === "Home" ? 0 : event.key === "End" ? 1 : 1 - index;
              navigate(tabs[next].path);
              document.getElementById(`extension-tab-${tabs[next].key}`)?.focus();
            }}><Icon size={16}/>{t(label)}</button>;
        })}
      </div>
    </header>
    <div id="extension-panel" className="extensions-panel" role="tabpanel" aria-labelledby={`extension-tab-${isMCP ? "mcp" : "skills"}`}>
      {isMCP ? <MCPView/> : <SkillsView/>}
    </div>
  </div>;
}
