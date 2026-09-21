import type { Attachment, AttachmentKind } from '@/lib/types';

/**
 * Client-side limits, mirroring backend/internal/media. They exist to give
 * immediate feedback — "this video is too big" before a 90-second upload — not
 * to enforce anything. The server re-checks every one of them, and it is the
 * server's answer that counts.
 */
export const MAX_BYTES: Record<AttachmentKind, number> = {
  image: 16 * 1024 * 1024,
  video: 64 * 1024 * 1024,
  audio: 16 * 1024 * 1024,
  document: 100 * 1024 * 1024,
  sticker: 16 * 1024 * 1024,
};

/** Files WhatsApp shows inline; everything else travels as a document. */
export const IMAGE_MIME = ['image/jpeg', 'image/png', 'image/webp', 'image/gif'];
export const VIDEO_MIME = ['video/mp4', 'video/3gpp', 'video/quicktime', 'video/webm'];

/** `accept` attribute for the "Foto & Video" picker. */
export const PHOTO_VIDEO_ACCEPT = [...IMAGE_MIME, ...VIDEO_MIME].join(',');

/**
 * Extensions the server refuses outright. Listed here only so the picker can
 * say so straight away rather than after an upload round trip.
 */
const BLOCKED_EXT = new Set([
  'exe', 'com', 'bat', 'cmd', 'msi', 'scr', 'pif', 'cpl', 'dll', 'sys',
  'vbs', 'vbe', 'js', 'jse', 'wsf', 'wsh', 'ps1', 'psm1', 'sh', 'bash',
  'jar', 'apk', 'app', 'deb', 'rpm', 'lnk', 'reg', 'hta', 'chm',
  'html', 'htm', 'svg', 'xhtml',
]);

export function extensionOf(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
}

/** Classifies a picked file the way the server will, for preview purposes. */
export function kindOfFile(file: File, asDocument: boolean): AttachmentKind {
  if (asDocument) return 'document';
  if (IMAGE_MIME.includes(file.type)) return 'image';
  if (VIDEO_MIME.includes(file.type)) return 'video';
  if (file.type.startsWith('audio/')) return 'audio';
  return 'document';
}

/**
 * Checks a file before it is queued, returning a human message or null.
 *
 * Deliberately permissive about types: the server does the real classification
 * from the file's own bytes, and guessing here from `file.type` — which the OS
 * fills in from the extension — would reject valid files the server accepts.
 * Only the two checks that cannot be wrong are made: a blocked extension, and
 * a size over the limit for what this file will be sent as.
 */
export function validateFile(file: File, asDocument: boolean): string | null {
  if (file.size === 0) return 'Berkas kosong';

  const ext = extensionOf(file.name);
  if (BLOCKED_EXT.has(ext)) {
    return `Tipe .${ext} tidak diizinkan`;
  }

  const kind = kindOfFile(file, asDocument);
  const limit = MAX_BYTES[kind];
  if (file.size > limit) {
    return `Terlalu besar. Maksimal ${formatBytes(limit)} untuk ${KIND_LABEL[kind].toLowerCase()}.`;
  }
  return null;
}

/**
 * The files on a clipboard, if any.
 *
 * Two sources, because browsers disagree. A file copied in the file manager
 * arrives in `files`; an image copied out of a web page or a screenshot tool
 * usually arrives only as an `item` of kind "file", and Safari has historically
 * filled one but not the other. Reading both and de-duplicating is the only way
 * to catch every case.
 *
 * A clipboard carrying only text returns an empty list, which is what tells the
 * caller to leave the paste alone and let the browser insert the text.
 */
export function filesFromClipboard(data: DataTransfer | null): File[] {
  if (!data) return [];

  const out: File[] = [];
  const seen = new Set<string>();
  const take = (file: File | null) => {
    if (!file || file.size === 0) return;
    // Name, size and mtime together: enough to spot the same file arriving
    // through both channels, and cheap.
    const key = `${file.name}:${file.size}:${file.lastModified}`;
    if (seen.has(key)) return;
    seen.add(key);
    out.push(file);
  };

  for (const file of Array.from(data.files ?? [])) take(file);
  for (const item of Array.from(data.items ?? [])) {
    if (item.kind === 'file') take(item.getAsFile());
  }
  return out;
}

/**
 * A readable name for a file the clipboard did not name.
 *
 * A pasted screenshot is called "image.png" every time, so three of them in a
 * row are three identical names in the queue and three identical names on the
 * recipient's phone. The timestamp is what the operator would have typed.
 */
export function nameClipboardFile(file: File, at = new Date()): File {
  const generic = !file.name || file.name.toLowerCase() === 'image.png';
  if (!generic) return file;

  const stamp = at
    .toLocaleString('sv-SE', { timeZone: 'Asia/Jakarta' })
    .replace(/[: ]/g, '-');
  const ext = extensionOf(file.name) || (file.type.split('/')[1] ?? 'png');
  return new File([file], `tempel-${stamp}.${ext}`, {
    type: file.type,
    lastModified: file.lastModified,
  });
}

export const KIND_LABEL: Record<AttachmentKind, string> = {
  image: 'Foto',
  video: 'Video',
  audio: 'Audio',
  document: 'Dokumen',
  sticker: 'Stiker',
};

/** "1,4 MB" — Indonesian decimal comma, binary units. */
export function formatBytes(bytes: number | null | undefined): string {
  if (!bytes || bytes <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const rounded = unit === 0 ? String(Math.round(value)) : value.toFixed(1).replace('.', ',');
  return `${rounded} ${units[unit]}`;
}

/** "0:42" / "1:03:20" for a duration in seconds. */
export function formatDuration(seconds: number | null | undefined): string {
  if (!seconds || seconds <= 0) return '';
  const s = Math.round(seconds);
  const hh = Math.floor(s / 3600);
  const mm = Math.floor((s % 3600) / 60);
  const ss = s % 60;
  const pad = (n: number) => String(n).padStart(2, '0');
  return hh > 0 ? `${hh}:${pad(mm)}:${pad(ss)}` : `${mm}:${pad(ss)}`;
}

/** The inline preview WhatsApp shipped with the message, if any. */
export function thumbnailSrc(attachment: Attachment): string | null {
  return attachment.thumbnail_b64 ? `data:image/jpeg;base64,${attachment.thumbnail_b64}` : null;
}

/** How large a media bubble is allowed to get, in CSS pixels. */
const MEDIA_MAX_WIDTH = 300;
const MEDIA_MAX_HEIGHT = 340;

/**
 * Aspect ratio for a media bubble, clamped so neither a panorama nor a very
 * tall screenshot takes over the thread.
 */
export function bubbleRatio(attachment: Attachment): number {
  const { width, height } = attachment;
  if (!width || !height) return 4 / 3;
  return Math.min(Math.max(width / height, 0.6), 1.9);
}

/**
 * The box a photo or video occupies in the thread.
 *
 * Both edges are capped, and the width is derived from the height cap for a
 * portrait image. Capping width alone is what made a tall poster fill most of
 * the screen: at 300px wide a 3:4 photo is 400px tall, and a whole message
 * disappears behind one picture. Deriving the width instead keeps every
 * attachment inside the same visual budget whichever way round it is.
 */
export function mediaBoxStyle(attachment: Attachment): {
  width: number;
  aspectRatio: number;
} {
  const ratio = bubbleRatio(attachment);
  const width = Math.min(MEDIA_MAX_WIDTH, Math.round(MEDIA_MAX_HEIGHT * ratio));
  return { width, aspectRatio: ratio };
}

/** How wide a sticker sits in the thread. WhatsApp's own is about this. */
const STICKER_WIDTH = 150;

/**
 * The box a sticker occupies.
 *
 * This exists because the sticker branch used to set a width and nothing else,
 * while the image inside it is sized `h-full`. A percentage height resolves
 * against the parent, the parent had no height, so the box came out 150 by 0
 * and every sticker in the thread was invisible. An aspect ratio is what gives
 * the parent a real height.
 *
 * Stickers are square far more often than not, so an unknown one is treated as
 * square rather than borrowing the 4:3 fallback photos use.
 */
export function stickerBoxStyle(attachment: Attachment): {
  width: number;
  aspectRatio: number;
} {
  const { width, height } = attachment;
  const ratio = width && height ? Math.min(Math.max(width / height, 0.5), 2) : 1;
  return { width: STICKER_WIDTH, aspectRatio: ratio };
}

/** A short label for the conversation list and reply previews. */
export function attachmentSummary(attachment: Attachment): string {
  if (attachment.kind === 'document') {
    return attachment.file_name ?? 'Dokumen';
  }
  return KIND_LABEL[attachment.kind];
}

/**
 * A URL for one attachment, cached until shortly before it expires.
 *
 * Signed URLs are minted per request and time out, so the same image scrolling
 * in and out of view must not cost a round trip each time — but a cached URL
 * that has quietly expired is worse than no cache at all. Entries are therefore
 * dropped a minute early.
 */
const linkCache = new Map<string, { url: string; expiresAt: number }>();
const EXPIRY_MARGIN_MS = 60_000;

export function cachedLink(attachmentId: string): string | null {
  const hit = linkCache.get(attachmentId);
  if (!hit) return null;
  if (hit.expiresAt - EXPIRY_MARGIN_MS <= Date.now()) {
    linkCache.delete(attachmentId);
    return null;
  }
  return hit.url;
}

export function cacheLink(attachmentId: string, url: string, expiresAt: string): void {
  const ts = new Date(expiresAt).getTime();
  linkCache.set(attachmentId, {
    url,
    expiresAt: Number.isNaN(ts) ? Date.now() + 5 * 60_000 : ts,
  });
}

/** Drops a cached URL, so the next read fetches a fresh one. */
export function forgetLink(attachmentId: string): void {
  linkCache.delete(attachmentId);
}
