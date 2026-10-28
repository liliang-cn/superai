// What the copy calls the machine SuperLeo runs on. In the desktop window that
// machine is the one in front of the person: a Mac, or a PC on Windows. A
// browser may be on any machine at all, so the served build says computer.
const served = Boolean((window as unknown as Record<string, unknown>).superleoServed);
const ua = navigator.userAgent;

export const machine: "Mac" | "PC" | "computer" =
  served ? "computer" : /Windows/i.test(ua) ? "PC" : /Macintosh|Mac OS/i.test(ua) ? "Mac" : "computer";
