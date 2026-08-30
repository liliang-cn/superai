import React, { useCallback, useEffect, useRef, useState } from "react";
import { ClipboardSetText } from "../../wailsjs/runtime";
import { GraphView as startGraphView, MemoryRecall } from "../../wailsjs/go/main/App";
import { openExternal } from "../lib/openExternal";
import ImportPanel from "../components/ImportPanel";
import { useImeGuard } from "@/lib/ime";

/**
 * One page for what SuperAI knows.
 *
 * Recall and the graph used to be two entries in the sidebar, and they read the
 * same CortexDB — one asks it by name, the other shows the whole thing. Kept
 * apart, using them together meant going back and forth: spot a node worth
 * understanding and leave to search for it; find a memory and leave to see what
 * it connects to. They are two views of one question, so they are one page.
 *
 * Searching also drives the graph. The 3D view already has a Find box; typing
 * here reaches into it, so a recall lights up the nodes it is about instead of
 * leaving the reader to spot them.
 */

/** What the backend says about the running view. See App.GraphView. */
interface GraphStatus {
  url: string;
  source: string;
  backend: string;
  nodes: number;
  edges: number;
  activity: boolean;
  error: string;
}

function normalize(raw: Record<string, any> | null): GraphStatus {
  return {
    url: String(raw?.url ?? ""),
    source: String(raw?.source ?? ""),
    backend: String(raw?.backend ?? "local"),
    nodes: Number(raw?.nodes ?? 0),
    edges: Number(raw?.edges ?? 0),
    activity: Boolean(raw?.activity),
    error: String(raw?.error ?? ""),
  };
}

/**
 * Where to point the frame.
 *
 * CortexDB binds the live view to 127.0.0.1 of the machine SuperAI runs on, and
 * there is deliberately no option to widen that — the page is the whole brain
 * with no authentication in front of it.
 *
 * That address is only reachable from the browser when the browser is on that
 * machine, which inside the desktop window it always is. Over HTTP it usually
 * is not: a tab open at a domain would resolve 127.0.0.1 to the viewer's own
 * laptop, where nothing is listening.
 *
 * This used to be handled by not drawing a frame at all and explaining the
 * situation, which is honest and is not the feature working — and served over
 * a domain is how SuperAI is actually used. So SuperAI proxies the view
 * instead: it runs on that host, it can reach loopback, and it has the front
 * door the view has none of. See backend/graphproxy.go.
 */
const SERVED = Boolean((window as unknown as Record<string, unknown>).superaiServed);
// Same origin as this page, so the session cookie rides along and the proxy's
// authentication applies to the frame exactly as it does to everything else.
const GRAPH_SRC = SERVED ? "/graph/" : null;

export default function KnowledgeView() {
  const ime = useImeGuard();
  const [status, setStatus] = useState<GraphStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [copied, setCopied] = useState(false);
  const frameRef = useRef<HTMLIFrameElement | null>(null);

  // Recall, which used to be the Memory page.
  const [query, setQuery] = useState("");
  const [recall, setRecall] = useState("");
  const [searched, setSearched] = useState(false);
  const [searching, setSearching] = useState(false);
  const [recallErr, setRecallErr] = useState("");
  // Bumped to force the iframe to remount, which is the only way to reload a
  // cross-origin frame from outside it.
  const [nonce, setNonce] = useState(0);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setStatus(normalize(await startGraphView()));
    } catch (e: any) {
      setStatus(normalize({ error: String(e?.message || e) }));
    } finally {
      setLoading(false);
    }
  }, []);

  // The view starts here, on the first render of this page — not at app boot.
  useEffect(() => {
    load();
  }, [load]);

  // An empty brain and a brain that has not been read yet look identical, so
  // keep asking until something shows up. Once there are nodes the page's own
  // event stream keeps it current and this stops.
  useEffect(() => {
    if (!status || status.error || status.nodes > 0) return;
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [status, load]);

  /**
   * Light the query up on the graph.
   *
   * Sent as a message rather than by reaching into the frame's DOM: the frame
   * is a different origin over HTTP (SuperAI proxies it), so touching its
   * document would throw. The view ignores messages it does not understand, so
   * an older CortexDB simply does not highlight — the recall still works.
   */
  // The graph's find is a substring match over node labels, and a recall query
  // is a question — "AI Agent" is not the name of anything, so forwarding it
  // verbatim lit up nothing while the graph sat full of agent-go, oss-agent and
  // the rest. The panel promises "the same search lights up the nodes it is
  // about"; sending the query's most distinctive word is what makes that true.
  //
  // Longest word, because that is the one carrying the meaning: "AI Agent" ->
  // "Agent", "what do I know about superai" -> "superai". A query with no
  // spaces (any CJK one, and most single terms) is already its own best term
  // and goes through unchanged.
  const highlightTerm = (q: string) => {
    const words = q.split(/[\s,.;:!?()[\]{}"'`]+/).filter(Boolean);
    if (words.length < 2) return q;
    return words.reduce((best, w) => (w.length > best.length ? w : best), "");
  };

  const highlight = useCallback((q: string) => {
    const w = frameRef.current?.contentWindow;
    if (!w) return;
    try {
      w.postMessage({ type: "cortexdb:highlight", query: q ? highlightTerm(q) : "" }, "*");
    } catch {
      // A frame that will not take a message is not a reason to fail a search.
    }
  }, []);

  const search = useCallback(async () => {
    const q = query.trim();
    if (!q || searching) return;
    setSearching(true);
    setRecallErr("");
    highlight(q);
    try {
      setRecall((await MemoryRecall(q)) ?? "");
      setSearched(true);
    } catch (e: any) {
      setRecallErr(String(e?.message || e));
      setSearched(true);
    } finally {
      setSearching(false);
    }
  }, [query, searching, highlight]);

  const clearSearch = useCallback(() => {
    setQuery("");
    setRecall("");
    setSearched(false);
    setRecallErr("");
    highlight("");
  }, [highlight]);

  const copy = (url: string) => {
    ClipboardSetText(url).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };

  const brain =
    status?.backend === "shared" ? "Shared brain" : "Local brain";

  const header = (
    <div className="view-header with-action">
      <div>
        <div className="view-title">Knowledge</div>
        <div className="view-desc">
          Everything SuperAI knows, as one graph it reads on every turn. Search to recall it in
          words; the same search lights up the nodes it is about.
        </div>
      </div>
      {status?.url && (
        <div className="vh-actions">
          <span className="graph-meta">
            {status.nodes} nodes · {status.edges} edges
          </span>
          <button className="btn ghost sm" onClick={() => setNonce((n) => n + 1)}>
            Reload
          </button>
          <button className="btn sm" onClick={() => openExternal(status.url)}>
            Open ↗
          </button>
        </div>
      )}
    </div>
  );

  let body: React.ReactNode;
  if (loading && !status) {
    body = (
      <div className="loading-row">
        <span className="spinner" /> Starting the view…
      </div>
    );
  } else if (status?.error) {
    body = (
      <div className="panel-scroll">
        <div className="panel-grid">
          <div className="card">
            <div className="card-title">{brain} unavailable</div>
            <div className="report-error">⚠ {status.error}</div>
            <div className="card-desc" style={{ marginTop: 12 }}>
              {status.backend === "shared"
                ? "The shared brain is configured under Settings → Memory. Check the endpoint is right and the CortexDB server is running and new enough to answer graph_list_all."
                : "The local brain lives in this machine's SuperAI data directory. If it will not open, another process may be holding it."}
            </div>
            <div style={{ marginTop: 14 }}>
              <button className="btn" onClick={load} disabled={loading}>
                {loading ? (
                  <>
                    <span className="spinner" /> Retrying…
                  </>
                ) : (
                  "Try again"
                )}
              </button>
            </div>
          </div>
        </div>
      </div>
    );
  } else {
    body = (
      <div className="graph-stage">
        <div className="knowledge-search">
          <input
            className="input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onCompositionStart={ime.handlers.onCompositionStart}
            onCompositionEnd={ime.handlers.onCompositionEnd}
            onKeyDown={(e) => {
              // Enter mid-composition accepts a candidate. preventDefault here
              // would swallow that keystroke and search for a half-typed query.
              if (e.key === "Enter" && !ime.composing(e)) {
                e.preventDefault();
                search();
              }
              if (e.key === "Escape") clearSearch();
            }}
            placeholder="Recall — e.g. what do I know about Alice?"
            autoComplete="off"
          />
          <button className="btn" onClick={search} disabled={searching || query.trim() === ""}>
            {searching ? (
              <>
                <span className="spinner" /> Searching…
              </>
            ) : (
              "Recall"
            )}
          </button>
          {(searched || query !== "") && (
            <button className="btn ghost" onClick={clearSearch}>
              Clear
            </button>
          )}
        </div>

        <ImportPanel />

        {recallErr && <div className="report-error">⚠ {recallErr}</div>}
        {!recallErr && searched && (
          <div className="knowledge-recall">
            {recall.trim() === "" ? (
              <div className="graph-note">
                Nothing matched. Either the brain holds nothing about that, or recall is
                unavailable because no embedding model is configured.
              </div>
            ) : (
              <div className="prose-panel">{recall}</div>
            )}
          </div>
        )}

        {status?.nodes === 0 && (
          <div className="graph-note">
            {brain} has no entities yet, so the scene is empty. It fills in on its own as SuperAI
            saves memories and draws relations.
          </div>
        )}
        <iframe
          key={`${GRAPH_SRC ?? status?.url}#${nonce}`}
          ref={frameRef}
          className="graph-frame"
          src={GRAPH_SRC ?? status?.url}
          title="CortexDB knowledge graph"
        />
        <div className="graph-foot">
          <span className="graph-source">{status?.source}</span>
          <span>
            {status?.activity
              ? "Tool calls light up the nodes they name."
              : "Structure only — SuperAI's tool calls do not feed this view's ticker."}
          </span>
          <button className="link-btn" onClick={() => status?.url && openExternal(status.url)}>
            Not drawing? Open it in a browser ↗
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="view">
      {header}
      {body}
    </div>
  );
}
