import React, { useCallback, useEffect, useState } from "react";
import { MCP } from "../../wailsjs/go/main/App";
import { mcp } from "../../wailsjs/go/models";

export default function MCPView() {
  const [servers, setServers] = useState<mcp.ServerStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const load = useCallback(async () => {
    setLoading(true);
    setErr("");
    try {
      const res = await MCP();
      setServers(Array.isArray(res) ? res : []);
    } catch (e: any) {
      setErr(String(e?.message || e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const toggle = (name: string) =>
    setExpanded((prev) => ({ ...prev, [name]: !prev[name] }));

  return (
    <div className="view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">MCP{servers.length > 0 ? ` (${servers.length})` : ""}</div>
          <div className="view-desc">Model Context Protocol servers SuperAI connects to for extra tools.</div>
        </div>
        <div className="vh-actions">
          <button className="btn ghost sm" onClick={load} disabled={loading}>
            {loading ? <><span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> Loading…</> : "↻ Refresh"}
          </button>
        </div>
      </div>

      <div className="panel-scroll">
        {err && <div className="report-error">⚠ {err}</div>}
        {!err && loading && servers.length === 0 && (
          <div className="loading-row">
            <span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading MCP servers…
          </div>
        )}
        {!err && !loading && servers.length === 0 && (
          <div className="inline-empty">
            <div className="ie-icon">🔌</div>
            <div>No MCP servers.</div>
            <div className="ie-hint">
              Add them in ~/.superai-desktop/mcpServers.json (standard {"{"}"mcpServers": {"{...}"}{"}"} format) and restart.
            </div>
          </div>
        )}
        {!err && servers.length > 0 && (
          <div className="record-list">
            {servers.map((s) => {
              const tools = Array.isArray(s.tools) ? s.tools : [];
              const hasTools = tools.length > 0;
              const isOpen = !!expanded[s.name];
              return (
                <div className="record-card" key={s.name}>
                  <div
                    className="rc-title"
                    style={{ display: "flex", alignItems: "center", gap: 8, cursor: hasTools ? "pointer" : "default", marginBottom: s.description || s.command ? 6 : 0 }}
                    onClick={hasTools ? () => toggle(s.name) : undefined}
                  >
                    <span className={`status-dot ${s.running ? "ok" : "bad"}`} />
                    <span>{s.name}</span>
                    <span style={{ color: "var(--text-3)", fontWeight: 400, fontSize: 12 }}>
                      {s.tool_count} {s.tool_count === 1 ? "tool" : "tools"}
                    </span>
                    {hasTools && (
                      <span style={{ color: "var(--text-3)", fontWeight: 400, fontSize: 12, marginLeft: "auto" }}>
                        {isOpen ? "▾" : "▸"}
                      </span>
                    )}
                  </div>
                  {s.description && (
                    <div className="rc-row">
                      <span className="rc-val">{s.description}</span>
                    </div>
                  )}
                  {s.command && (
                    <div className="rc-row" style={{ marginTop: 4 }}>
                      <span className="rc-val" style={{ fontFamily: "var(--font-mono)", color: "var(--text-2)", fontSize: 12 }}>
                        {s.command}
                      </span>
                    </div>
                  )}
                  {hasTools && isOpen && (
                    <div className="rc-json" style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                      {tools.map((t) => (
                        <div key={t.name}>
                          <span style={{ color: "var(--text-1)", fontWeight: 600 }}>{t.name}</span>
                          {t.description && (
                            <span style={{ color: "var(--text-2)" }}> — {t.description}</span>
                          )}
                        </div>
                      ))}
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
