import React, { useCallback, useEffect, useState } from "react";
import {
  ArrowLeftIcon,
  ClockIcon,
  Maximize2Icon,
  RefreshCwIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import { Response } from "@/components/ai-elements/response";
import { Dashboard, ageLabel, dashboards } from "../lib/dashboards";
import { describeCron } from "../lib/cron";

/**
 * The dashboards someone kept, and the state of how fresh each one is.
 *
 * A saved dashboard is the reply text and the question that produced it, so
 * this draws it with the same renderer the transcript uses — nothing here knows
 * what a `bigscreen` block is, which is why a new plugin needs no changes in
 * this file.
 */

/** The schedules on offer, as cron. Deliberately few: a dashboard that
 *  refreshes itself is answering "is this current when I open it", and the
 *  answers to that are hourly, this morning, or Monday. Anything more specific
 *  belongs on the Schedules page, which can already express it. */
const CRON_CHOICES: { value: string; label: string }[] = [
  { value: "", label: "Manual only" },
  { value: "0 * * * *", label: "Every hour" },
  { value: "0 8 * * *", label: "Every day at 08:00" },
  { value: "0 8 * * 1-5", label: "Weekdays at 08:00" },
  { value: "0 8 * * 1", label: "Mondays at 08:00" },
];

function Card({
  d,
  onOpen,
  onRefresh,
  onDelete,
}: {
  d: Dashboard;
  onOpen: () => void;
  onRefresh: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="dash-card">
      <button className="dash-card-main" onClick={onOpen} title="Open">
        <span className="dash-card-name">{d.name}</span>
        <span className="dash-card-meta">
          {d.refreshing ? (
            <>
              <span className="spinner" /> refreshing…
            </>
          ) : (
            ageLabel(d.refreshed_at)
          )}
          {d.cron && (
            <span className="dash-cron" title={describeCron(d.cron)}>
              <ClockIcon size={11} />
            </span>
          )}
        </span>
        {d.last_error && <span className="dash-card-err">⚠ {d.last_error}</span>}
      </button>
      <div className="dash-card-actions">
        <button
          className="panel-toggle inline"
          onClick={onRefresh}
          disabled={d.refreshing || !d.prompt}
          title={d.prompt ? "Ask again and replace the contents" : "No saved question to re-ask"}
        >
          <RefreshCwIcon className="size-3.5" />
        </button>
        <button className="panel-toggle inline" onClick={onDelete} title="Delete">
          <Trash2Icon className="size-3.5" />
        </button>
      </div>
    </div>
  );
}

/**
 * A dashboard with the window to itself.
 *
 * A data wall is laid out on a twelve-column grid and a side panel is three
 * hundred pixels wide, so in the panel the whole thing stacks into a column of
 * squeezed cards. This is the same source through the same renderer, given room
 * to be what it was drawn as.
 *
 * Escape closes it, because it covers everything and a full-screen thing that
 * can only be dismissed by finding a small button is a trap.
 */
function FullScreen({ d, onClose }: { d: Dashboard; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="dash-full" role="dialog" aria-label={d.name}>
      <div className="dash-full-bar">
        <span className="dash-full-name">{d.name}</span>
        <span className="dash-full-age">
          {d.refreshing ? (
            <>
              <span className="spinner" /> refreshing…
            </>
          ) : (
            <>data as of {ageLabel(d.refreshed_at)}</>
          )}
        </span>
        <button className="panel-toggle" onClick={onClose} title="Close (Esc)" aria-label="Close">
          <XIcon className="size-4" />
        </button>
      </div>
      <div className="dash-full-body">
        <Response>{d.source}</Response>
      </div>
    </div>
  );
}

function Detail({
  d,
  onBack,
  onRefresh,
  onRename,
  onCron,
}: {
  d: Dashboard;
  onBack: () => void;
  onRefresh: () => void;
  onRename: (name: string) => void;
  onCron: (cron: string) => void;
}) {
  const [name, setName] = useState(d.name);
  const [full, setFull] = useState(false);
  useEffect(() => setName(d.name), [d.id, d.name]);

  // Full screen replaces the panel copy rather than covering it. Rendering the
  // same source twice at once puts two live instances of every block on the
  // page — and a `bigscreen` wall, which owns a canvas and an animation loop
  // keyed by the block's id, draws as an empty grey bar when it is the second
  // one. The panel copy is behind an opaque overlay anyway, so there was never
  // anything to see for the cost.
  if (full) return <FullScreen d={d} onClose={() => setFull(false)} />;

  return (
    <>
      <div className="trace-head">
        <button className="panel-toggle inline" onClick={onBack} title="All dashboards">
          <ArrowLeftIcon className="size-4" />
        </button>
        <input
          className="dash-name-input"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => name.trim() && name !== d.name && onRename(name.trim())}
          aria-label="Dashboard name"
        />
        <button
          className="panel-toggle inline"
          onClick={() => setFull(true)}
          title="Full screen"
          aria-label="Full screen"
        >
          <Maximize2Icon className="size-4" />
        </button>
        <button
          className="panel-toggle inline"
          onClick={onRefresh}
          disabled={d.refreshing || !d.prompt}
          title={d.prompt ? "Ask again and replace the contents" : "No saved question to re-ask"}
        >
          <RefreshCwIcon className="size-4" />
        </button>
      </div>

      <div className="dash-detail">
        {/* Said before the numbers, not after: a wall of prices carries no clue
            in itself about which day it is a picture of. */}
        <div className="dash-age">
          {d.refreshing ? (
            <>
              <span className="spinner" /> refreshing…
            </>
          ) : (
            <>data as of {ageLabel(d.refreshed_at)}</>
          )}
        </div>
        {d.last_error && <div className="dash-error">⚠ {d.last_error}</div>}

        <Response>{d.source}</Response>

        {d.prompt ? (
          <div className="dash-foot">
            <div className="dash-foot-label">Refreshes by re-asking</div>
            <div className="dash-prompt">{d.prompt}</div>
            <select
              className="input"
              value={CRON_CHOICES.some((c) => c.value === d.cron) ? d.cron || "" : d.cron}
              onChange={(e) => onCron(e.target.value)}
            >
              {CRON_CHOICES.map((c) => (
                <option key={c.value} value={c.value}>
                  {c.label}
                </option>
              ))}
              {/* A schedule set elsewhere — on the Schedules page, say — must
                  still be selectable, or opening this would silently change it. */}
              {d.cron && !CRON_CHOICES.some((c) => c.value === d.cron) && (
                <option value={d.cron}>{describeCron(d.cron)}</option>
              )}
            </select>
          </div>
        ) : (
          <div className="dash-foot">
            <div className="dash-foot-label">
              Saved without a question, so it cannot refresh itself — it stays as it was.
            </div>
          </div>
        )}
      </div>
    </>
  );
}

export default function DashboardsPanel() {
  const [items, setItems] = useState<Dashboard[]>([]);
  const [openId, setOpenId] = useState("");

  const load = useCallback(async () => {
    try {
      setItems(await dashboards.list());
    } catch {
      // The list is the whole panel; an empty one with the message below is a
      // better failure than an error banner nobody can act on.
      setItems([]);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // A refresh finishes in Go, whether this window started it or a cron did.
  useEffect(() => {
    EventsOn("dashboard:updated", () => load());
    EventsOn("dashboard:refreshing", () => load());
    return () => {
      EventsOff("dashboard:updated");
      EventsOff("dashboard:refreshing");
    };
  }, [load]);

  const open = items.find((d) => d.id === openId);

  const refresh = async (id: string) => {
    try {
      await dashboards.refresh(id);
    } catch {
      /* the card shows the stored error once the run reports back */
    }
    load();
  };

  if (open) {
    return (
      <Detail
        d={open}
        onBack={() => setOpenId("")}
        onRefresh={() => refresh(open.id)}
        onRename={async (name) => {
          await dashboards.rename(open.id, name).catch(() => {});
          load();
        }}
        onCron={async (cron) => {
          await dashboards.setCron(open.id, cron).catch(() => {});
          load();
        }}
      />
    );
  }

  return (
    <>
      <div className="trace-head">
        <span>Dashboards</span>
        <span style={{ color: "var(--text-3)", fontWeight: 400 }}>{items.length}</span>
      </div>
      <div className="trace-list">
        {items.length === 0 ? (
          <div className="trace-empty">
            Nothing saved yet.
            <br />
            A reply that draws a chart, a wall or a panel gets a save button
            under it.
          </div>
        ) : (
          items.map((d) => (
            <Card
              key={d.id}
              d={d}
              onOpen={() => setOpenId(d.id)}
              onRefresh={() => refresh(d.id)}
              onDelete={async () => {
                await dashboards.remove(d.id).catch(() => {});
                load();
              }}
            />
          ))
        )}
      </div>
    </>
  );
}
