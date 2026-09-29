import React, { useCallback, useEffect, useState } from "react";
import { HiveStatus } from "../../wailsjs/go/app/App";

/** One worker as HiveStatus reports it. */
interface Member {
  name: string;
  url: string;
  version?: string;
  state: "live" | "lost";
  joined_at: string;
  last_seen: string;
}

interface QueenLink {
  url: string;
  joined: boolean;
  last_ok: string;
  error: string;
}

interface Status {
  role: "" | "queen" | "worker";
  name: string;
  protocol: string;
  interval_ms?: number;
  members: Member[];
  queen?: QueenLink;
}

/** How long ago, in the units a heartbeat is read in. A minute-granularity
 *  "less than a minute" (see lib/format.ts) would hide the one thing this panel
 *  is for: whether a worker spoke a moment ago or has gone quiet. */
function ago(iso: string, now: number): string {
  const t = Date.parse(iso);
  if (!t || t < 0 || iso.startsWith("0001")) return "never";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export default function HiveView() {
  const [st, setSt] = useState<Status | null>(null);
  const [err, setErr] = useState("");
  const [now, setNow] = useState(Date.now());

  const load = useCallback(async () => {
    try {
      setSt((await HiveStatus()) as unknown as Status);
      setErr("");
    } catch (e: any) {
      setErr(String(e?.message || e));
    }
  }, []);

  // The roster is polled, not pushed: a worker going quiet is the absence of an
  // event, and there is nothing to push for that. Three seconds is under the
  // default heartbeat, so a change shows on the next look rather than the one
  // after.
  useEffect(() => {
    load();
    const poll = window.setInterval(load, 3000);
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => {
      window.clearInterval(poll);
      window.clearInterval(tick);
    };
  }, [load]);

  const members = st?.members ?? [];
  const live = members.filter((m) => m.state === "live").length;

  return (
    <div className="view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">Hive</div>
          <div className="view-desc">
            SuperAI instances sharing one memory. The queen gives the orders; workers announce themselves and carry them out.
          </div>
        </div>
      </div>

      <div className="panel-scroll">
        {err && <div className="hint err">{err}</div>}
        {st && (
          <>
            <div className="card hive-self">
              <span className={`hive-role ${st.role || "alone"}`}>{st.role || "standalone"}</span>
              <div className="hive-self-body">
                <div className="hive-self-name">{st.name || "this instance"}</div>
                <div className="hive-dim">
                  {st.role === "queen" && `Accepting workers · ${live} live of ${members.length}`}
                  {st.role === "worker" && "Announces itself to the queen and obeys her"}
                  {st.role === "" && "Working on its own. Nothing here is required."}
                </div>
              </div>
              <span className="chip">{st.protocol}</span>
            </div>

            {st.role === "worker" && st.queen && (
              <div className="card hive-link">
                <span className={`status-dot ${st.queen.joined ? "ok" : "bad"}`} />
                <div className="hive-self-body">
                  <div className="hive-self-name">
                    {st.queen.joined ? "Joined the queen" : "Not joined yet"}
                  </div>
                  <div className="hive-dim hive-mono">{st.queen.url}</div>
                  {st.queen.joined ? (
                    <div className="hive-dim">last heard {ago(st.queen.last_ok, now)}</div>
                  ) : (
                    <div className="hive-dim">
                      {st.queen.error || "trying…"} — keeps trying, in any start order.
                    </div>
                  )}
                </div>
              </div>
            )}

            {st.role === "queen" && (
              <div className="card">
                <div className="card-title">Workers ({members.length})</div>
                {members.length === 0 ? (
                  <div className="hive-empty">
                    <p>No workers have joined yet.</p>
                    <p className="hive-dim">
                      A worker joins by itself: give it <code>hive.role: "worker"</code> and{" "}
                      <code>hive.join_url</code> pointing here, plus a reachable{" "}
                      <code>hive.advertise_url</code>. This roster is whoever is actually there.
                    </p>
                  </div>
                ) : (
                  <div className="hive-table" role="table">
                    <div className="hive-row hive-head" role="row">
                      <span />
                      <span>Worker</span>
                      <span>Address</span>
                      <span>Joined</span>
                      <span>Last heard</span>
                    </div>
                    {members.map((m) => (
                      <div className={`hive-row${m.state === "lost" ? " lost" : ""}`} role="row" key={m.name}>
                        <span
                          className={`status-dot ${m.state === "live" ? "ok" : "unknown"}`}
                          title={m.state}
                        />
                        <span className="hive-name">
                          {m.name}
                          {m.state === "lost" && <span className="chip">lost</span>}
                        </span>
                        <span className="hive-mono hive-dim">{m.url}</span>
                        <span className="hive-dim">{ago(m.joined_at, now)}</span>
                        <span className={m.state === "lost" ? "hive-warn" : "hive-dim"}>{ago(m.last_seen, now)}</span>
                      </div>
                    ))}
                  </div>
                )}
                <div className="hive-foot hive-dim">
                  A worker that stays quiet for three heartbeats
                  {st.interval_ms ? ` (${Math.round((st.interval_ms * 3) / 1000)}s)` : ""} is marked lost and is not
                  given orders; it returns to live by speaking again.
                </div>
              </div>
            )}

            {st.role === "" && (
              <div className="card">
                <div className="card-title">Joining a hive</div>
                <div className="hive-dim">
                  This instance is complete by itself. To make it a queen, set <code>hive.role: "queen"</code>. To make
                  it a worker, set <code>hive.role: "worker"</code>, <code>hive.join_url</code> and{" "}
                  <code>hive.advertise_url</code>. Either way it keeps working alone if the hive is gone.
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
