"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { activityFromEvent, activityKeys, addActivity } from "./activity";
import { API_URL, refreshSession } from "./api";
import { applyEvent, DOMAIN_EVENTS, eventTouchesTask, type PresenceUser, type StreamEvent } from "./board-events";
import { boardKeys, type BoardDetail } from "./boards";
import { workspaceKeys, type Member } from "./workspaces";

export type ConnectionStatus = "connecting" | "live" | "reconnecting" | "offline";

/** How long a dropped connection counts as "reconnecting" before it shows as offline. */
const OFFLINE_AFTER_MS = 10_000;
/** Backoff for reopening a stream the browser gave up on (an HTTP error). */
const REOPEN_BASE_MS = 3000;
const REOPEN_MAX_MS = 30_000;

/**
 * Keeps the board's cache in step with GET /boards/{id}/events (docs/EVENTS.md).
 *
 * - Each domain event goes into the TanStack Query cache; task events already
 *   reflected (same or lower version) are skipped.
 * - Events about the task being dragged wait until it is dropped (`holdTask`).
 * - While the board is being fetched, events wait for the result and then apply on
 *   top of it, so a refetch can't wipe out what arrived meanwhile.
 * - The browser reconnects by itself, sending Last-Event-ID. When it gives up (an
 *   HTTP error such as an expired access cookie), the session is renewed and a new
 *   stream continues from the last id with `?last_event_id=`.
 * - `resync` reloads the board.
 */
export function useBoardEvents(boardId: string, { enabled }: { enabled: boolean }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<ConnectionStatus>("connecting");
  const [presence, setPresence] = useState<PresenceUser[]>([]);
  const held = useRef<{ taskId: string | null; queue: StreamEvent[] }>({ taskId: null, queue: [] });
  const applyRef = useRef<(event: StreamEvent) => void>(() => {});

  useEffect(() => {
    if (!enabled) return;
    const queryKey = boardKeys.detail(boardId);
    const keyHash = JSON.stringify(queryKey);
    let source: EventSource | null = null;
    let lastEventId = "";
    let reopenTimer: ReturnType<typeof setTimeout> | undefined;
    let offlineTimer: ReturnType<typeof setTimeout> | undefined;
    let attempts = 0;
    let stopped = false;
    const waitingForFetch: StreamEvent[] = [];

    const board = () => queryClient.getQueryData<BoardDetail>(queryKey);
    // Names the events don't carry: from the members list, or whoever is connected.
    const present = new Map<string, string>();
    const nameOf = (userId: string) => {
      const workspaceId = board()?.workspace_id;
      const members = workspaceId ? queryClient.getQueryData<Member[]>(workspaceKeys.members(workspaceId)) : undefined;
      return members?.find((m) => m.user_id === userId)?.name ?? present.get(userId);
    };

    const applyNow = (event: StreamEvent) => {
      const current = board();
      if (!current) return;
      const next = applyEvent(current, event, nameOf);
      if (next === "refetch") void queryClient.invalidateQueries({ queryKey, exact: true });
      else if (next !== current) queryClient.setQueryData(queryKey, next);
    };

    const apply = (event: StreamEvent) => {
      if (queryClient.isFetching({ queryKey, exact: true }) > 0) {
        waitingForFetch.push(event);
      } else if (held.current.taskId && eventTouchesTask(event, held.current.taskId)) {
        held.current.queue.push(event);
      } else {
        applyNow(event);
      }
    };
    applyRef.current = apply;

    // A fetch replaces the whole board; what arrived while it ran goes on top.
    const unsubscribe = queryClient.getQueryCache().subscribe((e) => {
      if (e.type !== "updated" || JSON.stringify(e.query.queryKey) !== keyHash) return;
      if (e.action.type !== "success" && e.action.type !== "error") return;
      queueMicrotask(() => waitingForFetch.splice(0).forEach(apply));
    });

    const reload = () => void queryClient.invalidateQueries({ queryKey, exact: true });

    // Down for a while (or the browser says it's offline): offline until a stream opens.
    let offline = false;
    const markDown = () => {
      setStatus(offline || !navigator.onLine ? "offline" : "reconnecting");
      offlineTimer ??= setTimeout(() => {
        offline = true;
        setStatus("offline");
      }, OFFLINE_AFTER_MS);
    };
    const markLive = () => {
      clearTimeout(offlineTimer);
      offlineTimer = undefined;
      offline = false;
      attempts = 0;
      setStatus("live");
    };

    const open = () => {
      const url = new URL(`/boards/${boardId}/events`, API_URL);
      // A new EventSource doesn't carry Last-Event-ID; continue where the last one left off.
      if (lastEventId) url.searchParams.set("last_event_id", lastEventId);
      const es = new EventSource(url, { withCredentials: true });
      source = es;

      es.onopen = () => {
        markLive();
        // Without a last id (so no Last-Event-ID either) the stream starts live:
        // anything between the board's GET and now was missed, so read the board
        // again (events wait for it). With one, the stream replays what was missed.
        if (!lastEventId) reload();
      };

      es.onerror = () => {
        if (stopped) return;
        markDown();
        // CONNECTING: the browser retries by itself, with Last-Event-ID.
        if (es.readyState !== EventSource.CLOSED) return;
        // CLOSED: an HTTP error. Most likely the access cookie expired; renew the
        // session and open a new stream. A 403 shows up when the board reloads.
        es.close();
        const delay = Math.min(REOPEN_MAX_MS, REOPEN_BASE_MS * 2 ** attempts++);
        reopenTimer = setTimeout(async () => {
          try {
            // false: the session is over; the app signs out and this unmounts.
            if (!(await refreshSession())) return;
          } catch {
            // The API didn't answer; just try again.
          }
          if (stopped) return;
          reload();
          open();
        }, delay);
      };

      const remember = (e: MessageEvent) => {
        if (e.lastEventId) lastEventId = e.lastEventId;
      };
      for (const type of DOMAIN_EVENTS) {
        es.addEventListener(type, (e) => {
          remember(e);
          const event = parse<StreamEvent>(e);
          if (event?.board_id !== boardId) return;
          // The activity feed is a log: it takes every event, held or not.
          addActivity(queryClient, boardId, activityFromEvent(event, nameOf));
          apply(event);
        });
      }
      es.addEventListener("resync", (e) => {
        remember(e);
        reload();
        void queryClient.invalidateQueries({ queryKey: activityKeys.board(boardId) });
      });
      es.addEventListener("presence.snapshot", (e) => {
        const data = parse<{ users: PresenceUser[] }>(e);
        if (!data) return;
        data.users.forEach((u) => present.set(u.id, u.name));
        setPresence(data.users);
      });
      es.addEventListener("presence.joined", (e) => {
        const user = parse<{ user: PresenceUser }>(e)?.user;
        if (user) {
          present.set(user.id, user.name);
          setPresence((users) =>
            users.some((u) => u.id === user.id)
              ? users
              : [...users, user].sort((a, b) => a.name.localeCompare(b.name, "es")),
          );
        }
      });
      es.addEventListener("presence.left", (e) => {
        const user = parse<{ user: PresenceUser }>(e)?.user;
        if (user) setPresence((users) => users.filter((u) => u.id !== user.id));
      });
    };

    // Back online: don't wait for the next retry.
    const onOnline = () => {
      if (source?.readyState === EventSource.OPEN) return;
      source?.close();
      clearTimeout(reopenTimer);
      open();
    };
    const onOffline = () => {
      if (source?.readyState !== EventSource.OPEN) setStatus("offline");
    };
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);

    open();

    return () => {
      stopped = true;
      source?.close();
      clearTimeout(reopenTimer);
      clearTimeout(offlineTimer);
      unsubscribe();
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
      applyRef.current = () => {};
    };
  }, [boardId, enabled, queryClient]);

  /**
   * While a task is dragged, events about it wait; pass null on drop (after the
   * drop's own optimistic change) to apply them.
   */
  const holdTask = useCallback((taskId: string | null) => {
    const queue = taskId === null ? held.current.queue.splice(0) : [];
    held.current.taskId = taskId;
    queue.forEach((event) => applyRef.current(event));
  }, []);

  return { status, presence, holdTask };
}

function parse<T>(e: MessageEvent): T | null {
  try {
    return JSON.parse(e.data as string) as T;
  } catch {
    return null;
  }
}
