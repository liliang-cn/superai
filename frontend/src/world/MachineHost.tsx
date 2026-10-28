import { useCallback, useEffect, useState } from "react";
import { RefreshCwIcon, RotateCwIcon, ScrollTextIcon, TerminalIcon } from "lucide-react";
import { MachineExec, MachineLogs, MachineService, MachineServices, MachineStatus } from "../../wailsjs/go/app/App";
import { useI18n } from "../lib/i18n";
import { toast } from "../lib/toasts";

type Status = Awaited<ReturnType<typeof MachineStatus>>;
type Service = Awaited<ReturnType<typeof MachineServices>>[number];

const num = (n: number) => (n / 2 ** 30 >= 10 ? (n / 2 ** 30).toFixed(0) : (n / 2 ** 30).toFixed(1));
const gb = (n: number) => num(n) + " GB";
const pct = (used: number, total: number) => (total ? Math.round((used / total) * 100) : 0);
const uptime = (s: number) => {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`;
};
const errText = (e: unknown) => String(e instanceof Error ? e.message : e);

/**
 * A machine as something to look after: how it is doing, its services with
 * their logs and a restart, and a command line. The machine is a linked
 * agent's name, or "core".
 */
export default function MachineHost({ name }: { name: string }) {
  const { t } = useI18n();
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState("");
  const [tab, setTab] = useState<"" | "services" | "command">("");
  const [services, setServices] = useState<Service[] | null>(null);
  const [out, setOut] = useState<{ title: string; text: string } | null>(null);
  const [busy, setBusy] = useState("");
  const [confirm, setConfirm] = useState("");
  const [cmd, setCmd] = useState("");

  const load = useCallback(() => {
    setError("");
    MachineStatus(name).then(setStatus).catch((e) => setError(errText(e)));
  }, [name]);
  useEffect(() => { setStatus(null); setServices(null); setOut(null); setTab(""); load(); }, [load]);
  useEffect(() => {
    if (tab === "services" && !services) MachineServices(name).then((s) => setServices(s ?? [])).catch((e) => setError(errText(e)));
  }, [tab, services, name]);

  const logs = (svc: string) => {
    setBusy("log" + svc);
    MachineLogs(name, svc, 80).then((text) => setOut({ title: svc, text: text || t("The log is empty.") }))
      .catch((e) => setOut({ title: svc, text: errText(e) })).finally(() => setBusy(""));
  };
  const restart = (svc: string) => {
    if (confirm !== svc) { setConfirm(svc); return; }
    setConfirm(""); setBusy("restart" + svc);
    MachineService(name, svc, "restart").then((r) => { toast.success(r); setServices(null); })
      .catch((e) => toast.error(errText(e))).finally(() => setBusy(""));
  };
  const run = (e: React.FormEvent) => {
    e.preventDefault();
    const c = cmd.trim();
    if (!c) return;
    setBusy("exec");
    MachineExec(name, c, 60).then((r) => setOut({ title: `$ ${c}`, text: `${r.output}${r.output.endsWith("\n") || !r.output ? "" : "\n"}${t("exit {code}", { code: r.exit })}` }))
      .catch((er) => setOut({ title: `$ ${c}`, text: errText(er) })).finally(() => setBusy(""));
  };

  if (error && !status) return <p className="wi-note">{error}</p>;
  if (!status) return <p className="wi-note">{t("Reading the machine…")}</p>;

  const disk = status.disks.reduce<Status["disks"][number] | null>((a, d) => (!a || d.total > a.total || (d.total === a.total && d.used > a.used) ? d : a), null);
  return (
    <div className="wm">
      <div className="wm-gauges">
        <Gauge label={t("Load")} value={status.load[0]?.toFixed(2) ?? "–"} sub={t("{n} CPUs", { n: status.cpus })} level={status.load[0] / Math.max(1, status.cpus)} />
        <Gauge label={t("RAM")} value={`${pct(status.memory.used, status.memory.total)}%`} sub={`${num(status.memory.used)} / ${gb(status.memory.total)}`} level={status.memory.used / Math.max(1, status.memory.total)} />
        {disk && <Gauge label={t("Disk")} value={`${pct(disk.used, disk.total)}%`} sub={`${gb(disk.total - disk.used)} ${t("free")}`} level={disk.used / disk.total} />}
      </div>
      <p className="wi-note">{t("Up {time}", { time: uptime(status.uptimeSeconds) })}{status.kernel ? `, ${status.os} ${status.kernel}` : ""}{status.version ? `, SuperLeo ${status.version.split("-")[0]}` : ""}</p>
      <div className="wi-clis">
        <button type="button" className={tab === "services" ? "on" : ""} onClick={() => setTab(tab === "services" ? "" : "services")}>{t("Services")}</button>
        {status.control && <button type="button" className={tab === "command" ? "on" : ""} onClick={() => setTab(tab === "command" ? "" : "command")}>{t("Command")}</button>}
        <button type="button" title={t("Refresh")} onClick={() => { load(); setServices(null); }}><RefreshCwIcon size={12} /></button>
      </div>
      {!status.control && <p className="wi-note">{t("This machine's owner turned off commands and service actions.")}</p>}
      {tab === "services" && (
        services === null ? <p className="wi-note">{t("Reading services…")}</p> : (
          <ul className="wi-list wm-svcs">
            {services.length === 0 && <li className="wi-none">{t("No services.")}</li>}
            {services.slice(0, 40).map((s) => (
              <li key={s.name} title={s.about || s.name}>
                <i className={`wi-dot ${s.state === "running" ? "done" : s.state === "failed" ? "failed" : ""}`} />
                <span className="wm-n">{s.name}</span>
                <button className="wi-x" title={t("Log")} disabled={busy === "log" + s.name} onClick={() => logs(s.name)}><ScrollTextIcon size={11} /></button>
                {status.control && (
                  <button className={`wi-x${confirm === s.name ? " wm-sure" : ""}`} title={t(confirm === s.name ? "Restart it?" : "Restart")} disabled={busy === "restart" + s.name} onClick={() => restart(s.name)}>
                    <RotateCwIcon size={11} />
                  </button>
                )}
              </li>
            ))}
          </ul>
        )
      )}
      {tab === "command" && status.control && (
        <form className="wi-say" onSubmit={run}>
          <input value={cmd} onChange={(e) => setCmd(e.target.value)} placeholder={t("A shell command on {name}", { name })} spellCheck={false} />
          <button type="submit" disabled={!cmd.trim() || busy === "exec"}><TerminalIcon size={14} /></button>
        </form>
      )}
      {busy === "exec" && <p className="wi-note">{t("Running…")}</p>}
      {out && <div className="wm-out"><em>{out.title}</em><pre className="wi-text">{out.text}</pre></div>}
    </div>
  );
}

function Gauge({ label, value, sub, level }: { label: string; value: string; sub: string; level: number }) {
  const l = Math.max(0, Math.min(1, level || 0));
  return (
    <div className={`wm-g${l >= 0.9 ? " hot" : l >= 0.75 ? " warm" : ""}`}>
      <span>{label}</span>
      <b>{value}</b>
      <small>{sub}</small>
      <i style={{ width: `${l * 100}%` }} />
    </div>
  );
}
