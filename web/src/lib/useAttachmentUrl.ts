'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { ApiError, getAttachmentUrl } from '@/lib/api';
import { cacheLink, cachedLink, forgetLink } from '@/lib/media';
import type { Attachment } from '@/lib/types';

export interface AttachmentUrlState {
  url: string | null;
  loading: boolean;
  /** Set when the file cannot be produced at all; the caller shows a retry. */
  error: string | null;
  /** True when the file cannot be produced again; retrying will not help. */
  gone: boolean;
  /**
   * True when we deleted our own copy because it passed the retention window.
   *
   * Kept apart from `gone`, because the two are opposite advice. An unavailable
   * file is gone from WhatsApp's servers too and nobody can get it back; an
   * expired one is only gone from here, and is still sitting in the chat on the
   * phone. Telling somebody "no longer available" about a photo they can scroll
   * to on their own handset is simply wrong.
   */
  expired: boolean;
  reload: () => void;
}

/**
 * Resolves an attachment to a displayable URL.
 *
 * `enabled` is what keeps a long thread cheap: a bubble asks for its URL only
 * once it is actually on screen, and a document waits until someone clicks
 * download. The first request for an incoming file also makes the server fetch
 * it from WhatsApp, so it can take a second or two.
 */
export function useAttachmentUrl(
  attachment: Attachment | null,
  enabled: boolean,
): AttachmentUrlState {
  const id = attachment?.id ?? null;
  // Part of the key, not just a field to read.
  //
  // An outgoing file is a row before it is bytes: the bubble appears the moment
  // the message is written, and the upload to our bucket finishes a second or
  // two later. Asking in that gap is answered honestly with "not yet", and
  // without this the answer stuck: the id never changed, so the effect never
  // ran again, and a picture that had been sent successfully sat there claiming
  // it was unavailable. Watching the status means the bubble repairs itself the
  // moment the file lands.
  const status = attachment?.status ?? null;
  const [url, setUrl] = useState<string | null>(() => (id ? cachedLink(id) : null));
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [gone, setGone] = useState(false);
  const [expired, setExpired] = useState(false);
  const [nonce, setNonce] = useState(0);

  // Guards against a response for a previous attachment landing after the
  // component has moved on to another one.
  const currentId = useRef<string | null>(id);
  currentId.current = id;

  useEffect(() => {
    if (!id || !enabled) return;

    const cached = cachedLink(id);
    if (cached) {
      setUrl(cached);
      setError(null);
      setGone(false);
      return;
    }

    let cancelled = false;
    setLoading(true);
    setError(null);

    getAttachmentUrl(id)
      .then((link) => {
        if (cancelled || currentId.current !== id) return;
        cacheLink(id, link.url, link.expires_at);
        setUrl(link.url);
        setGone(false);
      })
      .catch((err: unknown) => {
        if (cancelled || currentId.current !== id) return;
        const apiErr = err instanceof ApiError ? err : null;
        // "Gone" removes the retry, so only the two final answers may set it.
        // A file still being uploaded is neither.
        setGone(apiErr?.code === 'media_unavailable' || apiErr?.code === 'media_expired');
        setExpired(apiErr?.code === 'media_expired');
        setError(apiErr?.message ?? 'Gagal memuat berkas');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [id, enabled, nonce, status]);

  // A new attachment, or one that has just finished uploading, starts from
  // scratch rather than showing the previous URL or the previous failure.
  useEffect(() => {
    setUrl(id ? cachedLink(id) : null);
    setError(null);
    setGone(false);
    setExpired(false);
  }, [id, status]);

  const reload = useCallback(() => {
    if (id) forgetLink(id);
    setUrl(null);
    setNonce((n) => n + 1);
  }, [id]);

  return { url, loading, error, gone, expired, reload };
}

/**
 * What to put on a bubble whose file could not be produced.
 *
 * One place, so the four media types cannot word the same situation three
 * different ways.
 */
export function attachmentFailureText(gone: boolean, expired: boolean): string {
  if (expired) return 'Berkas sudah expired, silakan cek di HP';
  if (gone) return 'Berkas tidak tersedia lagi';
  return 'Gagal memuat';
}

/**
 * Reports whether an element has come into view, so media below the fold does
 * not fetch a signed URL until it is nearly visible.
 *
 * The margin is generous on purpose: by the time an image reaches the viewport
 * its bytes should already be arriving, not just its URL.
 */
export function useOnScreen<T extends Element>(margin = '400px'): [React.RefObject<T | null>, boolean] {
  const ref = useRef<T | null>(null);
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const node = ref.current;
    if (!node) return;
    // Environments without IntersectionObserver (older browsers, some test
    // runners) simply load everything rather than showing nothing.
    if (typeof IntersectionObserver === 'undefined') {
      setVisible(true);
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect(); // one-way: loaded stays loaded
        }
      },
      { rootMargin: margin },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [margin]);

  return [ref, visible];
}
