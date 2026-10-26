"use client";

import { Avatar } from "@/components/ui/avatar";
import { cx } from "@/components/ui/cx";
import { useMe } from "@/lib/auth";
import type { PresenceUser } from "@/lib/board-events";
import type { ConnectionStatus } from "@/lib/use-board-events";

const statuses: Record<ConnectionStatus, { label: string; tone: string; pulse: boolean }> = {
  connecting: { label: "Conectando…", tone: "text-muted border-border-strong", pulse: false },
  live: { label: "En vivo", tone: "text-accent-text border-accent-text/40 bg-accent-soft", pulse: true },
  reconnecting: { label: "Reconectando…", tone: "text-warn border-warn/40", pulse: true },
  offline: { label: "Sin conexión", tone: "text-danger border-danger/40", pulse: false },
};

/** Whether the board is getting live updates. */
export function ConnectionBadge({ status }: { status: ConnectionStatus }) {
  const s = statuses[status];
  return (
    <span
      role="status"
      className={cx(
        "inline-flex shrink-0 items-center gap-[7px] whitespace-nowrap rounded-full border px-2.5 py-1 text-[12.5px] font-semibold",
        s.tone,
      )}
    >
      <span
        aria-hidden
        className={cx("size-[7px] rounded-full bg-current", s.pulse && "motion-safe:animate-[tb-pulse_1.8s_ease-in-out_infinite]")}
      />
      {/* On a phone, the dot alone; the label is still read out. */}
      <span className="max-sm:sr-only">{s.label}</span>
    </span>
  );
}

const MAX_SHOWN = 4;

/** Who has the board open, from the stream's presence messages. Dimmed while offline. */
export function PresenceAvatars({ users, stale }: { users: PresenceUser[]; stale: boolean }) {
  const { data: me } = useMe();
  if (users.length === 0) return null;
  const shown = users.slice(0, MAX_SHOWN);
  const hidden = users.slice(MAX_SHOWN);
  const nameOf = (u: PresenceUser) => (u.id === me?.id ? `${u.name} (tú)` : u.name);

  return (
    <div
      role="group"
      aria-label={`Conectados: ${users.map(nameOf).join(", ")}`}
      className={cx("flex items-center pl-2 transition-opacity", stale && "opacity-50")}
    >
      {shown.map((u) => (
        <span key={u.id} aria-hidden title={nameOf(u)} className="-ml-2">
          <Avatar name={u.name} size={28} ring={u.id === me?.id} />
        </span>
      ))}
      {hidden.length > 0 && (
        <span
          aria-hidden
          title={hidden.map(nameOf).join(", ")}
          className="-ml-2 flex size-7 items-center justify-center rounded-full border border-border-strong bg-raised text-[11px] font-semibold text-muted shadow-[0_0_0_2px_var(--surface)]"
        >
          +{hidden.length}
        </span>
      )}
    </div>
  );
}
