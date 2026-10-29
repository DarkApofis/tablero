"use client";

import { useEffect, useState } from "react";

const clock = new Intl.DateTimeFormat("es", { hour: "2-digit", minute: "2-digit" });
const dayMonth = new Intl.DateTimeFormat("es", { day: "numeric", month: "short" });

/** "hace 3 min", "ayer, 17:08"… */
export function relative(iso: string, now: number) {
  const then = Date.parse(iso);
  const seconds = Math.max(0, (now - then) / 1000);
  if (seconds < 45) return "hace un momento";
  if (seconds < 3600) return `hace ${Math.max(1, Math.round(seconds / 60))} min`;
  const day = (t: number) => new Date(t).toDateString();
  if (day(then) === day(now)) return seconds < 6 * 3600 ? `hace ${Math.round(seconds / 3600)} h` : `hoy, ${clock.format(then)}`;
  if (day(then) === day(now - 86_400_000)) return `ayer, ${clock.format(then)}`;
  return `${dayMonth.format(then)}, ${clock.format(then)}`;
}

/** The current time, refreshed every `ms` so relative times stay right. */
export function useNow(ms: number) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(timer);
  }, [ms]);
  return now;
}
