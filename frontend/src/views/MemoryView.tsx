import React, { useCallback, useEffect, useState } from "react";
import { MemoryRecall, MemorySkills } from "../../wailsjs/go/main/App";

export default function MemoryView() {
  const [query, setQuery] = useState("");
  const [recall, setRecall] = useState<string>("");
  const [searched, setSearched] = useState(false);
  const [searching, setSearching] = useState(false);
  const [recallErr, setRecallErr] = useState<string>("");

  const [skills, setSkills] = useState<string[]>([]);
  const [skillsLoading, setSkillsLoading] = useState(true);
  const [skillsErr, setSkillsErr] = useState<string>("");

  const loadSkills = useCallback(async () => {
    setSkillsLoading(true);
    setSkillsErr("");
    try {
      const res = await MemorySkills();
      setSkills(Array.isArray(res) ? res : []);
    } catch (e: any) {
      setSkillsErr(String(e?.message || e));
    } finally {
      setSkillsLoading(false);
    }
  }, []);

  useEffect(() => {
    loadSkills();
  }, [loadSkills]);

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
        <div className="view-desc">Search what SuperAI remembers and review the skills it can call on.</div>
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

          <div className="card">
            <div className="card-title">Skills</div>
            <div className="card-desc">Installed skill IDs SuperAI can activate during a turn.</div>
            {skillsLoading ? (
              <div className="loading-row" style={{ padding: 0 }}>
                <span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading skills…
              </div>
            ) : skillsErr ? (
              <div className="report-error">⚠ {skillsErr}</div>
            ) : skills.length === 0 ? (
              <div className="inline-empty">
                <div className="ie-icon">🧩</div>
                <div>No skills installed.</div>
                <div className="ie-hint">Drop SKILL.md folders into the skills directory to add capabilities.</div>
              </div>
            ) : (
              <div className="chips">
                {skills.map((s) => (
                  <span className="chip" key={s}>{s}</span>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
