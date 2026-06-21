import React, { useCallback, useEffect, useState } from "react";
import { Skills } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";

export default function SkillsView() {
  const [skills, setSkills] = useState<backend.SkillInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");

  const load = useCallback(async () => {
    setLoading(true);
    setErr("");
    try {
      const res = await Skills();
      setSkills(Array.isArray(res) ? res : []);
    } catch (e: any) {
      setErr(String(e?.message || e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <div className="view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">Skills{skills.length > 0 ? ` (${skills.length})` : ""}</div>
          <div className="view-desc">Installed skills SuperAI can activate during a turn.</div>
        </div>
        <div className="vh-actions">
          <button className="btn ghost sm" onClick={load} disabled={loading}>
            {loading ? <><span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> Loading…</> : "↻ Refresh"}
          </button>
        </div>
      </div>

      <div className="panel-scroll">
        {err && <div className="report-error">⚠ {err}</div>}
        {!err && loading && skills.length === 0 && (
          <div className="loading-row">
            <span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading skills…
          </div>
        )}
        {!err && !loading && skills.length === 0 && (
          <div className="inline-empty">
            <div className="ie-icon">🧩</div>
            <div>No skills installed.</div>
            <div className="ie-hint">Drop SKILL.md folders in ~/.agentgo/skills.</div>
          </div>
        )}
        {!err && skills.length > 0 && (
          <div className="record-list">
            {skills.map((s) => {
              const whenToUse = (s.when_to_use ?? "").trim();
              return (
                <div className="record-card" key={s.id || s.name}>
                  <div className="rc-title">
                    {s.name || s.id}
                    {s.id && s.id !== s.name && (
                      <span style={{ color: "var(--text-3)", fontWeight: 400, fontFamily: "var(--font-mono)", fontSize: 12, marginLeft: 8 }}>
                        {s.id}
                      </span>
                    )}
                    {s.collection && (
                      <span className="chip" style={{ marginLeft: 8, verticalAlign: "middle" }}>{s.collection}</span>
                    )}
                  </div>
                  {s.description && (
                    <div className="rc-row">
                      <span className="rc-val">{s.description}</span>
                    </div>
                  )}
                  {whenToUse !== "" && (
                    <div className="rc-row" style={{ marginTop: 4 }}>
                      <span className="rc-val" style={{ color: "var(--text-2)", fontSize: 12 }}>Use when: {whenToUse}</span>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
