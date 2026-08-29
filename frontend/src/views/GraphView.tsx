import React, { useCallback, useEffect, useState } from "react";
import { ClipboardSetText } from "../../wailsjs/runtime";
import { GraphView as startGraphView } from "../../wailsjs/go/main/App";
import { openExternal } from "../lib/openExternal";

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

export default function GraphView() {
  const [status, setStatus] = useState<GraphStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [copied, setCopied] = useState(false);
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
        <div className="view-title">Knowledge Graph</div>
        <div className="view-desc">
          The live 3D view of the CortexDB brain SuperAI reads on every turn. Drag to rotate; it
          redraws itself as the graph changes.
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
        {status?.nodes === 0 && (
          <div className="graph-note">
            {brain} has no entities yet, so the scene is empty. It fills in on its own as SuperAI
            saves memories and draws relations.
          </div>
        )}
        <iframe
          key={`${GRAPH_SRC ?? status?.url}#${nonce}`}
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
