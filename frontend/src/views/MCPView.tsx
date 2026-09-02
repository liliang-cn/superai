import React, { useCallback, useEffect, useState } from "react";
import { InstallMCPServer, MCP, RemoveMCPServer, SearchMCPServers } from "../../wailsjs/go/main/App";
import { mcp } from "../../wailsjs/go/models";
import { useImeGuard } from "@/lib/ime";
import { toast } from "../lib/toasts";

/** One installable server from the registry, as SearchMCPServers returns it. */
interface Candidate {
  name: string;
  title?: string;
  description: string;
  command?: string;
  args?: string[];
  required_env?: string[];
  remote_url?: string;
}

/** Turn a registry name like "io.github.foo/bar" into a usable server name. */
function shortName(full: string): string {
  const tail = full.split("/").pop() || full;
  return tail.replace(/[^A-Za-z0-9._-]/g, "-");
}

export default function MCPView() {
  const ime = useImeGuard();
  const [servers, setServers] = useState<mcp.ServerStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<Candidate[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [busy, setBusy] = useState("");
  const [note, setNote] = useState("");
  const [confirmRemove, setConfirmRemove] = useState("");

  const search = useCallback(async () => {
    setSearching(true);
    setNote("");
    try {
      const res: any = await SearchMCPServers(query);
      if (res?.error) setNote(res.error);
      setResults(Array.isArray(res?.servers) ? res.servers : []);
    } catch (e: any) {
      setNote(String(e?.message || e));
      setResults([]);
    } finally {
      setSearching(false);
    }
  }, [query]);

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
          <button className="btn ghost sm" onClick={() => (results ? setResults(null) : search())}>
            {results ? "Close" : "+ Add server"}
          </button>
          <button className="btn ghost sm" onClick={load} disabled={loading}>
            {loading ? <><span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> Loading…</> : "↻ Refresh"}
          </button>
        </div>
      </div>

      <div className="panel-scroll">
        {results !== null && (
          <div className="card" style={{ marginBottom: 14 }}>
            <div className="card-title">Add an MCP server</div>
            <div className="card-desc">
              Search the official registry. The launch command comes from the registry — no need to know the package name.
            </div>
            <div style={{ display: "flex", gap: 8 }}>
              <input
                className="input"
                value={query}
                autoFocus
                placeholder="What should it be able to do? e.g. postgres, slack, filesystem"
                onChange={(e) => setQuery(e.target.value)}
                onCompositionStart={ime.handlers.onCompositionStart}
                onCompositionEnd={ime.handlers.onCompositionEnd}
                onKeyDown={(e) => e.key === "Enter" && !ime.composing(e) && search()}
              />
              <button className="btn sm" onClick={search} disabled={searching}>
                {searching ? "Searching…" : "Search"}
              </button>
            </div>
            {note && <div className="hint err" style={{ marginTop: 8 }}>{note}</div>}
            <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 10 }}>
              {results.length === 0 && !searching && (
                <div className="trace-empty">No servers matched.</div>
              )}
              {results.map((c) => {
                const name = shortName(c.name);
                const installed = servers.some((s) => s.name === name);
                return (
                  <div key={c.name} className="record-card">
                    <div className="rc-title" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                      <span>{c.title || name}</span>
                      <span style={{ color: "var(--text-3)", fontWeight: 400, fontSize: 12 }}>{c.name}</span>
                      <span style={{ marginLeft: "auto" }}>
                        <button
                          className="btn ghost sm"
                          disabled={!c.command || installed || busy === c.name}
                          title={c.command ? "" : "This server is hosted remotely; add it by URL instead."}
                          onClick={async () => {
                            setBusy(c.name);
                            const res = await InstallMCPServer(name, c.command || "", c.args || [], {});
                            setBusy("");
                            if (res === "ok") toast.success(`Installed ${name}.`);
                            else toast.error(res);
                            load();
                          }}
                        >
                          {installed ? "Installed" : busy === c.name ? "Installing…" : "Install"}
                        </button>
                      </span>
                    </div>
                    {c.description && (
                      <div className="rc-row"><span className="rc-val">{c.description}</span></div>
                    )}
                    {c.command && (
                      <div className="rc-row" style={{ marginTop: 4 }}>
                        <span className="rc-val" style={{ fontFamily: "var(--font-mono)", color: "var(--text-2)", fontSize: 12 }}>
                          {c.command} {(c.args || []).join(" ")}
                        </span>
                      </div>
                    )}
                    {!!c.required_env?.length && (
                      <div className="hint" style={{ marginTop: 4 }}>
                        Needs {c.required_env.join(", ")} — set it in mcpServers.json after installing.
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        )}
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
                    <span style={{ marginLeft: "auto", display: "flex", alignItems: "center", gap: 8 }}>
                      {hasTools && (
                        <span style={{ color: "var(--text-3)", fontWeight: 400, fontSize: 12 }}>
                          {isOpen ? "▾" : "▸"}
                        </span>
                      )}
                      <button
                        className="btn ghost sm"
                        onClick={(e) => {
                          e.stopPropagation();
                          if (confirmRemove !== s.name) {
                            setConfirmRemove(s.name);
                            return;
                          }
                          setConfirmRemove("");
                          RemoveMCPServer(s.name).then((res) => {
                            if (res === "ok") toast.success(`Removed ${s.name}. It stops on the next launch.`);
                            else toast.error(res);
                            load();
                          });
                        }}
                      >
                        {confirmRemove === s.name ? "Confirm" : "Remove"}
                      </button>
                    </span>
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
