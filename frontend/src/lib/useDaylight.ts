import { useEffect, useState } from "react";

/** True unless the dark theme is on, and kept current as the theme changes. */
export function useDaylight(): boolean {
  const read = () => document.documentElement.dataset.theme !== "dark";
  const [light, setLight] = useState(read);
  useEffect(() => {
    const mo = new MutationObserver(() => setLight(read()));
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => mo.disconnect();
  }, []);
  return light;
}
