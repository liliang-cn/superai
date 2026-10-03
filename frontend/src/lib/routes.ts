import { ViewKey } from "./types";

// Where each screen lives.
//
// The app used to be one component holding "which screen" in state, which made
// every screen unreachable except by clicking to it: no link to send, no back
// button, and a reload landed on Chat whatever had been open. The address is now
// the state, and this is the table both directions read.
//
// Hash routing, not history routing. The same bundle runs inside the desktop
// window, where there is no server to answer an arbitrary path with the app, and
// in a browser behind one; a path after the # works identically in both.
export const PATHS: Record<ViewKey, string> = {
  home: "/",
  chat: "/chat",
  stats: "/stats",
  hive: "/hive",
  agents: "/agents",
  coding: "/coding",
  tasks: "/tasks",
  dashboards: "/dashboards",
  knowledge: "/knowledge",
  skills: "/skills",
  mcp: "/mcp",
  records: "/records",
  settings: "/settings",
};

/** Which screen an address belongs to. Anything unknown is home. */
export function viewOf(pathname: string): ViewKey {
  if (pathname === "/" || pathname === "") return "home";
  const first = "/" + pathname.split("/")[1];
  const hit = (Object.keys(PATHS) as ViewKey[]).find((k) => PATHS[k] === first);
  return hit ?? "home";
}

/** The address of one hive task. The id is a UUID, so it needs no escaping. */
export const taskPath = (id: string) => `/hive/tasks/${id}`;
