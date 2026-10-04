import { useI18n } from "../lib/i18n";
import { BASES, BaseName, COLORS, CanvasTheme, ColorName, DEFAULT_THEME } from "./theme";

/** The look, chosen like shadcn's theme panel: every pick applies at once,
 *  and the canvas behind the panel is the preview. */
export default function ThemePanel({ theme, onChange, onClose, className = "" }: { theme: CanvasTheme; onChange: (t: CanvasTheme) => void; onClose: () => void; className?: string }) {
  const { t } = useI18n();
  const set = <K extends keyof CanvasTheme>(k: K, v: CanvasTheme[K]) => onChange({ ...theme, [k]: v });
  return (
    <div className={`cv-scrim ${className}`} style={{ background: "transparent" }} onClick={onClose}>
      <div className="cv-glass cv-sheet" onClick={(e) => e.stopPropagation()} data-testid="theme-panel">
        <h3>{t("Theme")}</h3>
        <div className="cv-theme-row">
          <label>{t("Color")}</label>
          <div className="cv-theme-opts colors">
            {(Object.keys(COLORS) as ColorName[]).map((c) => (
              <button key={c} title={t(COLORS[c].label)} aria-label={t(COLORS[c].label)} className={`cv-opt${theme.color === c ? " on" : ""}`} onClick={() => set("color", c)}>
                <i style={{ background: `rgb(${COLORS[c].rgb.join(",")})` }} /><span>{t(COLORS[c].label)}</span>
              </button>
            ))}
          </div>
        </div>
        <div className="cv-theme-row">
          <label>{t("Background")}</label>
          <div className="cv-theme-opts bases">
            <button className={`cv-opt${theme.base === "timelapse" ? " on" : ""}`} onClick={() => set("base", "timelapse")}><i className="sky" />{t("Time-lapse")}</button>
            {(Object.keys(BASES) as Exclude<BaseName, "timelapse">[]).map((b) => {
              const [top, bottom] = BASES[b][theme.mode];
              return (
                <button key={b} className={`cv-opt${theme.base === b ? " on" : ""}`} onClick={() => set("base", b)}>
                  <i style={{ background: `linear-gradient(rgb(${top}), rgb(${bottom}))`, boxShadow: "inset 0 0 0 1px rgba(0,0,0,.12)" }} />{t(BASES[b].label)}
                </button>
              );
            })}
          </div>
        </div>
        <div className="cv-theme-row">
          <label>{t("Glass")}</label>
          <div className="cv-theme-opts">
            {(["clear", "frosted", "solid"] as const).map((g) => (
              <button key={g} className={`cv-opt${theme.glass === g ? " on" : ""}`} onClick={() => set("glass", g)}>{t(g[0].toUpperCase() + g.slice(1))}</button>
            ))}
          </div>
        </div>
        <div className="cv-theme-row">
          <label>{t("Radius")}</label>
          <div className="cv-theme-opts">
            {([0, 0.5, 0.75, 1, 1.25] as const).map((r) => (
              <button key={r} className={`cv-opt${theme.radius === r ? " on" : ""}`} onClick={() => set("radius", r)}>{r}</button>
            ))}
          </div>
        </div>
        <div className="cv-theme-row">
          <label>{t("Mode")}</label>
          <div className="cv-theme-opts">
            {(["light", "dark"] as const).map((m) => (
              <button key={m} className={`cv-opt${theme.mode === m ? " on" : ""}`} onClick={() => set("mode", m)}>{t(m === "light" ? "Light" : "Dark")}</button>
            ))}
          </div>
        </div>
        <div className="cv-theme-foot">
          <button className="cv-pill" onClick={() => onChange(DEFAULT_THEME)}>{t("Reset")}</button>
          <button className="cv-pill ink" onClick={onClose}>{t("Done")}</button>
        </div>
      </div>
    </div>
  );
}
