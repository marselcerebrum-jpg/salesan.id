/**
 * Cuts message text into the pieces that are more than text.
 *
 * Three things hide inside a plain WhatsApp message: a mention, a link, and a
 * phone number. They are found in that order and each stage only sees what the
 * previous one left behind, which is what keeps them from fighting over the
 * same characters. A mention is written as "@6285171593270", so without the
 * ordering a mention would also read as a phone number; a URL's path is full of
 * digits, so without it "wa.me/628123456789" would be half a link and half a
 * number.
 *
 * Nothing here decides anything irreversible. A detected number only earns a
 * menu, and the number is re-read by the server before any chat is opened — the
 * rule for reading a number lives there, next to the contact import that shares
 * it. This file decides what is worth offering, not what it means.
 */

export type MessageToken =
  | { kind: 'text'; text: string }
  | { kind: 'mention'; text: string; jid: string; user: string }
  | { kind: 'url'; text: string; href: string }
  | { kind: 'phone'; text: string; phone: string };

export function tokenizeMessage(text: string, mentionedJids: string[]): MessageToken[] {
  if (!text) return [];

  const out: MessageToken[] = [];
  for (const part of splitMentions(text, mentionedJids)) {
    if (typeof part !== 'string') {
      out.push(part);
      continue;
    }
    for (const run of splitURLs(part)) {
      if (typeof run !== 'string') {
        out.push(run);
        continue;
      }
      out.push(...splitPhones(run));
    }
  }
  return out;
}

/** Whether anything in here needs more than a text node. */
export function hasRichToken(tokens: MessageToken[]): boolean {
  return tokens.some((t) => t.kind !== 'text');
}

// ---------------------------------------------------------------- mentions

type Mention = Extract<MessageToken, { kind: 'mention' }>;

/**
 * Cuts out the mentions WhatsApp actually recorded.
 *
 * Driven by the JID list rather than by the "@" in the text: the metadata is
 * the fact, the text is only what the sender's client chose to render, and a
 * bare "@62812…" that nobody mentioned stays plain. Matching is longest-user
 * first, because a number that is a prefix of another would otherwise swallow
 * the longer one's match and leave a stray digit behind.
 */
function splitMentions(text: string, mentionedJids: string[]): Array<string | Mention> {
  if (mentionedJids.length === 0) return [text];

  const byUser = new Map<string, string>();
  for (const jid of mentionedJids) {
    const user = userPart(jid);
    if (user) byUser.set(user, jid);
  }
  if (byUser.size === 0) return [text];

  const users = [...byUser.keys()].sort((a, b) => b.length - a.length);
  const pattern = new RegExp(`@(${users.map(escapeRegExp).join('|')})`, 'g');

  const out: Array<string | Mention> = [];
  let cursor = 0;
  for (const match of text.matchAll(pattern)) {
    const start = match.index ?? 0;
    if (start > cursor) out.push(text.slice(cursor, start));
    const user = match[1];
    out.push({ kind: 'mention', text: match[0], jid: byUser.get(user)!, user });
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

// -------------------------------------------------------------------- urls

type Url = Extract<MessageToken, { kind: 'url' }>;

/**
 * The endings a bare host is allowed to have.
 *
 * An allowlist, not a pattern, and that is the point: "Rp1.500", "dll.ini" and
 * "jam 19.30" all look exactly like host.tld to a regex. Linking them would put
 * a live link on a price. A host with a path is accepted whatever its ending,
 * since "sesuatu/halaman" is not something anyone writes by accident.
 */
const BARE_TLDS = [
  'com', 'net', 'org', 'info', 'biz', 'id', 'co', 'io', 'me', 'app', 'dev',
  'site', 'online', 'store', 'link', 'xyz', 'tech', 'space', 'web', 'cloud',
  'ai', 'tv', 'sg', 'my', 'au', 'jp', 'uk', 'us',
].join('|');

const HOST = String.raw`[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*`;

/**
 * Scheme links, www links, and bare hosts.
 *
 * The leading group is a boundary rather than a lookbehind: a lookbehind is a
 * syntax error on older Safari, and a regex that fails to parse takes the whole
 * module with it rather than just the link it could not find.
 */
const URL_RE = new RegExp(
  String.raw`(^|[\s(\[])(` +
    // Anything with a scheme, as written.
    String.raw`https?:\/\/\S+` +
    // The host people type instead of a scheme.
    String.raw`|www\.\S+` +
    // A bare host, which needs either a known ending or a path to count.
    String.raw`|${HOST}\.(?:${BARE_TLDS})\b(?:\/\S*)?` +
    String.raw`|${HOST}\.[a-z]{2,24}\/\S*` +
    `)`,
  'gi',
);

/** Looks like somebody's address rather than a site. */
const EMAIL_RE = new RegExp(String.raw`^[^\s@]+@${HOST}\.[a-z]{2,24}$`, 'i');

/** The punctuation that belongs to the sentence, not to the link. */
const TRAILING = ['.', ',', ';', ':', '!', '?', '}', "'", '"', '…'];

function splitURLs(text: string): Array<string | Url> {
  const out: Array<string | Url> = [];
  let cursor = 0;

  for (const match of text.matchAll(URL_RE)) {
    const lead = match[1] ?? '';
    const start = (match.index ?? 0) + lead.length;
    const body = trimPunctuation(match[2]);
    const href = toHref(body);
    if (!href) continue;

    // An address, not a site. Left as text rather than linked: a mailto nobody
    // asked for is worse than a line the operator can copy, and half of it
    // linked — the domain alone — would be worse than both.
    const before = text.slice(0, start).match(/\S+$/);
    if (before && EMAIL_RE.test(before[0] + body)) continue;

    if (start > cursor) out.push(text.slice(cursor, start));
    out.push({ kind: 'url', text: body, href });
    cursor = start + body.length;
  }
  if (cursor < text.length) out.push(text.slice(cursor));
  return out.length > 0 ? out : [text];
}

/**
 * Takes the sentence's punctuation back off the end of a link.
 *
 * "Lihat salesan.id." ends in a full stop that belongs to the sentence, not to
 * the host. Brackets are counted rather than merely looked for: a URL that
 * opens a bracket keeps the one that closes it, which is how many
 * documentation links are genuinely written, but "(lihat https://a/x(y))"
 * gives its last bracket back to the sentence that opened it.
 */
function trimPunctuation(raw: string): string {
  let end = raw.length;
  while (end > 0) {
    const ch = raw[end - 1];
    if (ch === ')' || ch === ']') {
      const open = ch === ')' ? '(' : '[';
      const kept = raw.slice(0, end);
      if (count(kept, open) >= count(kept, ch)) break;
    } else if (!TRAILING.includes(ch)) {
      break;
    }
    end--;
  }
  return raw.slice(0, end);
}

function count(s: string, ch: string): number {
  let n = 0;
  for (const c of s) if (c === ch) n++;
  return n;
}

/**
 * The address the browser is allowed to follow.
 *
 * Only http and https are built, and never from a scheme the text supplied: a
 * scheme that arrived inside a customer's message is not one this page will
 * honour. Anything else returns null and the run stays text.
 */
function toHref(body: string): string | null {
  if (!body) return null;
  const lower = body.toLowerCase();
  if (lower.startsWith('http://') || lower.startsWith('https://')) return body;
  if (/^[a-z][a-z0-9+.-]*:/i.test(body)) return null;
  return `https://${body}`;
}

// ------------------------------------------------------------------ phones

/**
 * A run that could be an Indonesian number as people write them.
 *
 * Anchored on how it starts — "+62", "62", or a trunk "0" — which is what keeps
 * a price, a date or an invoice number out: those start with neither. The
 * leading group is a boundary for the same reason the URL pattern uses one.
 */
const PHONE_RE = /(^|[^\w+])((?:\+62|62|0)[\d\s().-]{7,19}\d)/g;

function splitPhones(text: string): MessageToken[] {
  const out: MessageToken[] = [];
  let cursor = 0;

  for (const match of text.matchAll(PHONE_RE)) {
    const lead = match[1] ?? '';
    const start = (match.index ?? 0) + lead.length;
    const body = match[2];
    const phone = readPhone(body);
    if (!phone) continue;

    if (start > cursor) out.push({ kind: 'text', text: text.slice(cursor, start) });
    out.push({ kind: 'phone', text: body, phone });
    cursor = start + body.length;
  }
  if (cursor < text.length) out.push({ kind: 'text', text: text.slice(cursor) });
  return out;
}

/**
 * The digits, in the one shape WhatsApp addresses use.
 *
 * Deliberately the same rule as the server's NormalizePhone, and deliberately
 * not the authority on it. This decides whether to offer a menu; the server
 * re-reads what the operator pressed and settles which thread it is. Two copies
 * of a rule will drift, so the one that can do damage is the one kept beside
 * the contact import that depends on it.
 */
function readPhone(raw: string): string | null {
  const digits = raw.replace(/\D/g, '');

  // Checked before the country code is added, not after.
  //
  // "08-10-2026" is a date with eight digits. Prefixing it first turns it into
  // a nine digit number that passes any length check applied afterwards, and
  // the operator is offered a chat with 62810-2026. A trunk number is at least
  // ten digits here; a date written this way never is.
  if (digits.startsWith('0')) {
    if (digits.length < 10 || digits.length > 14) return null;
    return '62' + digits.slice(1).replace(/^0+/, '');
  }
  if (digits.length < 11 || digits.length > 15) return null;
  if (digits.startsWith('620')) return '62' + digits.slice(2).replace(/^0+/, '');
  return digits;
}
