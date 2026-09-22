'use client';

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';

import { realtimeURL } from '@/lib/api';
import type { RealtimeEvent, RealtimeEventType } from '@/lib/types';

type Listener = (event: RealtimeEvent) => void;

interface RealtimeContextValue {
  connected: boolean;
  subscribe: (listener: Listener) => () => void;
  /**
   * Sends one frame to the server. Dropped silently when the socket is not
   * open: presence is the only thing that travels this way, and a caller that
   * cares re-sends when `connected` flips back to true.
   */
  send: (frame: object) => void;
}

const RealtimeContext = createContext<RealtimeContextValue>({
  connected: false,
  subscribe: () => () => {},
  send: () => {},
});

const MAX_BACKOFF_MS = 15_000;
/** How long a connection must hold before its success clears the backoff. */
const STABLE_MS = 5_000;

/**
 * Holds the single WebSocket to the Go backend and fans events out to hooks.
 *
 * Reconnects with exponential backoff, and re-mints the URL on every attempt so
 * a refreshed Supabase access token is picked up automatically.
 */
export function RealtimeProvider({ children }: { children: ReactNode }) {
  const [connected, setConnected] = useState(false);
  const listeners = useRef(new Set<Listener>());
  const socketRef = useRef<WebSocket | null>(null);

  const subscribe = useCallback((listener: Listener) => {
    listeners.current.add(listener);
    return () => {
      listeners.current.delete(listener);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    let attempt = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let openedAt = 0;

    /** Queue another attempt. The only way back from any failure. */
    function retry() {
      if (cancelled) return;
      const delay = Math.min(1000 * 2 ** attempt, MAX_BACKOFF_MS);
      attempt += 1;
      retryTimer = setTimeout(() => void connect(), delay);
    }

    async function connect() {
      if (cancelled) return;

      // Two ways this can come back without a URL, and both used to end the
      // effect for good:
      //
      //   - null, because Supabase has not finished restoring the session.
      //     That is the ordinary case on a cold load, so giving up here meant
      //     realtime never connected at all and the inbox only updated on a
      //     manual refresh.
      //   - a rejection, because refreshing the token failed. A transient
      //     network error should not cost the tab its live connection.
      //
      // Both now fall through to the same backoff as a dropped socket.
      let url: string | null = null;
      try {
        url = await realtimeURL();
      } catch {
        retry();
        return;
      }
      if (cancelled) return;
      if (!url) {
        retry();
        return;
      }

      const ws = new WebSocket(url);
      socketRef.current = ws;

      ws.onopen = () => {
        if (cancelled) return;
        openedAt = Date.now();
        setConnected(true);
      };

      ws.onmessage = (event) => {
        let parsed: RealtimeEvent;
        try {
          parsed = JSON.parse(event.data as string) as RealtimeEvent;
        } catch {
          // Ignore frames we cannot parse rather than tearing the socket down.
          return;
        }
        // Each listener is isolated. Previously one throwing subscriber was
        // caught by the same block as the parse, so it was both silently
        // swallowed and allowed to skip every listener queued after it: one
        // buggy component could stop realtime for the whole page.
        listeners.current.forEach((listener) => {
          try {
            listener(parsed);
          } catch (err) {
            console.error('realtime listener failed', err);
          }
        });
      };

      ws.onclose = () => {
        setConnected(false);
        socketRef.current = null;
        if (cancelled) return;
        // Reset the backoff only after a connection that actually lasted. A
        // server that accepts and immediately closes would otherwise reset the
        // counter on every attempt and be retried in a tight loop.
        if (openedAt && Date.now() - openedAt > STABLE_MS) attempt = 0;
        openedAt = 0;
        retry();
      };

      ws.onerror = () => {
        ws.close();
      };
    }

    void connect();

    return () => {
      cancelled = true;
      if (retryTimer) clearTimeout(retryTimer);
      socketRef.current?.close();
      socketRef.current = null;
    };
  }, []);

  const send = useCallback((frame: object) => {
    const ws = socketRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    try {
      ws.send(JSON.stringify(frame));
    } catch {
      // A socket that is closing between the check and the send. The next
      // reconnect re-announces presence anyway.
    }
  }, []);

  const value = useMemo(() => ({ connected, subscribe, send }), [connected, subscribe, send]);

  return <RealtimeContext.Provider value={value}>{children}</RealtimeContext.Provider>;
}

/** Reports whether the realtime socket is currently up. */
export function useRealtimeStatus() {
  return useContext(RealtimeContext).connected;
}

/**
 * Announces which conversation this browser has open, so colleagues on the
 * same number see "sedang dibuka oleh". Re-sent on every reconnect, because
 * the server forgets presence when a socket drops.
 */
export function usePresenceAnnounce(conversationId: string | null) {
  const { send, connected } = useContext(RealtimeContext);

  useEffect(() => {
    if (!connected) return;
    send({ type: 'presence.view', conversation_id: conversationId });
    // Leaving the page, or switching away, is also worth announcing: without
    // it the name would linger on the thread until the socket itself closed.
    return () => send({ type: 'presence.view', conversation_id: null });
  }, [conversationId, connected, send]);
}

/**
 * Runs `handler` for every event of the given types.
 *
 * The handler is kept in a ref so callers can pass an inline closure without
 * resubscribing on every render.
 */
export function useRealtimeEvent<T = unknown>(
  types: RealtimeEventType | RealtimeEventType[],
  handler: (payload: T, event: RealtimeEvent<T>) => void,
) {
  const { subscribe } = useContext(RealtimeContext);
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  const wanted = useMemo(() => (Array.isArray(types) ? types : [types]), [types]);
  const key = wanted.join('|');

  useEffect(() => {
    const accepted = new Set(key.split('|'));
    return subscribe((event) => {
      if (!accepted.has(event.type)) return;
      handlerRef.current(event.payload as T, event as RealtimeEvent<T>);
    });
  }, [key, subscribe]);
}
