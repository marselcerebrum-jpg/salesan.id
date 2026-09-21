'use client';

import clsx from 'clsx';
import { Fragment, useMemo } from 'react';

/**
 * Renders message text with its mentions highlighted.
 *
 * WhatsApp writes a mention into the text as "@6285171593270" and names who it
 * meant in separate metadata. The metadata is the fact — the text is only what
 * the sender's client chose to render — so the highlighting is driven by the
 * JID list, and a bare "@62812..." that nobody actually mentioned is left as
 * plain text.
 *
 * `resolveName` turns a JID into something readable; when it has nothing, the
 * number stays, which is still better than a raw address.
 */
export function MentionText({
  text,
  mentionedJids,
  ownJids,
  resolveName,
  className,
}: {
  text: string;
  mentionedJids: string[];
  /** Addresses belonging to this account, highlighted more strongly. */
  ownJids?: string[];
  resolveName?: (jid: string) => string | null;
  className?: string;
}) {
  const parts = useMemo(
    () => splitMentions(text, mentionedJids),
    [text, mentionedJids],
  );

  if (parts.length === 1 && typeof parts[0] === 'string') {
    return <span className={className}>{text}</span>;
  }

  const own = new Set((ownJids ?? []).map(userPart));

  return (
    <span className={className}>
      {parts.map((part, index) =>
        typeof part === 'string' ? (
          <Fragment key={index}>{part}</Fragment>
        ) : (
          <span
            key={index}
            title={part.jid}
            className={clsx(
              'rounded-sm px-[2px] font-medium',
              own.has(part.user)
                ? // Our own number gets the stronger treatment: in a busy group
                  // it is the one thing the operator is scanning for.
                  'bg-wa-accent/25 text-wa-accent'
                : 'text-wa-accent',
            )}
          >
            @{resolveName?.(part.jid) ?? part.user}
          </span>
        ),
      )}
    </span>
  );
}

interface MentionPart {
  jid: string;
  user: string;
}

/**
 * Cuts the text into plain runs and mention tokens.
 *
 * Matching is by the user part of each mentioned JID, longest first — a number
 * that is a prefix of another would otherwise swallow the longer one's match
 * and leave a stray digit behind.
 */
function splitMentions(text: string, mentionedJids: string[]): Array<string | MentionPart> {
  if (!text || mentionedJids.length === 0) return [text];

  const byUser = new Map<string, string>();
  for (const jid of mentionedJids) {
    const user = userPart(jid);
    if (user) byUser.set(user, jid);
  }
  if (byUser.size === 0) return [text];

  const users = [...byUser.keys()].sort((a, b) => b.length - a.length);
  const pattern = new RegExp(`@(${users.map(escapeRegExp).join('|')})`, 'g');

  const out: Array<string | MentionPart> = [];
  let cursor = 0;

  for (const match of text.matchAll(pattern)) {
    const start = match.index ?? 0;
    if (start > cursor) out.push(text.slice(cursor, start));

    const user = match[1];
    out.push({ jid: byUser.get(user)!, user });
    cursor = start + match[0].length;
  }
  if (cursor < text.length) out.push(text.slice(cursor));

  return out.length > 0 ? out : [text];
}

/** The digits (or LID) before the "@" of a JID. */
function userPart(jid: string): string {
  const at = jid.indexOf('@');
  const user = at >= 0 ? jid.slice(0, at) : jid;
  const colon = user.indexOf(':');
  return colon >= 0 ? user.slice(0, colon) : user;
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
