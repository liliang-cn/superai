import { ClipboardSetText } from "../../wailsjs/runtime";

// copyText copies via the Wails clipboard runtime, falling back to the Web API.
export async function copyText(s: string): Promise<boolean> {
  try {
    if (await ClipboardSetText(s)) return true;
  } catch {
    /* fall through */
  }
  try {
    await navigator.clipboard.writeText(s);
    return true;
  } catch {
    return false;
  }
}

function esc(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// prettyJson stringifies a value (objects pretty-printed; scalars as-is).
export function prettyJson(value: any): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

// highlightJson returns an HTML string with classed spans for keys/strings/
// numbers/booleans/null — the classic regex JSON highlighter (input escaped).
export function highlightJson(value: any): string {
  const json = esc(prettyJson(value));
  return json.replace(
    /("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,
    (m) => {
      let cls = "j-num";
      if (/^"/.test(m)) cls = /:$/.test(m) ? "j-key" : "j-str";
      else if (m === "true" || m === "false") cls = "j-bool";
      else if (m === "null") cls = "j-null";
      return `<span class="${cls}">${m}</span>`;
    }
  );
}
