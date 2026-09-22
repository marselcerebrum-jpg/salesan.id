'use client';

import clsx from 'clsx';
import {
  BarChart3,
  Bot,
  Braces,
  CalendarClock,
  CircleDashed,
  LayoutDashboard,
  Contact as ContactIcon,
  Instagram,
  LogOut,
  MessageCircle,
  Moon,
  Music2,
  Radio,
  Smartphone,
  SlidersHorizontal,
  Sun,
  Timer,
  RefreshCcwDot,
  Users,
  X,
  Zap,
} from 'lucide-react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import useSWR, { mutate } from 'swr';
import type { ComponentType } from 'react';

import { SalesanMark } from '@/components/ui/BrandLogo';

import { fetcher } from '@/lib/api';
import { getSupabaseBrowserClient } from '@/lib/supabase/client';
import { useTheme } from '@/lib/theme';
import { initials } from '@/lib/format';
import type { Account, Me } from '@/lib/types';

/** What the sidebar calls each operational role. */
const ROLE_LABEL: Record<string, string> = {
  leader: 'Leader',
  pic: 'PIC',
  freelance: 'Freelance',
};

interface NavItem {
  label: string;
  href?: string;
  icon: ComponentType<{ className?: string }>;
  badge?: 'unread';
  comingSoon?: boolean;
}

interface NavSection {
  title: string;
  items: NavItem[];
}

/**
 * Navigation mirrors the reference sidebar exactly. Channels outside the MVP
 * (Instagram, TikTok, Broadcast, Chatbot, ...) are rendered as disabled
 * "COMING SOON" rows so the layout matches without implying they work.
 */
const SECTIONS: NavSection[] = [
  {
    title: 'Utama',
    items: [
      { label: 'Dashboard', href: '/dashboard', icon: LayoutDashboard },
      { label: 'Performa', href: '/performa', icon: BarChart3 },
      { label: 'Akun WhatsApp', href: '/accounts', icon: Smartphone },
      { label: 'Akun Instagram', icon: Instagram, comingSoon: true },
      { label: 'Akun TikTok', icon: Music2, comingSoon: true },
    ],
  },
  {
    title: 'Fitur',
    items: [
      { label: 'Chat WhatsApp', href: '/chat', icon: MessageCircle, badge: 'unread' },
      { label: 'Chat Instagram', icon: Instagram, comingSoon: true },
      { label: 'Chat TikTok', icon: Music2, comingSoon: true },
      { label: 'Broadcast', href: '/broadcast', icon: Radio },
      { label: 'WA Story', href: '/story', icon: CircleDashed },
      { label: 'Kontak', href: '/contacts', icon: ContactIcon },
      { label: 'Fetch Grup', href: '/groups', icon: Users },
    ],
  },
  {
    title: 'Automasi',
    items: [
      { label: 'Chatbot', icon: Bot, comingSoon: true },
      { label: 'Auto Follow Up', icon: RefreshCcwDot, comingSoon: true },
    ],
  },
  {
    title: 'Pengaturan',
    items: [
      // Accounts, roles and applications together: they are one decision made
      // in three parts, and splitting them across screens is how somebody ends
      // up with a role and no reach, or reach and no role.
      { label: 'Akun & Peran', href: '/pengaturan', icon: SlidersHorizontal },
      // Both sit under Akun & Peran because both are things a Leader or PIC
      // sets up once and the team then uses. They were inside that screen
      // until it became clear neither is about access.
      { label: 'Variabel Pesan', href: '/variabel', icon: Braces },
      { label: 'Balas Cepat', href: '/balas-cepat', icon: Zap },
      { label: 'Target SLA', href: '/sla', icon: Timer },
      // Beside Target SLA because the two are one setting in two halves: the
      // target says how fast, these hours say when the clock is allowed to run.
      { label: 'Jam Kerja', href: '/jam-kerja', icon: CalendarClock },
    ],
  },
];

export function Sidebar({
  profile,
  open,
  onClose,
}: {
  profile: Me | null;
  open: boolean;
  onClose: () => void;
}) {
  const pathname = usePathname();
  const { resolved, toggle } = useTheme();

  const { data } = useSWR<{ accounts: Account[] }>('/accounts', fetcher, {
    refreshInterval: 60_000,
  });
  /*
   * Chats waiting for an answer, not messages waiting to be opened.
   *
   * This counted unread_count, which is WhatsApp's "not yet opened" figure. A
   * chat somebody had read and not replied to disappeared from the badge while
   * still being the thing that needed doing — and the number disagreed with
   * the one on the account row, which was counting something else again.
   */
  const unanswered = (data?.accounts ?? []).reduce((sum, a) => sum + a.unanswered_count, 0);
  const unreadLabel = unanswered > 99 ? '99+' : String(unanswered);

  async function signOut() {
    await getSupabaseBrowserClient().auth.signOut();
    // Everything this tab fetched belonged to the person who just left:
    // profile, inbox, Performa figures, PIC lists. SWR would otherwise hand the
    // same cache to whoever signs in next, until each page happened to
    // refetch. Clear it, then leave with a full reload so the realtime socket
    // and every component's state go with it.
    await mutate(() => true, undefined, { revalidate: false });
    window.location.assign('/login');
  }

  const role = profile?.scope?.role ?? '';
  const roleLabel =
    ROLE_LABEL[role] ??
    (profile?.scope?.all ? 'Leader' : profile ? 'Belum diberi peran' : '');

  return (
    <>
      {/* Mobile scrim */}
      <div
        aria-hidden={!open}
        onClick={onClose}
        className={clsx(
          'fixed inset-0 z-30 bg-ink/40 transition-opacity lg:hidden',
          open ? 'opacity-100' : 'pointer-events-none opacity-0',
        )}
      />

      <aside
        className={clsx(
          'fixed inset-y-0 left-0 z-40 flex w-64 flex-col bg-brand-900 text-white transition-transform',
          'lg:translate-x-0',
          open ? 'translate-x-0' : '-translate-x-full',
        )}
      >
        <div className="flex items-center justify-between gap-2 px-5 py-5">
          <Link href="/accounts" className="flex items-center gap-2.5">
            <SalesanMark size={36} className="rounded-lg" />
            <span className="text-lg font-semibold tracking-tight">salesan.id</span>
          </Link>
          <button
            type="button"
            onClick={onClose}
            aria-label="Tutup menu"
            className="rounded-lg p-1.5 text-white/70 hover:bg-white/10 lg:hidden"
          >
            <X className="size-5" />
          </button>
        </div>

        <nav className="scrollbar-slim-dark flex-1 overflow-y-auto px-3 pb-4">
          {SECTIONS.map((section) => (
            <div key={section.title} className="mb-5">
              <p className="px-2 pb-2 text-2xs font-semibold text-white/55">
                {section.title}
              </p>
              <ul className="space-y-0.5">
                {section.items.map((item) => {
                  const active =
                    !!item.href &&
                    (pathname === item.href || pathname.startsWith(`${item.href}/`));
                  const Icon = item.icon;

                  const inner = (
                    <>
                      <Icon className="size-[18px] shrink-0" />
                      <span className="flex-1 truncate text-left">{item.label}</span>
                      {item.comingSoon ? (
                        <span className="rounded bg-white/10 px-1.5 py-0.5 text-2xs font-semibold tracking-wide text-white/60">
                          COMING SOON
                        </span>
                      ) : null}
                      {item.badge === 'unread' && unanswered > 0 ? (
                        <span className="rounded-full bg-amber-badge px-2 py-0.5 text-2xs font-bold text-brand-950">
                          {unreadLabel}
                        </span>
                      ) : null}
                    </>
                  );

                  if (!item.href) {
                    return (
                      <li key={item.label}>
                        <span
                          aria-disabled="true"
                          title="Belum tersedia pada tahap ini"
                          className="flex cursor-not-allowed items-center gap-3 rounded-lg px-2.5 py-2.5 text-sm text-white/40"
                        >
                          {inner}
                        </span>
                      </li>
                    );
                  }

                  return (
                    <li key={item.label}>
                      <Link
                        href={item.href}
                        onClick={onClose}
                        aria-current={active ? 'page' : undefined}
                        className={clsx(
                          'flex items-center gap-3 rounded-lg px-2.5 py-2.5 text-sm transition-colors',
                          active
                            ? 'bg-white/12 font-medium text-white'
                            : 'text-white/75 hover:bg-white/8 hover:text-white',
                        )}
                      >
                        {inner}
                      </Link>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </nav>

        <div className="border-t border-white/10 px-3 py-3">
          <p className="px-2 pb-2 text-2xs font-semibold text-white/55">
            Pengaturan
          </p>
          <div className="flex items-center gap-2.5 rounded-xl bg-white/8 px-2.5 py-2.5">
            <span className="grid size-9 shrink-0 place-items-center rounded-full bg-amber-badge text-sm font-bold text-brand-950">
              {initials(profile?.user.full_name ?? profile?.user.email, 'A')}
            </span>
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">
                {profile?.user.full_name ?? profile?.user.email ?? 'Memuat…'}
              </p>
              {/* The role this person works as, and the address they signed in
                  with: together they say whose account this is, which is the
                  question this card exists to answer. */}
              <p
                className="truncate text-2xs text-white/55"
                title={profile ? `${roleLabel} · ${profile.user.email}` : undefined}
              >
                {profile ? `${roleLabel} · ${profile.user.email}` : ''}
              </p>
            </div>
            <button
              type="button"
              onClick={toggle}
              aria-label={resolved === 'dark' ? 'Ganti ke mode terang' : 'Ganti ke mode gelap'}
              title={resolved === 'dark' ? 'Mode terang' : 'Mode gelap'}
              className="rounded-lg p-1.5 text-white/70 transition-colors hover:bg-white/10 hover:text-white"
            >
              {resolved === 'dark' ? (
                <Sun className="size-[18px]" />
              ) : (
                <Moon className="size-[18px]" />
              )}
            </button>
            <button
              type="button"
              onClick={signOut}
              aria-label="Keluar"
              title="Keluar"
              className="rounded-lg p-1.5 text-white/70 transition-colors hover:bg-white/10 hover:text-white"
            >
              <LogOut className="size-[18px]" />
            </button>
          </div>
        </div>
      </aside>
    </>
  );
}
