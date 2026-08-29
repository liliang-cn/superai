import React, { useCallback, useState } from "react";
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
 */
export default function ImportPanel() {
  const [open, setOpen] = useState(false);
  const [path, setPath] = useState("");
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
          className="input"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="/Users/me/contacts.csv — absolute path on the machine running SuperAI"
          autoComplete="off"
        />
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
