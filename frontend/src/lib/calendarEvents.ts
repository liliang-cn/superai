/** Match the same meeting saved twice while retaining unrelated appointments. */
export function calendarEvents<T extends Record<string, any>>(items: T[]): T[] {
  const seen = new Set<string>();
  return items.filter(event => {
    const start = typeof event.start_at === "string" ? Date.parse(event.start_at) : NaN;
    if (!Number.isFinite(start)) return true;
    const participants = Array.isArray(event.participants) ? event.participants.map(String).map(name => name.trim()).sort() : [];
    const identity = participants.length ? participants.join("\u0000") : String(event.title ?? event.subject ?? event.id ?? "");
    const key = `${start}|${identity}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
