import React, { useCallback, useEffect, useState } from "react";
import { ChevronDownIcon, ChevronUpIcon, RefreshCwIcon } from "lucide-react";
import {
  Deliverables,
  OpenWorkspaceFileExternal,
  ReadWorkspaceFile,
  ReadWorkspaceFileDataURL,
} from "../../wailsjs/go/main/App";
import { agent } from "../../wailsjs/go/models";

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function iconFor(type: string, path: string): string {
  const t = (type || "").toLowerCase();
  const p = (path || "").toLowerCase();
  if (t.includes("image") || /\.(png|jpe?g|gif|svg|webp)$/.test(p)) return "🖼️";
  if (/\.(md|markdown)$/.test(p)) return "📝";
  if (/\.(json|ya?ml|toml)$/.test(p)) return "🧩";
  if (/\.(csv|xlsx?|tsv)$/.test(p)) return "📊";
  if (/\.pdf$/.test(p)) return "📕";
  if (/\.(html?|css|tsx?|jsx?|go|py)$/.test(p)) return "💻";
  return "📄";
}

type ViewerKind = "text" | "pdf" | "image" | "binary";

const TEXT_RE =
  /\.(md|markdown|txt|json|ya?ml|toml|ini|csv|tsv|log|go|py|rb|rs|java|kt|c|h|cpp|cs|js|mjs|cjs|ts|tsx|jsx|css|scss|html?|xml|sh|bash|zsh|sql|env|gitignore)$/i;
const IMAGE_RE = /\.(png|jpe?g|gif|webp|bmp|svg|avif)$/i;

/** What the modal can actually show for a given file. */
function viewerKind(path: string): ViewerKind {
  if (/\.pdf$/i.test(path)) return "pdf";
  if (IMAGE_RE.test(path)) return "image";
  if (TEXT_RE.test(path) || !/\.[a-z0-9]+$/i.test(path)) return "text";
  return "binary";
}

const OPEN_KEY = "superai-deliverables-open";

/**
 * The files the current conversation produced, with a preview modal. Scoped to
 * a session id: switching conversations swaps the list, so a chat only shows
 * what it made rather than everything ever written to the workspace.
 */
export default function DeliverablesBar({
  sessionId,
  refreshKey,
}: {
  sessionId: string;
  /** Bump to re-read the list (e.g. when a turn finishes). */
  refreshKey?: number;
}) {
  const [items, setItems] = useState<agent.Deliverable[]>([]);
  const [open, setOpen] = useState(() => localStorage.getItem(OPEN_KEY) !== "0");
  const [viewer, setViewer] = useState<
    { path: string; kind: ViewerKind; content: string; url: string } | null
  >(null);
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(() => {
    Deliverables(sessionId)
      .then((d) => setItems(d || []))
      .catch(() => setItems([]));
  }, [sessionId]);

  useEffect(() => {
    refresh();
  }, [refresh, refreshKey]);

  useEffect(() => {
    localStorage.setItem(OPEN_KEY, open ? "1" : "0");
  }, [open]);

  const openFile = async (path: string) => {
    const kind = viewerKind(path);
    setViewer({ path, kind, content: "", url: "" });
    setLoading(true);
    try {
      if (kind === "text") {
        setViewer({ path, kind, content: await ReadWorkspaceFile(path), url: "" });
      } else if (kind === "pdf" || kind === "image") {
        // Binary formats the webview renders natively: hand them over as a
        // data: URL rather than stringifying the bytes into the modal.
        setViewer({ path, kind, content: "", url: await ReadWorkspaceFileDataURL(path) });
      } else {
        setViewer({ path, kind, content: "", url: "" });
      }
    } catch (e: any) {
      setViewer({
        path,
        kind: "text",
        content: `Failed to read file:\n${String(e?.message || e)}`,
        url: "",
      });
    } finally {
      setLoading(false);
    }
  };

  // Nothing produced yet: stay out of the way entirely.
  if (items.length === 0) return null;

  return (
    <>
      <div className="deliv-bar">
        <div className="trace-head">
          <button className="deliv-toggle" onClick={() => setOpen((v) => !v)}>
            {open ? <ChevronDownIcon className="size-3.5" /> : <ChevronUpIcon className="size-3.5" />}
            <span>Files ({items.length})</span>
          </button>
          <button className="btn ghost sm" onClick={refresh} title="Rescan the workspace">
            <RefreshCwIcon className="size-3.5" /> Refresh
          </button>
        </div>
        {open && (
          <div className="deliv-grid">
            {items.map((d) => (
              <button key={d.path} className="deliv-item" onClick={() => openFile(d.path)}>
                <span className="deliv-icon">{iconFor(d.type, d.path)}</span>
                <span className="deliv-meta">
                  <div className="deliv-name">{d.path.split("/").pop() || d.path}</div>
                  <div className="deliv-sub">{fmtSize(d.size)}</div>
                </span>
              </button>
            ))}
          </div>
        )}
      </div>

      {viewer && (
        <div className="modal-overlay" onClick={() => setViewer(null)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <span className="modal-title">{viewer.path}</span>
              <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <button
                  className="btn ghost sm"
                  onClick={() => OpenWorkspaceFileExternal(viewer.path)}
                  title="Open with the default app"
                >
                  Open externally
                </button>
                <button className="modal-close" onClick={() => setViewer(null)}>
                  ×
                </button>
              </span>
            </div>
            <div className="modal-body">
              {loading ? (
                <div className="loading-row">
                  <span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading…
                </div>
              ) : viewer.kind === "pdf" ? (
                <embed
                  src={viewer.url}
                  type="application/pdf"
                  style={{ width: "100%", height: "70vh", border: "none" }}
                />
              ) : viewer.kind === "image" ? (
                <img
                  src={viewer.url}
                  alt={viewer.path}
                  style={{ maxWidth: "100%", display: "block", margin: "0 auto" }}
                />
              ) : viewer.kind === "binary" ? (
                <div className="trace-empty">
                  Nothing to preview for this format.
                  <br />
                  Use “Open externally” to view it in the app that owns it.
                </div>
              ) : (
                <pre>{viewer.content}</pre>
              )}
            </div>
          </div>
        </div>
      )}
    </>
  );
}
