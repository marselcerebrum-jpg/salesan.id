'use client';

import { Menu, Wifi, WifiOff } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import useSWR from 'swr';

import { Sidebar } from '@/components/layout/Sidebar';
import { fetcher } from '@/lib/api';
import { RealtimeProvider, useRealtimeStatus } from '@/lib/realtime';
import type { User, Workspace } from '@/lib/types';

/**
 * Two-column application frame: fixed dark sidebar on the left, scrollable
 * content on the right. Below `lg` the sidebar becomes a drawer.
 */
export function AppShell({ children }: { children: ReactNode }) {
  return (
    <RealtimeProvider>
      <Shell>{children}</Shell>
    </RealtimeProvider>
  );
}

function Shell({ children }: { children: ReactNode }) {
  const [menuOpen, setMenuOpen] = useState(false);
  const { data: profile } = useSWR<{ user: User; workspace: Workspace }>('/me', fetcher);

  return (
    <div className="min-h-dvh bg-surface">
      <Sidebar profile={profile ?? null} open={menuOpen} onClose={() => setMenuOpen(false)} />

      <div className="lg:pl-64">
        {/* Opaque, not frosted. DESIGN.md rules out glassmorphism because
            transparency makes text hard to read over dense data, and this bar
            sits over exactly that: tables and message lists scrolling under
            it. An e1 shadow is what separates it from the content instead. */}
        <header className="sticky top-0 z-20 flex items-center gap-3 border-b border-hairline bg-surface px-4 py-3 shadow-e1 lg:hidden">
          <button
            type="button"
            onClick={() => setMenuOpen(true)}
            aria-label="Buka menu"
            className="rounded-lg p-2 text-ink-soft hover:bg-surface-sunken"
          >
            <Menu className="size-5" />
          </button>
          <span className="text-sm font-semibold">salesan.id</span>
          <div className="ml-auto">
            <ConnectionIndicator />
          </div>
        </header>

        <main className="min-h-dvh">{children}</main>
      </div>
    </div>
  );
}

/** Small live/offline marker for the realtime socket. */
export function ConnectionIndicator({ className }: { className?: string }) {
  const connected = useRealtimeStatus();
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-2xs font-medium ${
        connected ? 'text-brand-700' : 'text-ink-muted'
      } ${className ?? ''}`}
      title={connected ? 'Terhubung ke server realtime' : 'Realtime terputus, mencoba ulang'}
    >
      {connected ? <Wifi className="size-3.5" /> : <WifiOff className="size-3.5" />}
      {connected ? 'live' : 'offline'}
    </span>
  );
}
