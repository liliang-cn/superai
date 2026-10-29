import { Fragment, useCallback, useEffect, useState } from "react";
import { ChevronDownIcon, PlusIcon, SlidersHorizontalIcon, TerminalIcon, Trash2Icon } from "lucide-react";
import { DeleteCodingTool, ListCodingTools, SaveCodingTool, SetCodingToolsEnabled, TryCodingTool } from "../../../wailsjs/go/app/App";
import { app } from "../../../wailsjs/go/models";
import { translate } from "../../lib/i18n";
import { Avatar, Block, Btn, ConfirmBtn, Group, IconBtn, Menu, Note, Row, Seg, State, Text, Toggle } from "./kit";

/**
 * The coding tools SuperLeo may hand work to: the ones it knows, the ones
 * found on this machine, and the ones added by their command line — here or
 * on a SuperLeo linked by URL. Loads itself; takes nothing.
 */

type Tool = app.CodingToolRow;
type Offer = app.CodingToolOffer;
type Draft = {
  kind: string;
  id: string;
  name: string;
  command: string;
  model: string;
  path: string;
  preset: string;
  machine: string;
  /** A tool not saved yet. */
  fresh: boolean;
};

const keyOf = (t: { id: string; machine?: string }) => `${t.machine ?? ""}/${t.id}`;
const NEW = "/__new__";

function errText(e: unknown) {
  return String(e instanceof Error ? e.message : e);
}

/** The small "Custom" mark after a user-added tool's name. */
function Tag({ children }: { children: string }) {
  return (
    <span style={{
      marginLeft: 6, padding: "1px 6px", borderRadius: 5, fontSize: 11, fontWeight: 500, verticalAlign: 1,
      color: "var(--accent)", background: "var(--accent-soft)",
    }}>{translate(children)}</span>
  );
}

function initial(name: string) {
  const c = name.trim().charAt(0);
  return c ? c.toUpperCase() : "▸";
}

function stateOf(t: Tool) {
  if (t.tried) {
    return t.tried.ok
      ? <State tone="ok" title={t.tried.text}>{translate("Answered in {s}s", { s: (t.tried.ms / 1000).toFixed(1) })}</State>
      : <State tone="bad" title={t.tried.reason}>Did not answer</State>;
  }
  if (t.off) return <State tone="off">Switched off</State>;
  if (t.installed) return <State tone="ok">Installed</State>;
  return null;
}

function subOf(t: Tool) {
  if (t.kind === "custom") {
    return (
      <span className="mono">
        {t.machine ? `${translate("On {machine}", { machine: t.machine })} · ` : ""}{t.command}
      </span>
    );
  }
  if (!t.installed) return translate("Not installed");
  return <span className="mono">{[t.version, t.path].filter(Boolean).join(" · ")}</span>;
}

export default function CodingToolsCard() {
  const [data, setData] = useState<app.CodingTools | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [open, setOpen] = useState(""); // keyOf the row being edited, or NEW
  const [busy, setBusy] = useState(false);
  const [trying, setTrying] = useState(false);
  const [tried, setTried] = useState<app.CodingToolTry | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(() => {
    ListCodingTools().then(setData).catch((e) => setError(errText(e)));
  }, []);
  useEffect(load, [load]);

  const close = () => {
    setOpen("");
    setDraft(null);
    setTried(null);
    setError("");
  };

  const edit = (t: Tool) => {
    if (open === keyOf(t)) return close();
    setOpen(keyOf(t));
    setTried(null);
    setError("");
    setDraft({
      kind: t.kind, id: t.id, name: t.name, command: t.command ?? "", model: t.model ?? "",
      path: t.override ?? "", preset: t.preset ?? "", machine: t.machine ?? "", fresh: false,
    });
  };

  const act = async (f: () => Promise<app.CodingTools>) => {
    setBusy(true);
    setError("");
    try {
      setData(await f());
      return true;
    } catch (e) {
      setError(errText(e));
      load();
      return false;
    } finally {
      setBusy(false);
    }
  };

  const toEdit = (d: Draft) => new app.CodingToolEdit({
    kind: d.kind, id: d.id, name: d.name.trim(), command: d.command.trim(), model: d.model.trim(),
    path: d.path.trim(), preset: d.preset,
  });

  const save = async () => {
    if (!draft) return;
    if (await act(() => SaveCodingTool(draft.machine, toEdit(draft)))) close();
  };

  const tryIt = async () => {
    if (!draft) return;
    setTrying(true);
    setTried(null);
    try {
      setTried(await TryCodingTool(draft.machine, toEdit(draft)));
      if (!draft.fresh) load();
    } catch (e) {
      setTried(new app.CodingToolTry({ ok: false, ms: 0, reason: errText(e) }));
    } finally {
      setTrying(false);
    }
  };

  const add = (o: Offer | null) => {
    setTried(null);
    setError("");
    if (o && !o.command && o.found) {
      // A CLI SuperLeo drives itself, here already: offering it is all.
      void act(() => SaveCodingTool("", new app.CodingToolEdit({ kind: "found", id: o.id })));
      return;
    }
    setOpen(NEW);
    setDraft(o?.command
      ? { kind: "custom", id: o.id, name: o.label, command: o.command, model: "", path: "", preset: o.id, machine: "", fresh: true }
      : o
        ? { kind: "found", id: o.id, name: o.label, command: "", model: "", path: "", preset: o.id, machine: "", fresh: true }
        : { kind: "custom", id: "", name: "", command: "", model: "", path: "", preset: "", machine: "", fresh: true });
  };

  if (!data) {
    return (
      <Group icon={TerminalIcon} title="Coding tools" testId="coding-tools">
        <Row label={error ? <Note bad>{error}</Note> : <State tone="busy">…</State>} />
      </Group>
    );
  }

  const set = (patch: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...patch } : d));
  const machines = data.machines ?? [];

  const form = (d: Draft) => (
    <Block testId="coding-tool-form">
      <div className="st-form">
        {d.kind === "custom" ? (
          <>
            <label>{translate("Name")}</label>
            <Text label="Name" value={d.name} onChange={(v) => set({ name: v })} placeholder="Gemini CLI" testId="coding-tool-name" />
            <label>{translate("Command")}</label>
            <Text mono label="Command" value={d.command} onChange={(v) => set({ command: v })} placeholder="gemini -p {prompt} --yolo" testId="coding-tool-command" />
          </>
        ) : (
          <>
            <label>{translate("Path")}</label>
            <Text mono label="Path" value={d.path} onChange={(v) => set({ path: v })}
              placeholder={data.tools.find((t) => t.id === d.id)?.path || "Full path"} testId="coding-tool-path" />
          </>
        )}
        <label>{translate("Model")}</label>
        <Text mono label="Model" value={d.model} onChange={(v) => set({ model: v })} placeholder="Default" testId="coding-tool-model" />
        {d.kind === "custom" && d.fresh && machines.length > 0 && (
          <>
            <label>{translate("Which machine")}</label>
            <div>
              <Seg label="Which machine" value={d.machine}
                options={[{ value: "", label: "This machine" }, ...machines.map((m) => ({ value: m, label: m }))]}
                onChange={(v) => set({ machine: v })} />
            </div>
          </>
        )}
        <div className="st-form-b">
          <Btn onClick={() => void tryIt()} disabled={trying || busy || (d.kind === "custom" && !d.command.trim())} data-testid="coding-tool-try">
            {trying ? "Trying…" : "Try it"}
          </Btn>
          {error ? <Note bad>{error}</Note> : tried ? (
            <Note bad={!tried.ok} testId="coding-tool-tried">
              {tried.ok
                ? `${translate("Answered in {s}s", { s: (tried.ms / 1000).toFixed(1) })} · ${tried.text}`
                : tried.reason || translate("Did not answer")}
            </Note>
          ) : <span style={{ flex: 1 }} />}
          <Btn onClick={close}>Cancel</Btn>
          <Btn pri onClick={() => void save()} data-testid="coding-tool-save"
            disabled={busy || (d.kind === "custom" ? !d.name.trim() || !d.command.trim() : d.fresh && !d.path.trim())}>
            Save
          </Btn>
        </div>
      </div>
    </Block>
  );

  const offers = data.offers ?? [];
  const menu = [
    ...offers.map((o) => ({
      key: o.id,
      label: (
        <span style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>{o.label}</span>
          <span style={{ fontSize: 12, color: o.found ? "var(--green)" : "var(--text-3)" }}>{translate(o.found ? "Found" : "Not installed")}</span>
        </span>
      ),
      hint: o.path || o.command || o.binary,
      onSelect: () => add(o),
    })),
    { key: "__custom", label: translate("Custom command…"), lead: <TerminalIcon size={15} strokeWidth={1.9} />, onSelect: () => add(null) },
  ];

  return (
    <Group icon={TerminalIcon} title="Coding tools" testId="coding-tools" action={
      <>
        <span className="st-lab">{translate("Hand work to them")}</span>
        <Toggle on={data.enabled} label="Hand work to coding tools" disabled={busy} testId="coding-tools-on"
          onChange={(on) => void act(() => SetCodingToolsEnabled(on))} />
        <Menu disabled={busy} items={menu} trigger={
          <Btn icon={PlusIcon} trail={ChevronDownIcon} data-testid="coding-tools-add">Add tool</Btn>
        } />
      </>
    }>
      {data.tools.map((t) => {
        const k = keyOf(t);
        const removable = t.kind !== "builtin";
        return (
          <Fragment key={k}>
            <Row testId={`coding-tool-${t.id}${t.machine ? `.${t.machine}` : ""}`}
              dim={!t.installed && t.kind !== "custom"}
              lead={<Avatar tone={t.kind === "custom" ? "accent" : t.installed ? undefined : "muted"}>{initial(t.name)}</Avatar>}
              label={<>{t.name}{t.kind === "custom" && <Tag>Custom</Tag>}</>}
              sub={subOf(t)}>
              {stateOf(t)}
              <IconBtn icon={SlidersHorizontalIcon} label={translate("Edit {name}", { name: t.name })} pressed={open === k}
                onClick={() => edit(t)} data-testid={`coding-tool-edit-${t.id}`} />
              {removable && (
                <ConfirmBtn icon={Trash2Icon} label="Delete tool" title={translate("Delete {name}?", { name: t.name })} ok="Delete"
                  disabled={busy} testId={`coding-tool-delete-${t.id}`}
                  onConfirm={async () => {
                    if (open === k) close();
                    await act(() => DeleteCodingTool(t.machine ?? "", t.id));
                  }} />
              )}
            </Row>
            {open === k && draft && form(draft)}
          </Fragment>
        );
      })}
      {open === NEW && draft && form(draft)}
      {!open && error && <Row label={<Note bad>{error}</Note>} />}
    </Group>
  );
}
