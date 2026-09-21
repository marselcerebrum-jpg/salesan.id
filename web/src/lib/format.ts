import type { AccountStatus, ConversationStatus, MessageStatus } from '@/lib/types';

/** "09.23" for today, "Kemarin", otherwise "dd/mm/yy" — as in the reference. */
export function formatChatTime(iso: string | null): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';

  const now = new Date();
  const sameDay =
    date.getDate() === now.getDate() &&
    date.getMonth() === now.getMonth() &&
    date.getFullYear() === now.getFullYear();

  if (sameDay) {
    return date.toLocaleTimeString('id-ID', { hour: '2-digit', minute: '2-digit', hour12: false });
  }

  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  const isYesterday =
    date.getDate() === yesterday.getDate() &&
    date.getMonth() === yesterday.getMonth() &&
    date.getFullYear() === yesterday.getFullYear();
  if (isYesterday) return 'Kemarin';

  return date.toLocaleDateString('id-ID', { day: '2-digit', month: '2-digit', year: '2-digit' });
}

/** Clock time inside a message bubble. */
export function formatMessageTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString('id-ID', { hour: '2-digit', minute: '2-digit', hour12: false });
}

/** Day separator inside the message thread. */
export function formatDayLabel(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';

  const now = new Date();
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const diffDays = Math.round((startOfDay(now) - startOfDay(date)) / 86_400_000);

  if (diffDays === 0) return 'Hari ini';
  if (diffDays === 1) return 'Kemarin';
  return date.toLocaleDateString('id-ID', { weekday: 'long', day: 'numeric', month: 'long' });
}

/** "baru saja", "5 menit lalu", "2 jam lalu", "3 hari lalu". */
export function formatRelative(iso: string | null): string {
  if (!iso) return 'belum pernah';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return 'belum pernah';

  // A clock skew between server and browser can put this slightly in the
  // future; treat that as "just now" rather than printing a negative age.
  const seconds = Math.floor((Date.now() - date.getTime()) / 1000);
  if (seconds < 45) return 'baru saja';
  if (seconds < 3600) return `${Math.floor(seconds / 60)} menit lalu`;
  if (seconds < 86_400) return `${Math.floor(seconds / 3600)} jam lalu`;
  if (seconds < 604_800) return `${Math.floor(seconds / 86_400)} hari lalu`;
  return date.toLocaleDateString('id-ID', { day: 'numeric', month: 'short' });
}

/** Up to two uppercase initials for the avatar circles. */
export function initials(name: string | null | undefined, fallback = '?'): string {
  const trimmed = (name ?? '').trim();
  if (!trimmed) return fallback;
  const parts = trimmed.split(/\s+/).filter(Boolean);
  if (parts.length === 1) return parts[0].slice(0, 1).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

/** Strips the WhatsApp server suffix so a JID reads as a phone number. */
export function jidToDisplay(jid: string): string {
  const user = jid.split('@')[0] ?? jid;
  return user.split(':')[0];
}

/** Best available human label for a conversation. */
export function conversationTitle(conv: {
  name: string | null;
  phone_number: string | null;
  chat_jid: string;
}): string {
  return conv.name?.trim() || conv.phone_number || jidToDisplay(conv.chat_jid);
}

export const ACCOUNT_STATUS_LABEL: Record<AccountStatus, string> = {
  disconnected: 'Terputus',
  connecting: 'Menyambungkan',
  qr_pending: 'Menunggu QR',
  connected: 'Terhubung',
  logged_out: 'Keluar',
  error: 'Gagal',
};

export const CONVERSATION_STATUS_LABEL: Record<ConversationStatus, string> = {
  new: 'Baru',
  in_progress: 'Diproses',
  done: 'Selesai',
};

export const MESSAGE_STATUS_LABEL: Record<MessageStatus, string> = {
  pending: 'Mengirim',
  sent: 'Terkirim',
  delivered: 'Diterima',
  read: 'Dibaca',
  failed: 'Gagal',
};

/** Colour treatment for the status pill on an account card. */
export function accountStatusTone(status: AccountStatus): {
  dot: string;
  text: string;
  bg: string;
} {
  switch (status) {
    case 'connected':
      return { dot: 'bg-online', text: 'text-brand-700', bg: 'bg-brand-600/10' };
    case 'connecting':
    case 'qr_pending':
      return { dot: 'bg-warn', text: 'text-warn', bg: 'bg-warn-soft' };
    case 'error':
      return { dot: 'bg-danger', text: 'text-danger', bg: 'bg-danger-soft' };
    default:
      return { dot: 'bg-ink-muted', text: 'text-ink-muted', bg: 'bg-surface-sunken' };
  }
}

/**
 * Preview line for a conversation row.
 *
 * The "Kamu:" prefix is driven by the direction of the *newest* message, which
 * the server recomputes from the message table — so an incoming reply replaces
 * an outgoing preview, prefix and all, rather than leaving a stale "Kamu:".
 */
export function previewText(text: string | null, direction: 'in' | 'out' | null): string {
  if (!text) return 'Belum ada pesan';
  return direction === 'out' ? `Kamu: ${text}` : text;
}
