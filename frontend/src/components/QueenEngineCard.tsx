import { useCallback, useEffect, useRef, useState } from "react";
import { ArrowDownIcon, ArrowUpIcon, CheckIcon, HexagonIcon, XIcon } from "lucide-react";
import { QueenEngineList, SetQueenEngineList } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { translate } from "../lib/i18n";
import { Block, Group, IconBtn, Note, Row, State, Text, Toggle } from "./settings/kit";

type Engine = {
  id: string;
  label: string;
  where: string;
  local: boolean;
  version?: string;
  state: "online" | "installed" | "offline";
  reason?: string;
  enabled: boolean;
};

const SUPERLEO = "superleo";

/** The rank in front of a row: the order turns are tried in, the first one
 *  in the accent; a dash for an engine switched off. */
function Rank({ n, first }: { n: number; first: boolean }) {
  const style = n === 0
    ? { background: "transparent", color: "var(--text-3)", boxShadow: "inset 0 0 0 1px var(--border)" }
    : first
      ? { background: "var(--accent)", color: "var(--on-accent, #fff)" }
      : { background: "var(--bg-2, var(--accent-soft))", color: "var(--text-1)" };
  return (
    <span aria-hidden style={{
      width: 22, height: 22, borderRadius: "50%", display: "grid", placeItems: "center", flex: "none",
      font: "600 11.5px var(--font-mono)", ...style,
    }}>{n === 0 ? "–" : n}</span>
  );
}

/** The single engine and fallback the older settings fields carry, from the
 *  list: the first engine on ("" for SuperLeo), and SuperLeo on after it. */
function legacyOf(list: Engine[]): { engine: string; fallback: boolean } {
  const on = list.filter((e) => e.enabled).map((e) => e.id);
  const first = on[0] ?? SUPERLEO;
  return { engine: first === SUPERLEO ? "" : first, fallback: first === SUPERLEO || on.slice(1).includes(SUPERLEO) };
}

/**
 * The queen's engines in the order a turn tries them: each switched on or
 * off, moved up or down. Only engines this system has are listed (SuperLeo,
 * the agent CLIs here and on linked machines); one in the list that is not
 * there now shows offline and can be removed. Loads and saves itself; the
 * older props are accepted, and onChange is told the single engine and
 * fallback after each save so a page holding a copy of the settings follows.
 */
export default function QueenEngineCard({ hidden, onChange }: {
  hidden?: boolean;
  engine?: string;
  fallback?: boolean;
  publicURL?: string;
  onChange?: (engine: string, fallback: boolean, publicURL: string) => void;
}) {
  const [list, setList] = useState<Engine[] | null>(null);
  const [now, setNow] = useState("");
  const [publicURL, setPublicURL] = useState("");
  const [error, setError] = useState("");
  const saved = useRef<{ list: Engine[]; publicURL: string }>({ list: [], publicURL: "" });

  const take = useCallback((r: any) => {
    const engines = ((r?.engines ?? []) as Engine[]).map((e) => ({ ...e }));
    setList(engines);
    setNow(r?.now ?? "");
    setPublicURL(r?.public_url ?? "");
    saved.current = { list: engines, publicURL: r?.public_url ?? "" };
  }, []);

  useEffect(() => {
    if (hidden) return;
    QueenEngineList().then(take).catch((e) => setError(String(e?.message ?? e)));
    const off = EventsOn("queen:engine", (p: { engine?: string }) => setNow(p?.engine ?? ""));
    return () => {
      if (typeof off === "function") off();
    };
  }, [hidden, take]);

  const save = (next: Engine[], url: string) => {
    setList(next);
    setError("");
    const entries = next.map((e) => ({ engine: e.id, enabled: e.enabled }));
    SetQueenEngineList(entries as any, url)
      .then((r: any) => {
        take(r);
        const l = legacyOf((r?.engines ?? []) as Engine[]);
        onChange?.(l.engine, l.fallback, r?.public_url ?? url);
      })
      .catch((e) => {
        setList(saved.current.list);
        setPublicURL(saved.current.publicURL);
        setError(`${translate("Could not save")}: ${String(e?.message ?? e)}`);
      });
  };

  if (hidden) return null;
  const rows = list ?? [];
  const move = (i: number, by: number) => {
    const j = i + by;
    if (j < 0 || j >= rows.length) return;
    const next = rows.slice();
    [next[i], next[j]] = [next[j], next[i]];
    save(next, publicURL);
  };
  const toggle = (i: number, on: boolean) => save(rows.map((e, k) => (k === i ? { ...e, enabled: on } : e)), publicURL);
  const remove = (i: number) => save(rows.filter((_, k) => k !== i), publicURL);

  const onIds = rows.filter((e) => e.enabled).map((e) => e.id);
  const remote = rows.some((e) => e.enabled && !e.local);

  return (
    <Group icon={HexagonIcon} title="Queen engine" testId="queen-engines">
      {rows.map((e, i) => {
        const rank = e.enabled ? onIds.indexOf(e.id) + 1 : 0;
        const where = e.id === SUPERLEO ? translate("Built in") : e.local ? translate("This machine") : e.where;
        const sub = [where, e.version].filter(Boolean).join(" · ");
        const inUse = now !== "" && now === e.id && now !== onIds[0];
        const reason = e.reason ? translate(e.reason) : undefined;
        const state = !e.enabled
          ? <State tone="off" title={reason}>{e.state === "offline" ? "Offline" : "Not used"}</State>
          : e.state === "offline"
            ? <State tone="bad" title={reason}>Offline</State>
            : inUse
              ? <State tone="busy">In use</State>
              : <State tone="ok">Online</State>;
        // Not there now, and not SuperLeo: it can be taken out of the list.
        const gone = e.state === "offline" && e.id !== SUPERLEO && (e.reason === "not installed" || e.reason === "not reachable now");
        return (
          <Row key={e.id} lead={<Rank n={rank} first={rank === 1} />} label={e.label} sub={sub} dim={!e.enabled}
            testId={`queen-engine-${e.id}`}>
            {state}
            <IconBtn icon={ArrowUpIcon} label="Move up" disabled={i === 0} onClick={() => move(i, -1)} />
            <IconBtn icon={ArrowDownIcon} label="Move down" disabled={i === rows.length - 1} onClick={() => move(i, 1)} />
            {gone && <IconBtn icon={XIcon} label={translate("Remove {name}", { name: `${e.label} · ${e.where}` })} tone="danger" onClick={() => remove(i)} />}
            <Toggle on={e.enabled} label={translate("Enable {name}", { name: e.local ? e.label : `${e.label} · ${e.where}` })}
              onChange={(v) => toggle(i, v)} testId={`queen-engine-${e.id}-on`} />
          </Row>
        );
      })}
      {remote && (
        <Row label="Public address">
          <Text mono value={publicURL} placeholder="https://ai.example.com" label="Public address" width={264}
            onChange={setPublicURL}
            onEnter={() => publicURL !== saved.current.publicURL && save(rows, publicURL)} />
          {publicURL !== saved.current.publicURL && (
            <IconBtn icon={CheckIcon} label="Save" tone="accent" onClick={() => save(rows, publicURL)} />
          )}
        </Row>
      )}
      {error && <Block><Note bad>{error}</Note></Block>}
    </Group>
  );
}
