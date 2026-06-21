import React, { useCallback, useState } from "react";
import { ImportCSV } from "../../wailsjs/go/main/App";

export default function DataView() {
  const [path, setPath] = useState("");
  const [hint, setHint] = useState("");
  const [running, setRunning] = useState(false);
  const [report, setReport] = useState<string>("");
  const [done, setDone] = useState(false);

  const run = useCallback(async () => {
    const p = path.trim();
    if (!p || running) return;
    setRunning(true);
    setReport("");
    setDone(false);
    try {
      const res = await ImportCSV(p, hint.trim());
      setReport(res ?? "");
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

  return (
    <div className="view">
      <div className="view-header">
        <div className="view-title">Data</div>
        <div className="view-desc">Import a CSV into memory so SuperAI can reason over it.</div>
      </div>

      <div className="panel-scroll">
        <div className="panel-grid">
          <div className="card">
            <div className="card-title">Import CSV</div>
            <div className="card-desc">
              Imports a CSV file into the RAG vector store and knowledge graph. The column-to-field
              mapping is inferred by the LLM; an optional domain hint helps it map ambiguous columns.
            </div>

            <div className="field">
              <label>CSV file path</label>
              <input
                className="input"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                placeholder="/Users/me/contacts.csv"
                autoComplete="off"
              />
              <span className="hint">Absolute path to a .csv file on this machine.</span>
            </div>

            <div className="field">
              <label>Domain hint <span style={{ color: "var(--text-3)", fontWeight: 400 }}>(optional)</span></label>
              <input
                className="input"
                value={hint}
                onChange={(e) => setHint(e.target.value)}
                placeholder="e.g. customer contacts, sales orders, product catalog"
                autoComplete="off"
              />
              <span className="hint">Describes the dataset so the LLM maps columns more accurately.</span>
            </div>

            <div className="settings-actions" style={{ maxWidth: "none" }}>
              <button className="btn" onClick={run} disabled={running || path.trim() === ""}>
                {running ? <><span className="spinner" /> Importing…</> : "Import to memory"}
              </button>
            </div>
          </div>

          {done && (
            <div className="card">
              <div className="card-title">Import report</div>
              {isError ? (
                <div className="report-error">⚠ {report}</div>
              ) : report.trim() === "" ? (
                <div className="card-desc" style={{ marginBottom: 0 }}>Import finished with no report.</div>
              ) : (
                <div className="prose-panel">{pretty}</div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
