import { useEffect, useState } from "react";
import { PlusIcon, SearchIcon, XIcon } from "lucide-react";
import { AddWorkerMCP, AddWorkerSkill, RemoveWorkerMCP, RemoveWorkerSkill, SearchSkills, WorkerKit } from "../../wailsjs/go/app/App";
import { useI18n } from "../lib/i18n";
import { toast } from "../lib/toasts";
import "./world.css";

type Kit = { mcp: { name: string; about?: string; ok: boolean; tools?: string[]; own: boolean }[]; skills: { name: string; about?: string; own: boolean }[] };

/** One worker's own MCP servers and skills, beside what the hive gives every
 *  worker: added and removed on that worker alone. */
export default function WorkerKitCard({ worker }: { worker: string }) {
  const { t } = useI18n();
  const [kit, setKit] = useState<Kit | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [confirm, setConfirm] = useState("");
  const [name, setName] = useState("");
  const [cmd, setCmd] = useState("");
  const [q, setQ] = useState("");
  const [found, setFound] = useState<{ name: string; description: string; path: string }[] | null>(null);
  useEffect(() => {
    setKit(null); setErr("");
    WorkerKit(worker).then((k: any) => setKit(k)).catch((e) => setErr(String(e instanceof Error ? e.message : e)));
  }, [worker]);
  const run = async (key: string, fn: () => Promise<unknown>, ok: string) => {
    setBusy(key);
    try { setKit((await fn()) as any); toast.success(ok); return true; }
    catch (e) { toast.error(String(e instanceof Error ? e.message : e)); return false; }
    finally { setBusy(""); setConfirm(""); }
  };
  const drop = (key: string, fn: () => Promise<unknown>, what: string) => () => {
    if (confirm !== key) { setConfirm(key); return; }
    void run(key, fn, t("Removed {name}", { name: what }));
  };
  if (err) return <p className="wi-note">{err}</p>;
  if (!kit) return <p className="wi-note">…</p>;
  const has = new Set(kit.skills.map((s) => s.name));
  return (
    <div className="wi-kit" data-testid="worker-kit">
      <p className="wi-cap">MCP</p>
      <ul className="wi-list">
        {kit.mcp.map((m) => (
          <li key={m.name} title={[m.about, ...(m.tools ?? [])].filter(Boolean).join("\n")}>
            <i className={`wi-dot ${m.ok ? "done" : "failed"}`} />{m.name}
            <em>{m.own ? (m.ok ? t("{count} tools", { count: m.tools?.length ?? 0 }) : t("not connected")) : t("from the hive")}</em>
            {m.own && <button className="wi-x" disabled={busy === "m:" + m.name} title={t("Remove")} onClick={drop("m:" + m.name, () => RemoveWorkerMCP(worker, m.name), m.name)}>
              {confirm === "m:" + m.name ? t("Remove it?") : <XIcon size={12} />}</button>}
          </li>
        ))}
      </ul>
      <form className="wi-say wi-kit-add" onSubmit={(e) => {
        e.preventDefault();
        const [command, ...args] = cmd.trim().split(/\s+/);
        if (!name.trim() || !command) return;
        void run("addm", () => AddWorkerMCP(worker, name.trim(), command, args, {}), t("Added {name}", { name: name.trim() })).then((ok) => { if (ok) { setName(""); setCmd(""); } });
      }}>
        <input className="wi-kit-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={t("Name")} data-testid="kit-mcp-name" />
        <input value={cmd} onChange={(e) => setCmd(e.target.value)} placeholder={t("Command and arguments")} data-testid="kit-mcp-cmd" />
        <button type="submit" disabled={!name.trim() || !cmd.trim() || busy === "addm"} title={t("Add")}>{busy === "addm" ? "…" : <PlusIcon size={14} />}</button>
      </form>
      <p className="wi-cap">{t("Skills")}</p>
      <ul className="wi-list">
        {kit.skills.map((s) => (
          <li key={s.name} title={s.about}>
            <i className="wi-dot done" />{s.name}
            {!s.own && <em>{t("from the hive")}</em>}
            {s.own && <button className="wi-x" disabled={busy === "s:" + s.name} title={t("Remove")} onClick={drop("s:" + s.name, () => RemoveWorkerSkill(worker, s.name), s.name)}>
              {confirm === "s:" + s.name ? t("Remove it?") : <XIcon size={12} />}</button>}
          </li>
        ))}
      </ul>
      <form className="wi-say" onSubmit={(e) => {
        e.preventDefault();
        setBusy("search");
        SearchSkills(q.trim()).then((r: any) => setFound(r ?? [])).catch((e) => toast.error(String(e))).finally(() => setBusy(""));
      }}>
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("Find a skill to add…")} />
        <button type="submit" disabled={busy === "search"}><SearchIcon size={14} /></button>
      </form>
      {found && (
        <ul className="wi-list wi-shop">
          {found.filter((c) => !has.has(c.name)).slice(0, 6).map((c) => (
            <li key={c.path || c.name} title={c.description}>
              <span className="wi-shop-n"><b>{c.name}</b><small>{c.description}</small></span>
              <button className="wi-get" disabled={busy === "k:" + c.name}
                onClick={() => void run("k:" + c.name, () => AddWorkerSkill(worker, c.name, c.path), t("Added {name}", { name: c.name }))}>
                {busy === "k:" + c.name ? "…" : t("Add")}</button>
            </li>
          ))}
          {found.length === 0 && <li className="wi-none">{t("Nothing found on this machine.")}</li>}
        </ul>
      )}
    </div>
  );
}
