import React, { useCallback, useEffect, useState } from "react";
import { InstallSkill, RemoveSkill, SearchSkills, Skills } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";

export default function SkillsView() {
  const [skills, setSkills] = useState<backend.SkillInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");
  const [query, setQuery] = useState("");
  const [available, setAvailable] = useState<backend.SkillCandidate[] | null>(null);
  const [busy, setBusy] = useState("");
  const [note, setNote] = useState("");
  const [confirmRemove, setConfirmRemove] = useState("");

  const browse = useCallback(async (q: string) => {
    setNote("");
    try {
      setAvailable(await SearchSkills(q));
    } catch (e: any) {
      setNote(String(e?.message || e));
      setAvailable([]);
    }
  }, []);

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
          <button
            className="btn ghost sm"
            onClick={() => (available ? setAvailable(null) : browse(query))}
          >
            {available ? "Close" : "+ Add skill"}
          </button>
          <button className="btn ghost sm" onClick={load} disabled={loading}>
            {loading ? <><span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> Loading…</> : "↻ Refresh"}
          </button>
        </div>
      </div>

      <div className="panel-scroll">
        {available !== null && (
          <div className="card" style={{ marginBottom: 14 }}>
            <div className="card-title">Add a skill</div>
            <div className="card-desc">
              Skills already on this machine, including the ones Claude Code uses (~/.claude/skills).
            </div>
            <input
              className="input"
              value={query}
              autoFocus
              placeholder="Filter by name or what it does…"
              onChange={(e) => {
                setQuery(e.target.value);
                browse(e.target.value);
              }}
            />
            {note && <div className="hint err" style={{ marginTop: 8 }}>{note}</div>}
            <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 10 }}>
              {available.length === 0 && <div className="trace-empty">Nothing found on this machine.</div>}
              {available.map((c) => (
                <div key={c.name} className="record-card">
                  <div className="rc-title" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <span>{c.name}</span>
                    <span style={{ marginLeft: "auto" }}>
                      <button
                        className="btn ghost sm"
                        disabled={c.installed || busy === c.name}
                        onClick={async () => {
                          setBusy(c.name);
                          const res = await InstallSkill(c.name, c.path);
                          setBusy("");
                          if (res === "ok") toast.success(`Installed ${c.name}.`);
                            else toast.error(res);
                          load();
                          browse(query);
                        }}
                      >
                        {c.installed ? "Installed" : busy === c.name ? "Installing…" : "Install"}
                      </button>
                    </span>
                  </div>
                  {c.description && (
                    <div className="rc-row">
                      <span className="rc-val" style={{ fontSize: 12 }}>{c.description}</span>
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
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
            <div className="ie-hint">Use “+ Add skill” to install one already on this machine.</div>
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
                    <button
                      className="btn ghost sm"
                      style={{ float: "right" }}
                      onClick={() => {
                        const id = s.id || s.name;
                        if (confirmRemove !== id) {
                          setConfirmRemove(id);
                          return;
                        }
                        setConfirmRemove("");
                        RemoveSkill(id).then((res) => {
                          if (res === "ok") toast.success(`Removed ${id}.`);
                          else toast.error(res);
                          load();
                          if (available) browse(query);
                        });
                      }}
                    >
                      {confirmRemove === (s.id || s.name) ? "Confirm" : "Remove"}
                    </button>
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
