'use client';

import { ChevronRight } from 'lucide-react';
import Link from 'next/link';
import useSWR from 'swr';

import { AppMark } from '@/components/accounts/AccountCard';
import { EmptyState, Spinner } from '@/components/ui/Primitives';
import { fetcher } from '@/lib/api';
import type { Application } from '@/lib/types';

/** Reference screen 4 — pick an application before drilling into its numbers. */
export default function ChatApplicationsPage() {
  const { data, isLoading } = useSWR<{ applications: Application[] }>('/applications', fetcher, {
    refreshInterval: 30_000,
  });
  const applications = data?.applications ?? [];

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <header>
        <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Chat</h1>
        <p className="mt-1 text-sm text-ink-muted">
          Pilih aplikasi untuk melihat percakapannya.
        </p>
      </header>

      {isLoading ? (
        <Spinner label="Memuat aplikasi…" />
      ) : applications.length === 0 ? (
        <EmptyState
          title="Belum ada aplikasi"
          description="Buat aplikasi lewat tombol Kelola Aplikasi di halaman Akun WhatsApp."
        />
      ) : (
        <div className="mt-6 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {applications.map((app) => (
            <Link
              key={app.id}
              href={`/chat/${app.id}`}
              className="group flex items-center gap-3.5 rounded-card border border-hairline shadow-e1 transition-shadow hover:shadow-e2 bg-surface-raised px-4 py-4 transition-colors hover:border-brand-700/30 hover:bg-brand-600/[0.03]"
            >
              <AppMark code={app.code} color={app.color} size={44} />

              <div className="min-w-0 flex-1">
                <p className="truncate text-lg font-semibold text-ink">{app.name}</p>
                <p className="text-xs text-ink-muted">{app.account_count} nomor</p>
              </div>

              {/* Chats waiting for an answer, not chats that exist. Read from
                  the same column as the account rows and the sidebar. */}
              {app.unanswered_count > 0 ? (
                <span
                  title={`${app.unanswered_count} chat belum dibalas`}
                  className="rounded-full bg-warn-soft px-2.5 py-1 text-2xs font-semibold text-warn"
                >
                  {app.unanswered_count > 99 ? '99+' : app.unanswered_count}
                </span>
              ) : null}

              <ChevronRight className="size-4 shrink-0 text-ink-muted transition-transform group-hover:translate-x-0.5" />
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
