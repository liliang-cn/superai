import type { AttentionItem } from "../canvas/attention";

/** A watch agent's repeated updates are one item; its full history stays on Bees. */
export function summarizeAttention(items: AttentionItem[]): AttentionItem[] {
  const result: AttentionItem[] = [];
  const reports = new Map<string, number>();
  for (const item of items) {
    if (item.kind !== "report" || !item.ref) { result.push(item); continue; }
    const index = reports.get(item.ref);
    if (index === undefined) {
      reports.set(item.ref, result.length);
      result.push({ ...item, updates: 1 });
    } else {
      const previous = result[index];
      const latest = Date.parse(item.at ?? "") > Date.parse(previous.at ?? "") ? item : previous;
      result[index] = { ...latest, updates: (previous.updates ?? 1) + 1 };
    }
  }
  return result;
}
