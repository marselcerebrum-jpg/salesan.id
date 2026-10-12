'use client';

import clsx from 'clsx';
import { Fragment, useMemo } from 'react';

import { hasRichToken, tokenizeMessage } from '@/components/chat/messagetokens';

/**
 * Renders message text with the parts that are more than text made usable.
 *
 * A mention is highlighted and names the person rather than their number. A
 * link is followable. A phone number offers to become a chat. Everything else
 * is left exactly as written, including the line breaks, because a message is
 * somebody's words and this is not the place to tidy them.
 *
 * `resolveName` turns a JID into something readable; when it has nothing, the
 * number stays, which is still better than a raw address.
 */
export function MessageText({
  text,
  mentionedJids,
  ownJids,
  resolveName,
  onPickNumber,
  className,
}: {
  text: string;
  mentionedJids: string[];
  /** Addresses belonging to this account, highlighted more strongly. */
  ownJids?: string[];
  resolveName?: (jid: string) => string | null;
  /**
   * Opens the menu for a number found in the text.
   *
   * Absent on a surface where there is nowhere for that menu to go, and then
   * the number renders as plain text rather than as a button that does
   * nothing.
   */
  onPickNumber?: (phone: string, written: string) => void;
  className?: string;
}) {
  const tokens = useMemo(() => tokenizeMessage(text, mentionedJids), [text, mentionedJids]);

  if (!hasRichToken(tokens)) {
    return <span className={className}>{text}</span>;
  }

  const own = new Set((ownJids ?? []).map(userPart));

  return (
    <span className={className}>
      {tokens.map((token, index) => {
        switch (token.kind) {
          case 'mention':
            return (
              <span
                key={index}
                title={token.jid}
                className={clsx(
                  'rounded-sm px-[2px] font-medium',
                  own.has(token.user)
                    ? // Our own number gets the stronger treatment: in a busy
                      // group it is the one thing the operator is scanning for.
                      'bg-wa-accent/25 text-wa-accent'
                    : 'text-wa-accent',
                )}
              >
                @{resolveName?.(token.jid) ?? token.user}
              </span>
            );

          case 'url':
            return (
              <a
                key={index}
                href={token.href}
                target="_blank"
                // noreferrer as well as noopener: the new tab must not be able
                // to reach back into this page, and a customer's link has no
                // business learning which thread it was sent in.
                rel="noopener noreferrer nofollow"
                // The bubble underneath listens for clicks of its own. Without
                // this, following a link would also select or quote the message.
                onClick={(event) => event.stopPropagation()}
                className="font-medium break-all text-wa-accent underline decoration-wa-accent/40 underline-offset-2 hover:decoration-wa-accent"
              >
                {token.text}
              </a>
            );

          case 'phone':
            return onPickNumber ? (
              <button
                key={index}
                type="button"
                onClick={(event) => {
                  event.stopPropagation();
                  onPickNumber(token.phone, token.text);
                }}
                title="Lihat pilihan untuk nomor ini"
                className="font-medium text-wa-accent underline decoration-dotted decoration-wa-accent/50 underline-offset-2 hover:decoration-solid"
              >
                {token.text}
              </button>
            ) : (
              <Fragment key={index}>{token.text}</Fragment>
            );

          default:
            return <Fragment key={index}>{token.text}</Fragment>;
        }
      })}
    </span>
  );
}

/** The digits (or LID) before the "@" of a JID. */
function userPart(jid: string): string {
  const at = jid.indexOf('@');
  const user = at >= 0 ? jid.slice(0, at) : jid;
  const colon = user.indexOf(':');
  return colon >= 0 ? user.slice(0, colon) : user;
}
