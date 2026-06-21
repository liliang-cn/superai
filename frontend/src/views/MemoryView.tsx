import React, { useCallback, useState } from "react";
import { MemoryRecall } from "../../wailsjs/go/main/App";

export default function MemoryView() {
  const [query, setQuery] = useState("");
  const [recall, setRecall] = useState<string>("");
  const [searched, setSearched] = useState(false);
  const [searching, setSearching] = useState(false);
  const [recallErr, setRecallErr] = useState<string>("");

  const search = useCallback(async () => {
    const q = query.trim();
    if (!q || searching) return;
    setSearching(true);
    setRecallErr("");
    try {
      const res = await MemoryRecall(q);
      setRecall(res ?? "");
      setSearched(true);
    } catch (e: any) {
      setRecallErr(String(e?.message || e));
      setSearched(true);
    } finally {
      setSearching(false);
    }
  }, [query, searching]);

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      search();
    }
  };

  const recallEmpty = searched && !recallErr && recall.trim() === "";

  return (
    <div className="view">
      <div className="view-header">
        <div className="view-title">Memory</div>
        <div className="view-desc">Search what SuperAI remembers in long-term memory.</div>
      </div>

      <div className="panel-scroll">
        <div className="panel-grid">
          <div className="card">
            <div className="card-title">Recall</div>
            <div className="card-desc">Search long-term memory. Returns nothing if no embedding model is configured.</div>
            <div className="search-row">
              <input
                className="input"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={onKeyDown}
                placeholder="e.g. what do I know about Alice?"
                autoComplete="off"
              />
              <button className="btn" onClick={search} disabled={searching || query.trim() === ""}>
                {searching ? <><span className="spinner" /> Searching…</> : "Recall"}
              </button>
            </div>

            {recallErr && <div className="report-error" style={{ marginTop: 14 }}>⚠ {recallErr}</div>}

            {!recallErr && searched && (
              <div style={{ marginTop: 14 }}>
                {recallEmpty ? (
                  <div className="inline-empty">
                    <div className="ie-icon">🧠</div>
                    <div>No memories matched.</div>
                    <div className="ie-hint">Either nothing was found, or memory is not configured (no embedding model).</div>
                  </div>
                ) : (
                  <div className="prose-panel">{recall}</div>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
