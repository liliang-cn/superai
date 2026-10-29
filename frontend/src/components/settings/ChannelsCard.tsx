import { CSSProperties, Fragment, ReactNode, useCallback, useEffect, useState } from "react";
import {
  ChevronDownIcon, CodeXmlIcon, CopyIcon, EyeIcon, EyeOffIcon, MessagesSquareIcon, PencilIcon, PlusIcon,
  RefreshCwIcon, SendIcon, Trash2Icon,
} from "lucide-react";
import {
  DeleteChannel, ListChannels, RegenerateChannelToken, RevealChannelToken, SaveChannel, TestChannel,
} from "../../../wailsjs/go/app/App";
import { backend } from "../../../wailsjs/go/models";
import { EventsOn } from "../../../wailsjs/runtime";
import { translate, useI18n } from "../../lib/i18n";
import { Btn, Chips, ConfirmBtn, Group, IconBtn, Menu, Note, Secret, Seg, State, Text, Toggle } from "./kit";

/**
 * Settings › Channels: where SuperLeo reaches you, and where you reach it.
 * Loads and saves itself through the channel RPCs, one channel at a time, so
 * it is not part of the page's Save.
 */

type View = backend.ChannelView;
type Kind = "telegram" | "feishu" | "wecom" | "dingtalk" | "slack" | "bark" | "webhook";

const KINDS: Record<Kind, { label: string; mark: ReactNode; color: string; twoWay: boolean }> = {
  telegram: { label: "Telegram", mark: "T", color: "#2aa3df", twoWay: true },
  feishu: { label: "Feishu", mark: "飞", color: "#3370ff", twoWay: false },
  wecom: { label: "WeCom", mark: "企", color: "#2b7de9", twoWay: false },
  dingtalk: { label: "DingTalk", mark: "钉", color: "#1677ff", twoWay: false },
  slack: { label: "Slack", mark: "S", color: "#4a154b", twoWay: false },
  bark: { label: "Bark", mark: "B", color: "#e5493a", twoWay: false },
  webhook: { label: "Generic webhook", mark: <CodeXmlIcon size={14} strokeWidth={2.2} />, color: "#33434d", twoWay: true },
};
const ORDER: Kind[] = ["telegram", "feishu", "wecom", "dingtalk", "slack", "bark", "webhook"];
const EVENTS: { id: string; label: string }[] = [
  { id: "reminder", label: "Reminder" },
  { id: "schedule", label: "Scheduled results" },
  { id: "needs_you", label: "Needs your OK" },
  { id: "failed", label: "Task failed" },
];

/** The coloured square that says which service a channel is. */
function Mark({ kind, small }: { kind: Kind; small?: boolean }) {
  const k = KINDS[kind] ?? KINDS.webhook;
  const size = small ? 22 : 32;
  return (
    <span aria-hidden className="st-avatar" style={{
      width: size, height: size, borderRadius: small ? 6 : 9, background: k.color, color: "#fff",
      fontSize: small ? 11 : 13, fontWeight: 700,
    }}>{k.mark}</span>
  );
}

const tagStyle = (two: boolean): CSSProperties => ({
  display: "inline-flex", alignItems: "center", height: 19, padding: "0 7px", borderRadius: 999,
  fontSize: 11, fontWeight: 500, whiteSpace: "nowrap",
  background: two ? "var(--accent-soft)" : "var(--bg-2)", color: two ? "var(--accent)" : "var(--text-2)",
});

function Tag({ two }: { two: boolean }) {
  return <span style={tagStyle(two)}>{translate(two ? "Two-way" : "Send only")}</span>;
}

/** Events as toggles: any number of them on. */
function EventPicks({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="st-chips" role="group" aria-label={translate("What it gets")} style={{ justifyContent: "flex-start" }}>
      {EVENTS.map((e) => {
        const on = value.includes(e.id);
        return (
          <button key={e.id} type="button" className="st-chip" aria-pressed={on}
            onClick={() => onChange(on ? value.filter((x) => x !== e.id) : [...value, e.id])}
            style={{
              padding: "0 11px", cursor: "pointer",
              ...(on ? { background: "var(--accent-soft)", borderColor: "transparent", color: "var(--accent)" } : {}),
            }}>
            {translate(e.label)}
          </button>
        );
      })}
    </div>
  );
}

/** A label above a sub-section of the form: Receiving, Sending. */
function Sub({ children }: { children: string }) {
  return <div style={{ gridColumn: "1 / -1", fontSize: 12, fontWeight: 600, color: "var(--text-3)", paddingTop: 6 }}>{translate(children)}</div>;
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const rt = (window as any).runtime;
    if (rt?.ClipboardSetText) await rt.ClipboardSetText(text);
  }
}

function clock(iso?: string) {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  return d.toDateString() === new Date().toDateString() ? hm : `${d.getMonth() + 1}-${d.getDate()} ${hm}`;
}

function title(c: View) {
  const label = KINDS[c.type as Kind]?.label ?? c.type;
  const base = translate(label);
  const name = (c.name ?? "").trim();
  // A name that only repeats the type (a migrated "Webhook") adds nothing.
  const same = !name || [base, label, c.type].some((x) => x.toLowerCase() === name.toLowerCase());
  return same ? base : `${base} · ${name}`;
}

function receiveURL(c: View, publicURL: string) {
  const path = c.inbound_path ?? "";
  if (!path) return "";
  if (publicURL) return publicURL + path;
  if (location.protocol.startsWith("http")) return location.origin + path;
  return path;
}

function summary(c: View): string {
  switch (c.type) {
    case "telegram": {
      const n = c.allowed_chats?.length ?? 0;
      return [c.bot_token_mask, n ? translate("{count} chats", { count: n }) : ""].filter(Boolean).join(" · ");
    }
    case "bark":
      return [c.server || "api.day.app", c.device_key_mask].filter(Boolean).join(" · ");
    case "webhook": {
      const send = c.url ? c.url.replace(/^https?:\/\//, "") : "";
      const recv = c.inbound_path ? c.inbound_path.slice(0, 14) + "…" : "";
      return [recv && `${translate("Receiving")} ${recv}`, send && `${translate("Sending")} ${send}`].filter(Boolean).join(" · ");
    }
    default:
      return (c.url ?? "").replace(/^https?:\/\//, "");
  }
}

function StateOf({ c }: { c: View }) {
  const s = c.status ?? ({} as backend.ChannelStatus);
  if (!c.enabled) return <State tone="off">Off</State>;
  if (c.state === "error") return <State tone="bad" title={c.state_error}>{translate("Error")}</State>;
  const okAt = s.last_ok_at ?? "", errAt = s.last_error_at ?? "";
  if (errAt && errAt > okAt) {
    return <State tone="bad" title={s.last_error}>{translate("Failed {time}", { time: clock(errAt) })}</State>;
  }
  if (c.state === "polling") return <State tone="ok">Online</State>;
  if (c.type === "webhook" && s.inbound_today > 0) {
    return <State tone="ok">{translate("{count} received today", { count: s.inbound_today })}</State>;
  }
  if (okAt) return <State tone="ok">{translate("Sent {time}", { time: clock(okAt) })}</State>;
  if (c.state === "idle") return <State tone="off">Idle</State>;
  if (!c.can_send && c.type !== "webhook") return <State tone="off">{translate("Not set up")}</State>;
  return <State tone="ok">{translate("Ready")}</State>;
}

/** What the form holds. Secrets start empty: empty keeps what is saved. */
type Draft = {
  name: string; events: string[]; url: string; secret: string; reply: string;
  botToken: string; chats: string[]; server: string; deviceKey: string;
};

function draftOf(c: View): Draft {
  return {
    name: c.name ?? "", events: [...(c.events ?? [])], url: "", secret: "", reply: c.reply_mode || "sync",
    botToken: "", chats: (c.allowed_chats ?? []).map(String), server: c.server ?? "", deviceKey: "",
  };
}

function inputOf(c: View, d: Draft, isNew: boolean): backend.ChannelInput {
  const in_: any = { name: d.name, events: d.events };
  if (isNew) in_.type = c.type; else in_.id = c.id;
  if (d.url.trim()) in_.url = d.url.trim();
  if (d.secret.trim()) in_.secret = d.secret.trim();
  if (c.type === "webhook") in_.reply_mode = d.reply;
  if (c.type === "telegram") {
    if (d.botToken.trim()) in_.bot_token = d.botToken.trim();
    in_.allowed_chats = d.chats.map((x) => Number(x)).filter((n) => Number.isFinite(n) && n !== 0);
  }
  if (c.type === "bark") {
    in_.server = d.server.trim();
    if (d.deviceKey.trim()) in_.device_key = d.deviceKey.trim();
  }
  return backend.ChannelInput.createFrom(in_);
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <label>{translate(label)}</label>
      <div style={{ display: "flex", alignItems: "center", gap: 4, minWidth: 0 }}>{children}</div>
    </>
  );
}

/** The receive address and token of a webhook channel. */
function Receive({ c, publicURL, onChanged }: { c: View; publicURL: string; onChanged: (v: View) => void }) {
  const [token, setToken] = useState("");
  const [copied, setCopied] = useState("");
  const url = receiveURL(c, publicURL);
  const reveal = async () => (token ? setToken("") : setToken(await RevealChannelToken(c.id)));
  const flash = (what: string) => { setCopied(what); window.setTimeout(() => setCopied(""), 1400); };
  return (
    <>
      <Field label="Receive URL">
        <Text value={url} readOnly mono label="Receive URL" />
        <IconBtn icon={CopyIcon} label={copied === "url" ? "Copied" : "Copy"} onClick={async () => { await copy(url); flash("url"); }} />
      </Field>
      <Field label="Token">
        <Text value={token || c.token_mask || ""} readOnly mono label="Token" />
        <IconBtn icon={token ? EyeOffIcon : EyeIcon} label={token ? "Hide" : "Show"} onClick={reveal} />
        <IconBtn icon={CopyIcon} label={copied === "token" ? "Copied" : "Copy token"}
          onClick={async () => { await copy(token || (await RevealChannelToken(c.id))); flash("token"); }} />
        <ConfirmBtn icon={RefreshCwIcon} label="New token" title="Replace the token?" ok="Replace" tone="accent"
          onConfirm={async () => { const v = await RegenerateChannelToken(c.id); setToken(""); onChanged(v); }} />
      </Field>
    </>
  );
}

function Editor({ c, isNew, publicURL, onDone, onSaved, onDeleted }: {
  c: View; isNew: boolean; publicURL: string;
  onDone: () => void; onSaved: (v: View) => void; onDeleted: () => void;
}) {
  const [d, setD] = useState<Draft>(() => draftOf(c));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((p) => ({ ...p, [k]: v }));
  const kind = c.type as Kind;

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      onSaved(await SaveChannel(inputOf(c, d, isNew)));
      onDone();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  };

  const urlField = (label: string, placeholder: string) => (
    <Field label={label}>
      <Text value={d.url} onChange={(v) => set("url", v)} mono label={label} placeholder={c.url || placeholder} />
    </Field>
  );
  const secretField = (label: string) => (
    <Field label={label}>
      <Secret value={d.secret} onChange={(v) => set("secret", v)} label={label} placeholder={c.secret_mask || "Not set"} />
    </Field>
  );

  return (
    <div className="st-row st-block" style={{ background: "var(--bg-2)", paddingLeft: 60 }} data-testid={`channel-edit-${c.id || "new"}`}>
      <div className="st-form">
        <Field label="Name">
          <Text value={d.name} onChange={(v) => set("name", v)} label="Name" placeholder={translate(KINDS[kind]?.label ?? "")} />
        </Field>
        {kind === "telegram" && (
          <>
            <Field label="Bot token">
              <Secret value={d.botToken} onChange={(v) => set("botToken", v)} label="Bot token" placeholder={c.bot_token_mask || "Not set"} />
            </Field>
            <Field label="Allowed chats">
              <Chips items={d.chats} mono removeLabel="Remove" addLabel="Add a chat ID" placeholder="123456789"
                onRemove={(x) => set("chats", d.chats.filter((y) => y !== x))}
                onAdd={(x) => { if (/^-?\d+$/.test(x) && !d.chats.includes(x)) set("chats", [...d.chats, x]); }} />
            </Field>
          </>
        )}
        {kind === "feishu" && <>{urlField("Webhook URL", "https://open.feishu.cn/open-apis/bot/v2/hook/…")}{secretField("Signing secret")}</>}
        {kind === "wecom" && urlField("Webhook URL", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…")}
        {kind === "dingtalk" && <>{urlField("Webhook URL", "https://oapi.dingtalk.com/robot/send?access_token=…")}{secretField("Signing secret")}</>}
        {kind === "slack" && urlField("Webhook URL", "https://hooks.slack.com/services/…")}
        {kind === "bark" && (
          <>
            <Field label="Server">
              <Text value={d.server} onChange={(v) => set("server", v)} mono label="Server" placeholder="https://api.day.app" />
            </Field>
            <Field label="Device key">
              <Secret value={d.deviceKey} onChange={(v) => set("deviceKey", v)} label="Device key" placeholder={c.device_key_mask || "Not set"} />
            </Field>
          </>
        )}
        {kind === "webhook" && (
          <>
            <Sub>Receiving</Sub>
            {!isNew && <Receive c={c} publicURL={publicURL} onChanged={onSaved} />}
            <Field label="Reply">
              <Seg value={d.reply} onChange={(v) => set("reply", v)} label="Reply"
                options={[{ value: "sync", label: "In the response" }, { value: "callback", label: "To the send URL" }]} />
            </Field>
            <Sub>Sending</Sub>
            {urlField("Send URL", "https://")}
            {secretField("Secret")}
          </>
        )}
        <Field label="What it gets">
          <EventPicks value={d.events} onChange={(v) => set("events", v)} />
        </Field>
        <div className="st-form-b">
          {!isNew && (
            <ConfirmBtn icon={Trash2Icon} label="Delete channel" title={translate("Delete {name}?", { name: title(c) })} ok="Delete"
              testId={`channel-delete-${c.id}`}
              onConfirm={async () => { await DeleteChannel(c.id); onDeleted(); }} />
          )}
          {err ? <Note bad>{err}</Note> : <div style={{ flex: 1 }} />}
          <Btn onClick={onDone}>Cancel</Btn>
          <Btn pri onClick={save} disabled={busy} data-testid={`channel-save-${c.id || "new"}`}>Save</Btn>
        </div>
      </div>
    </div>
  );
}

function ChannelRow({ c, open, onEdit, onToggle, onTest, testing, testErr }: {
  c: View; open: boolean; onEdit: () => void; onToggle: (on: boolean) => void; onTest: () => void;
  testing: boolean; testErr: string;
}) {
  return (
    <div className="st-row lead" data-testid={`channel-${c.id}`}>
      <div className="st-row-lead"><Mark kind={c.type as Kind} /></div>
      <div className="st-row-l">
        <div className="st-row-t" style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
          {title(c)}<Tag two={c.two_way} />
        </div>
        <div className="st-row-s mono" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{summary(c)}</div>
        {testErr && <Note bad>{testErr}</Note>}
      </div>
      <div className="st-row-c">
        <StateOf c={c} />
        <IconBtn icon={SendIcon} label="Send a test" onClick={onTest} disabled={testing || !c.can_send} spin={false}
          data-testid={`channel-test-${c.id}`} />
        <IconBtn icon={PencilIcon} label="Edit" pressed={open} onClick={onEdit} data-testid={`channel-editbtn-${c.id}`} />
        <Toggle on={c.enabled} onChange={onToggle} label={translate("Enable {name}", { name: title(c) })} testId={`channel-switch-${c.id}`} />
      </div>
    </div>
  );
}

export default function ChannelsCard() {
  useI18n();
  const [list, setList] = useState<View[]>([]);
  const [publicURL, setPublicURL] = useState("");
  const [open, setOpen] = useState<string>("");
  const [draft, setDraft] = useState<View | null>(null);
  const [testing, setTesting] = useState("");
  const [testErr, setTestErr] = useState<Record<string, string>>({});
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    try {
      const r = await ListChannels();
      setList(r?.channels ?? []);
      setPublicURL(r?.public_url ?? "");
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, []);

  useEffect(() => {
    load();
    const off = EventsOn("channels:changed", () => load());
    return () => { if (typeof off === "function") off(); };
  }, [load]);

  const replace = (v: View) => setList((l) => (l.some((x) => x.id === v.id) ? l.map((x) => (x.id === v.id ? v : x)) : [...l, v]));

  const add = (kind: Kind) => {
    setOpen("");
    setDraft(backend.ChannelView.createFrom({
      id: "", type: kind, name: "", enabled: true, events: EVENTS.map((e) => e.id), two_way: KINDS[kind].twoWay,
      secret_set: false, can_send: false, reply_mode: "sync", status: { inbound_today: 0 },
    }));
  };

  const toggle = async (c: View, on: boolean) => {
    replace({ ...c, enabled: on } as View);
    try {
      replace(await SaveChannel(backend.ChannelInput.createFrom({ id: c.id, enabled: on })));
    } catch (e) {
      replace(c);
      setTestErr((m) => ({ ...m, [c.id]: String(e) }));
    }
  };

  const test = async (c: View) => {
    setTesting(c.id);
    setTestErr((m) => ({ ...m, [c.id]: "" }));
    try {
      const r = await TestChannel(c.id);
      if (r.channel?.id) replace(r.channel);
      if (!r.ok) setTestErr((m) => ({ ...m, [c.id]: r.error ?? "" }));
    } finally {
      setTesting("");
    }
  };

  const addMenu = (
    <Menu
      trigger={<Btn icon={PlusIcon} trail={ChevronDownIcon} data-testid="channel-add">Add channel</Btn>}
      items={ORDER.map((k) => ({
        key: k,
        lead: <Mark kind={k} small />,
        label: <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ flex: 1 }}>{translate(KINDS[k].label)}</span><Tag two={KINDS[k].twoWay} />
        </span>,
        onSelect: () => add(k),
      }))}
    />
  );

  return (
    <Group icon={MessagesSquareIcon} title="Channels" action={addMenu} testId="channels-card">
      {list.length === 0 && !draft && (
        <div className="st-row"><div className="st-row-l"><div className="st-row-s">{translate("No channels")}</div></div></div>
      )}
      {list.map((c) => (
        <Fragment key={c.id}>
          <ChannelRow c={c} open={open === c.id} testing={testing === c.id} testErr={testErr[c.id] ?? ""}
            onEdit={() => { setDraft(null); setOpen(open === c.id ? "" : c.id); }}
            onToggle={(on) => toggle(c, on)} onTest={() => test(c)} />
          {open === c.id && (
            <Editor key={c.id} c={c} isNew={false} publicURL={publicURL}
              onDone={() => setOpen("")} onSaved={replace}
              onDeleted={() => { setOpen(""); setList((l) => l.filter((x) => x.id !== c.id)); }} />
          )}
        </Fragment>
      ))}
      {draft && (
        <>
          <div className="st-row lead">
            <div className="st-row-lead"><Mark kind={draft.type as Kind} /></div>
            <div className="st-row-l"><div className="st-row-t" style={{ display: "flex", alignItems: "center", gap: 8 }}>
              {translate(KINDS[draft.type as Kind].label)}<Tag two={draft.two_way} />
            </div></div>
          </div>
          <Editor c={draft} isNew publicURL={publicURL}
            onDone={() => setDraft(null)}
            onSaved={(v) => { replace(v); if (v.type === "webhook") setOpen(v.id); }}
            onDeleted={() => setDraft(null)} />
        </>
      )}
      {err && <div className="st-row"><Note bad>{err}</Note></div>}
    </Group>
  );
}
