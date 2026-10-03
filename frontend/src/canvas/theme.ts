import type React from "react";

/** The canvas's look, picked the way shadcn's theme panel picks one: a colour,
 *  a background, how much glass, how round, light or dark. */
export interface CanvasTheme {
  color: ColorName;
  base: BaseName;
  glass: "clear" | "frosted" | "solid";
  radius: 0 | 0.5 | 0.75 | 1 | 1.25;
  mode: "light" | "dark";
}

export type ColorName = "honey" | "orange" | "red" | "rose" | "green" | "teal" | "blue" | "yellow" | "neutral";
export type BaseName = "timelapse" | "neutral" | "stone" | "slate" | "sand";

// No violet: purple is the one colour asked to stay out.
export const COLORS: Record<ColorName, { rgb: [number, number, number]; deep: string; label: string }> = {
  honey: { rgb: [242, 165, 22], deep: "#9a5c06", label: "Honey" },
  orange: { rgb: [249, 115, 22], deep: "#a5420a", label: "Orange" },
  red: { rgb: [229, 72, 77], deep: "#a3222a", label: "Red" },
  rose: { rgb: [236, 92, 128], deep: "#a32448", label: "Rose" },
  green: { rgb: [34, 170, 98], deep: "#11693b", label: "Green" },
  teal: { rgb: [20, 168, 168], deep: "#0b6767", label: "Teal" },
  blue: { rgb: [59, 130, 246], deep: "#1d4fb8", label: "Blue" },
  yellow: { rgb: [234, 196, 30], deep: "#806600", label: "Yellow" },
  neutral: { rgb: [82, 82, 82], deep: "#262626", label: "Neutral" },
};

/** A fixed ground, light and dark, for when the time-lapse is not wanted. */
export const BASES: Record<Exclude<BaseName, "timelapse">, { light: [number[], number[]]; dark: [number[], number[]]; label: string }> = {
  neutral: { light: [[236, 236, 236], [250, 250, 250]], dark: [[23, 23, 23], [38, 38, 38]], label: "Neutral" },
  stone: { light: [[236, 232, 226], [250, 248, 245]], dark: [[28, 25, 23], [41, 37, 36]], label: "Stone" },
  slate: { light: [[222, 230, 239], [246, 248, 251]], dark: [[15, 23, 42], [30, 41, 59]], label: "Slate" },
  sand: { light: [[244, 232, 212], [252, 247, 238]], dark: [[33, 28, 20], [48, 40, 29]], label: "Sand" },
};

export const DEFAULT_THEME: CanvasTheme = { color: "honey", base: "timelapse", glass: "frosted", radius: 1, mode: "light" };

const KEY = "superai-canvas-theme";

export function loadTheme(): CanvasTheme {
  try {
    return { ...DEFAULT_THEME, ...JSON.parse(localStorage.getItem(KEY) || "{}") };
  } catch {
    return DEFAULT_THEME;
  }
}

export function saveTheme(t: CanvasTheme) {
  try {
    localStorage.setItem(KEY, JSON.stringify(t));
  } catch {
    /* kept for this visit only */
  }
}

/** The CSS variables the canvas's stylesheet reads. */
export function themeVars(t: CanvasTheme): React.CSSProperties {
  const c = COLORS[t.color];
  const dark = t.mode === "dark";
  const glass = { clear: [0.14, 10], frosted: [0.3, 22], solid: [0.62, 30] }[t.glass];
  return {
    "--honey": `rgb(${c.rgb.join(",")})`,
    "--honey-rgb": c.rgb.join(","),
    "--honey-deep": dark ? `rgb(${c.rgb.join(",")})` : c.deep,
    "--rs": String(t.radius),
    "--hl": dark ? "0.22" : "0.95",
    "--ga": String(dark ? glass[0] * 0.9 : glass[0]),
    "--gb": `${glass[1]}px`,
    "--glass-rgb": dark ? "30,32,36" : "255,255,255",
    "--ink": dark ? "#f2f1ee" : "#1d1a16",
    "--ink-2": dark ? "rgba(242,241,238,.66)" : "rgba(29,26,22,.62)",
    "--ink-3": dark ? "rgba(242,241,238,.42)" : "rgba(29,26,22,.42)",
    "--line": dark ? "rgba(255,255,255,.1)" : "rgba(29,26,22,.08)",
    colorScheme: dark ? "dark" : "light",
  } as React.CSSProperties;
}
