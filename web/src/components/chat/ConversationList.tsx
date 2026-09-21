'use client';

import clsx from 'clsx';
import {
  AtSign,
  ChevronDown,
  Eraser,
  MailQuestion,
  Search,
  Tag,
  Trash2,
  Users,
} from 'lucide-react';
import { useState } from 'react';

import { Chip } from '@/components/ui/Primitives';
import { conversationTitle, formatChatTime, initials, previewText } from '@/lib/format';
import type { Conversation, ConversationCounts, Label } from '@/lib/types';

export interface InboxFilters {
  search: string;
  type: '' | 'personal' | 'group';
  unread: boolean;
  /** Only groups that named this account and have not been looked at. */
  mentions: boolean;
  labelId: string;
}

export const EMPTY_FILTERS: InboxFilters = {
  search: '',
  type: '',
  unread: false,
  mentions: false,
  labelId: '',
};

interface ConversationListProps {
  conversations: Conversation[];
  counts?: ConversationCounts;
  labels: Label[];
  filters: InboxFilters;
  onFiltersChange: (next: InboxFilters) => void;
  selectedId: string | null;
  onSelect: (conversation: Conversation) => void;
  onMarkUnread: (conversation: Conversation) => void;
  onDelete: (conversation: Conversation, clearOnly: boolean) => void;
  onToggleLabel: (conversation: Conversation, labelId: string, attached: boolean) => void;
  loading: boolean;
}

/**
 * A joined group of mutually exclusive options.
 *
 * Replaces a row of loose pills: three rows each opening with its own "Semua"
 * chip read as nine unrelated buttons, when they are really three questions
 * with one answer each. One bordered track per question makes the grouping
 * visible and drops the repeated reset buttons.
 */
function Segmented<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
}: {
  options: { value: T; label: string; count?: number }[];
  value: T;
  onChange: (value: T) => void;
  ariaLabel: string;
}) {
  return (
    <div
      role="group"
      aria-label={ariaLabel}
      className="inline-flex min-w-0 overflow-hidden rounded-lg border border-wa-border bg-wa-panel"
    >
      {options.map((option, index) => {
        const active = value === option.value;
        return (
          <button
            key={option.value}
            type="button"
            onClick={() => onChange(option.value)}
            aria-pressed={active}
            className={clsx(
              'px-2.5 py-1.5 text-xs font-medium whitespace-nowrap transition-colors',
              index > 0 && 'border-l border-hairline',
              active ? 'bg-brand-800 text-white' : 'text-ink-soft hover:bg-surface-sunken',
            )}
          >
            {option.label}
            {option.count !== undefined ? (
              <span className={clsx('ml-1', active ? 'text-white/70' : 'text-ink-muted')}>
                {option.count}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

/** Label filter as a menu, so a long label set cannot push the header wider. */
function LabelFilter({
  labels,
  value,
  onChange,
}: {
  labels: Label[];
  value: string;
  onChange: (labelId: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const active = labels.find((l) => l.id === value);

  if (labels.length === 0) return null;

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className={clsx(
          'inline-flex max-w-[190px] items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition-colors',
          active
            ? 'border-brand-700/30 bg-brand-600/10 text-brand-700'
            : 'border-wa-border bg-wa-panel text-wa-text-2 hover:bg-wa-hover',
        )}
      >
        <Tag className="size-3.5 shrink-0" style={active ? { color: active.color } : undefined} />
        <span className="truncate">{active ? active.name : 'Label'}</span>
        <ChevronDown className="size-3.5 shrink-0 opacity-60" />
      </button>

      {open ? (
        <>
          {/* Click-away layer: closing on mouseleave alone strands the menu
              open whenever the pointer jumps straight out of it. */}
          <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} aria-hidden />
          <div className="scrollbar-slim absolute left-0 z-20 mt-1 max-h-64 w-56 overflow-y-auto rounded-xl border border-wa-border bg-wa-panel p-1.5 shadow-e3">
            <button
              type="button"
              onClick={() => {
                onChange('');
                setOpen(false);
              }}
              className={clsx(
                'flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors hover:bg-surface-sunken',
                value === '' && 'font-semibold text-brand-700',
              )}
            >
              Semua label
            </button>
            {labels.map((label) => (
              <button
                key={label.id}
                type="button"
                onClick={() => {
                  onChange(label.id === value ? '' : label.id);
                  setOpen(false);
                }}
                className={clsx(
                  'flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors hover:bg-surface-sunken',
                  label.id === value && 'font-semibold text-brand-700',
                )}
              >
                <span
                  className="size-2.5 shrink-0 rounded-full"
                  style={{ backgroundColor: label.color }}
                  aria-hidden
                />
                <span className="truncate">{label.name}</span>
              </button>
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}

/**
 * Left column of the inbox (reference screen 6): search, a compact filter bar,
 * then the scrollable thread list.
 */
export function ConversationList({
  conversations,
  counts,
  labels,
  filters,
  onFiltersChange,
  selectedId,
  onSelect,
  onMarkUnread,
  onDelete,
  onToggleLabel,
  loading,
}: ConversationListProps) {
  const patch = (next: Partial<InboxFilters>) => onFiltersChange({ ...filters, ...next });

  // Summed from the loaded page rather than fetched separately. It is a hint on
  // a filter chip, not a figure anyone acts on, and one more round trip per
  // keystroke of the search box would not be worth it.
  const mentionTotal = conversations.reduce((sum, c) => sum + c.mention_count, 0);

  return (
    <div className="flex min-h-0 flex-col">
      <div className="space-y-3 px-3 pb-3">
        <label className="relative block">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" />
          <input
            value={filters.search}
            onChange={(event) => patch({ search: event.target.value })}
            placeholder="Cari atau mulai chat baru"
            aria-label="Cari chat"
            className="h-9 w-full rounded-lg bg-wa-panel-2 pr-3 pl-9 text-sm text-wa-text outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-wa-accent placeholder:text-wa-text-2"
          />
        </label>

        {/* Audience, unread toggle and labels share one line: three questions,
            each answered in place, instead of three stacked rows of pills. */}
        <div className="flex flex-wrap items-center gap-1.5">
          <Segmented
            ariaLabel="Jenis percakapan"
            value={filters.type}
            onChange={(type) => patch({ type })}
            options={[
              { value: '', label: 'Semua' },
              { value: 'personal', label: 'Pribadi' },
              { value: 'group', label: 'Grup' },
            ]}
          />

          <Chip active={filters.unread} onClick={() => patch({ unread: !filters.unread })}>
            <span className="size-2 rounded-full bg-brand-600" aria-hidden />
            Belum dibaca
            <span className="text-ink-muted">{counts?.unread ?? 0}</span>
          </Chip>

          {/* Separate from "belum dibaca" on purpose: a mention in a group that
              has otherwise been read through is exactly what this is for, and
              folding the two together would hide it. */}
          <Chip active={filters.mentions} onClick={() => patch({ mentions: !filters.mentions })}>
            <AtSign className="size-3" />
            Mention
            {mentionTotal > 0 ? <span className="text-ink-muted">{mentionTotal}</span> : null}
          </Chip>

          <LabelFilter
            labels={labels}
            value={filters.labelId}
            onChange={(labelId) => patch({ labelId })}
          />
        </div>

      </div>

      <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto border-t border-hairline">
        {loading && conversations.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-ink-muted">Memuat percakapan…</p>
        ) : conversations.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-ink-muted">
            Tidak ada percakapan pada filter ini.
          </p>
        ) : (
          <ul>
            {conversations.map((conv) => (
              <ConversationRow
                key={conv.id}
                conversation={conv}
                selected={conv.id === selectedId}
                labels={labels}
                onSelect={onSelect}
                onMarkUnread={onMarkUnread}
                onDelete={onDelete}
                onToggleLabel={onToggleLabel}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

/**
 * Per-row action menu, opened by the chevron under the timestamp.
 *
 * Rendered in a fixed layer rather than inside the row: the thread list is a
 * scroll container, so an absolutely positioned menu would be clipped by its
 * own parent the moment it grew past the row.
 */
function RowMenu({
  anchor,
  conversation,
  labels,
  onClose,
  onMarkUnread,
  onToggleLabel,
  onDelete,
}: {
  anchor: DOMRect;
  conversation: Conversation;
  labels: Label[];
  onClose: () => void;
  onMarkUnread: (conversation: Conversation) => void;
  onToggleLabel: (conversation: Conversation, labelId: string, attached: boolean) => void;
  onDelete: (conversation: Conversation, clearOnly: boolean) => void;
}) {
  const attached = new Set(conversation.labels.map((l) => l.id));

  // Flip above the anchor when there is not enough room below.
  const menuHeight = Math.min(340, 176 + labels.length * 38);
  const opensUp = anchor.bottom + menuHeight > window.innerHeight - 12;

  const style: React.CSSProperties = {
    position: 'fixed',
    left: Math.max(12, Math.min(anchor.right - 232, window.innerWidth - 244)),
    width: 232,
    ...(opensUp ? { bottom: window.innerHeight - anchor.top + 6 } : { top: anchor.bottom + 6 }),
  };

  return (
    <>
      <div className="fixed inset-0 z-40" onClick={onClose} aria-hidden />
      <div
        role="menu"
        style={style}
        className="z-50 overflow-hidden rounded-xl border border-wa-border bg-wa-panel shadow-e3"
      >
        <button
          type="button"
          role="menuitem"
          onClick={() => {
            onMarkUnread(conversation);
            onClose();
          }}
          className="flex w-full items-center gap-2.5 px-3 py-2.5 text-left text-sm transition-colors hover:bg-surface-sunken"
        >
          <MailQuestion className="size-4 text-ink-muted" />
          Tandai belum dibaca
        </button>

        <button
          type="button"
          role="menuitem"
          onClick={() => {
            onDelete(conversation, true);
            onClose();
          }}
          className="flex w-full items-center gap-2.5 px-3 py-2.5 text-left text-sm transition-colors hover:bg-surface-sunken"
        >
          <Eraser className="size-4 text-ink-muted" />
          Bersihkan isi chat
        </button>

        <button
          type="button"
          role="menuitem"
          onClick={() => {
            onDelete(conversation, false);
            onClose();
          }}
          className="flex w-full items-center gap-2.5 px-3 py-2.5 text-left text-sm text-danger transition-colors hover:bg-danger-soft"
        >
          <Trash2 className="size-4" />
          Hapus obrolan
        </button>

        <div className="border-t border-hairline px-3 py-1.5">
          <span className="text-2xs font-medium tracking-wide text-ink-muted uppercase">
            Label
          </span>
        </div>

        <div className="scrollbar-slim max-h-56 overflow-y-auto pb-1">
          {labels.length === 0 ? (
            <p className="px-3 py-3 text-center text-xs text-ink-muted">Belum ada label.</p>
          ) : (
            labels.map((label) => {
              const on = attached.has(label.id);
              return (
                <button
                  key={label.id}
                  type="button"
                  role="menuitemcheckbox"
                  aria-checked={on}
                  onClick={() => onToggleLabel(conversation, label.id, on)}
                  className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm transition-colors hover:bg-surface-sunken"
                >
                  <span
                    className="size-2.5 shrink-0 rounded-full"
                    style={{ backgroundColor: label.color }}
                    aria-hidden
                  />
                  <span className="flex-1 truncate">{label.name}</span>
                  <input type="checkbox" checked={on} readOnly tabIndex={-1} className="accent-brand-700" />
                </button>
              );
            })
          )}
        </div>
      </div>
    </>
  );
}

function ConversationRow({
  conversation,
  selected,
  labels,
  onSelect,
  onMarkUnread,
  onDelete,
  onToggleLabel,
}: {
  conversation: Conversation;
  selected: boolean;
  labels: Label[];
  onSelect: (conversation: Conversation) => void;
  onMarkUnread: (conversation: Conversation) => void;
  onDelete: (conversation: Conversation, clearOnly: boolean) => void;
  onToggleLabel: (conversation: Conversation, labelId: string, attached: boolean) => void;
}) {
  const [menuAt, setMenuAt] = useState<DOMRect | null>(null);
  const title = conversationTitle(conversation);
  const isGroup = conversation.type === 'group';

  return (
    <li className="group relative">
      <button
        type="button"
        onClick={() => onSelect(conversation)}
        aria-current={selected ? 'true' : undefined}
        className={clsx(
          'flex w-full items-start gap-3 px-3 py-[10px] text-left transition-colors',
          selected ? 'bg-wa-active' : 'hover:bg-wa-hover',
        )}
      >
        <span
          className={clsx(
            'grid size-[49px] shrink-0 place-items-center rounded-full text-base font-medium',
            isGroup ? 'bg-wa-active text-wa-text-2' : 'bg-wa-accent text-white',
          )}
        >
          {isGroup ? <Users className="size-6" /> : initials(title)}
        </span>

        <span className="min-w-0 flex-1 border-b border-wa-border pb-[10px]">
          <span className="flex items-baseline justify-between gap-2">
            <span className="truncate text-base text-wa-text">{title}</span>
            <span
              className={clsx(
                'shrink-0 text-xs',
                conversation.unread_count > 0 ? 'text-wa-badge' : 'text-wa-text-2',
              )}
            >
              {formatChatTime(conversation.last_message_at)}
            </span>
          </span>

          <span className="mt-[2px] flex items-center justify-between gap-2">
            <span className="flex min-w-0 items-center gap-1 truncate text-xs text-wa-text-2">
              {/* Shown ahead of the preview rather than instead of it: the
                  operator needs to know they were named *and* what about. */}
              {conversation.mention_count > 0 ? (
                <AtSign className="size-3.5 shrink-0 text-wa-accent" aria-label="Anda disebut" />
              ) : null}
              <span className="truncate">
                {previewText(conversation.last_message_text, conversation.last_message_direction)}
              </span>
            </span>
            {/* The badge yields to the chevron on hover — the two want the same
                spot, and only one of them is useful at a time. */}
            <span
              className={clsx(
                'shrink-0 transition-opacity',
                menuAt ? 'opacity-0' : 'group-hover:opacity-0',
              )}
            >
              {conversation.mention_count > 0 ? (
                // Its own badge in the accent colour, ahead of the unread
                // count: in a group with two hundred unread, the mention is
                // the part worth opening for.
                <span className="grid h-[20px] min-w-[20px] place-items-center rounded-full bg-wa-accent px-[6px] text-xs font-medium text-white">
                  @{conversation.mention_count > 99 ? '99+' : conversation.mention_count}
                </span>
              ) : conversation.unread_count > 0 ? (
                <span className="grid h-[20px] min-w-[20px] place-items-center rounded-full bg-wa-badge px-[6px] text-xs font-medium text-[#111b21]">
                  {conversation.unread_count > 999 ? '999+' : conversation.unread_count}
                </span>
              ) : conversation.marked_unread ? (
                // Flagged unread by hand with nothing actually unread: a dot,
                // since there is no number to show.
                <span
                  className="block size-[10px] rounded-full bg-wa-badge"
                  title="Ditandai belum dibaca"
                  aria-label="Ditandai belum dibaca"
                />
              ) : null}
            </span>
          </span>

          {conversation.labels.length > 0 ? (
            <span className="mt-1.5 flex flex-wrap gap-1">
              {conversation.labels.map((label) => (
                <span
                  key={label.id}
                  style={{ color: label.color, borderColor: `${label.color}40`, backgroundColor: `${label.color}14` }}
                  className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-2xs font-medium"
                >
                  <Tag className="size-2.5" />
                  {label.name}
                </span>
              ))}
            </span>
          ) : null}
        </span>
      </button>

      {/* Sits under the timestamp, overlaying the row rather than nesting in
          it — a button inside a button is invalid and breaks keyboard use. */}
      <button
        type="button"
        aria-label={`Aksi untuk ${title}`}
        aria-haspopup="menu"
        aria-expanded={Boolean(menuAt)}
        onClick={(event) => {
          event.stopPropagation();
          setMenuAt(menuAt ? null : event.currentTarget.getBoundingClientRect());
        }}
        className={clsx(
          'absolute top-[30px] right-2 grid size-6 place-items-center rounded-md text-ink-muted transition-opacity',
          'hover:bg-hairline/70 hover:text-ink focus-visible:opacity-100',
          menuAt ? 'bg-hairline/70 text-ink opacity-100' : 'opacity-0 group-hover:opacity-100',
        )}
      >
        <ChevronDown className="size-4" />
      </button>

      {menuAt ? (
        <RowMenu
          anchor={menuAt}
          conversation={conversation}
          labels={labels}
          onClose={() => setMenuAt(null)}
          onMarkUnread={onMarkUnread}
          onDelete={onDelete}
          onToggleLabel={onToggleLabel}
        />
      ) : null}
    </li>
  );
}
