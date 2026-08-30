import React, { useCallback, useRef, useState } from "react";
import { ImportCSV } from "../../wailsjs/go/main/App";

/**
 * Feeding the brain, on the page that shows it.
 *
 * This used to be a sidebar entry called Data, which said nothing about what it
 * was for. What it is for is putting a table into the graph on this page — so
 * it lives here, collapsed, and the graph above it fills in on its own once the
 * import lands. The cause and the effect are in one place.
 *
 * Kept behind a disclosure because importing is rare and looking is not: an
 * upload form permanently occupying the top of the page would push the thing
 * people came for down the screen.
 *
 * The disclosure can be driven from outside (props.open / props.onOpenChange)
 * so the trigger can live in the page header as an icon rather than as a link
 * occupying a row of its own. Uncontrolled, it keeps its own link trigger.
 */
export type ImportPanelProps = {
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
};

export default function ImportPanel({ open: openProp, onOpenChange }: ImportPanelProps = {}) {
  const [openState, setOpenState] = useState(false);
  const controlled = openProp !== undefined;
  const open = controlled ? openProp : openState;
  const setOpen = (v: boolean) => {
    if (!controlled) setOpenState(v);
    onOpenChange?.(v);
  };
  // The server-side path of the file to import. Filled by uploading, never
  // typed: a path is only meaningful on the machine SuperAI runs on, and over
  // the network the obvious thing to type — a path on your own laptop — is the
  // one thing that cannot work.
  const [path, setPath] = useState("");
  const [picked, setPicked] = useState("");
  const [uploading, setUploading] = useState(false);
  const fileRef = useRef<HTMLInputElement | null>(null);
  const [hint, setHint] = useState("");
  const [running, setRunning] = useState(false);
  const [report, setReport] = useState("");
  const [done, setDone] = useState(false);

  const run = useCallback(async () => {
    const p = path.trim();
    if (!p || running) return;
    setRunning(true);
    setReport("");
    setDone(false);
    try {
      setReport((await ImportCSV(p, hint.trim())) ?? "");
      setDone(true);
    } catch (e: any) {
      setReport("error: " + String(e?.message || e));
      setDone(true);
    } finally {
      setRunning(false);
    }
  }, [path, hint, running]);

  const upload = useCallback(async (f: File) => {
    setUploading(true);
    setReport("");
    setDone(false);
    try {
      const form = new FormData();
      form.append("file", f);
      const res = await fetch("/api/upload", { method: "POST", body: form });
      if (!res.ok) throw new Error((await res.text()).trim() || `upload failed (${res.status})`);
      const out = await res.json();
      setPath(out.path);
      setPicked(`${out.name} · ${Math.max(1, Math.round(out.bytes / 1024))} KB`);
    } catch (e: any) {
      setReport("error: " + String(e?.message || e));
      setDone(true);
      setPath("");
      setPicked("");
    } finally {
      setUploading(false);
    }
  }, []);

  const isError = report.startsWith("error:") || report === "backend not ready";
  let pretty = report;
  if (!isError && report.trim() !== "") {
    try {
      pretty = JSON.stringify(JSON.parse(report), null, 2);
    } catch {
      pretty = report;
    }
  }

  if (!open) {
    // Controlled: the page is showing its own trigger, so show nothing here.
    if (controlled) return null;
    return (
      <button className="link-btn import-toggle" onClick={() => setOpen(true)}>
        + Import a file into this graph
      </button>
    );
  }

  return (
    <div className="import-panel">
      <div className="import-head">
        <span className="card-title">Import into this graph</span>
        <button className="link-btn" onClick={() => setOpen(false)}>
          Close
        </button>
      </div>
      {/* The formats are stated rather than discovered on submit. The list is
          backend.ImportableExtensions; a file type nothing parses is refused by
          name rather than read as CSV and turned into a column of garbage. */}
      <div className="card-desc">
        A CSV/TSV table or a MySQL / PostgreSQL dump (<code>.csv .tsv .sql .dump</code>). Rows
        become searchable text and entities in the graph above; the column-to-field mapping is
        inferred, and the hint is what settles an ambiguous column name.
      </div>
      <div className="search-row">
        <input
          ref={fileRef}
          type="file"
          accept=".csv,.tsv,.txt,.sql,.dump"
          style={{ display: "none" }}
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) void upload(f);
            // Cleared so choosing the same file twice still fires a change.
            e.target.value = "";
          }}
        />
        <button className="btn ghost" onClick={() => fileRef.current?.click()} disabled={uploading}>
          {uploading ? (
            <>
              <span className="spinner" /> Uploading…
            </>
          ) : (
            "Choose a file…"
          )}
        </button>
        <span className="import-picked">{picked || "No file chosen"}</span>
      </div>
      <div className="search-row" style={{ marginTop: 8 }}>
        <input
          className="input"
          value={hint}
          onChange={(e) => setHint(e.target.value)}
          placeholder="Optional: what this dataset is — e.g. customer contacts, sales orders"
          autoComplete="off"
        />
        <button className="btn" onClick={run} disabled={running || path.trim() === ""}>
          {running ? (
            <>
              <span className="spinner" /> Importing…
            </>
          ) : (
            "Import"
          )}
        </button>
      </div>
      {done && (
        <div style={{ marginTop: 10 }}>
          {isError ? (
            <div className="report-error">⚠ {report}</div>
          ) : (
            <pre className="import-report">{pretty}</pre>
          )}
        </div>
      )}
    </div>
  );
}
