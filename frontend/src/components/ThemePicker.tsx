import React, { useEffect, useRef, useState } from "react";
import { Accent, Theme } from "../lib/types";

/**
 * Appearance: how dark the chassis is, and what finish is on it.
 *
 * One control rather than two. The old moon button toggled dark and light and
 * nothing else, so a second button for the accent would have meant two
 * unlabelled glyphs doing related jobs side by side. Both settings live on the
 * same small panel, in the order people decide them: light or dark first, then
 * the finish.
 *
 * The swatch on the button is the current finish, so the control shows its own
 * state without being opened — which is also what makes it findable.
 */

export const ACCENTS: { key: Accent; label: string; swatch: string }[] = [
  { key: "copper", label: "Copper", swatch: "#d2793f" },
  { key: "cobalt", label: "Cobalt", swatch: "#5b93ff" },
  { key: "jade", label: "Jade", swatch: "#2fbfa4" },
  { key: "rose", label: "Rose", swatch: "#f2739f" },
  { key: "violet", label: "Violet", swatch: "#8b8af5" },
];

export default function ThemePicker({
  theme,
  onTheme,
  accent,
  onAccent,
}: {
  theme: Theme;
  onTheme: (t: Theme) => void;
  accent: Accent;
  onAccent: (a: Accent) => void;
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const current = ACCENTS.find((a) => a.key === accent) ?? ACCENTS[0];

  return (
    <div className="theme-picker" ref={wrapRef}>
      <button
        className={`theme-toggle${open ? " on" : ""}`}
        onClick={() => setOpen((v) => !v)}
        title="Appearance"
        aria-label="Appearance"
        aria-expanded={open}
        aria-haspopup="dialog"
      >
        <span className="tp-current" style={{ background: current.swatch }} />
      </button>

      {open && (
        <div className="tp-panel" role="dialog" aria-label="Appearance">
          <div className="tp-group">
            <div className="tp-label">Appearance</div>
            <div className="tp-seg">
              {(["dark", "light"] as Theme[]).map((t) => (
                <button
                  key={t}
                  className={`tp-seg-btn${theme === t ? " on" : ""}`}
                  aria-pressed={theme === t}
                  onClick={() => onTheme(t)}
                >
                  {t === "dark" ? "Dark" : "Light"}
                </button>
              ))}
            </div>
          </div>

          <div className="tp-group">
            <div className="tp-label">Accent</div>
            <div className="tp-swatches">
              {ACCENTS.map((a) => (
                <button
                  key={a.key}
                  className={`tp-swatch${accent === a.key ? " on" : ""}`}
                  style={{ ["--sw" as string]: a.swatch }}
                  title={a.label}
                  aria-label={a.label}
                  aria-pressed={accent === a.key}
                  onClick={() => onAccent(a.key)}
                />
              ))}
            </div>
            <div className="tp-current-name">{current.label}</div>
          </div>
        </div>
      )}
    </div>
  );
}
