'use client';

import { ChevronRight, Download, X } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import useSWR from 'swr';

import { AppMark } from '@/components/accounts/AccountCard';
import { ExportChatDialog, type ExportResult } from '@/components/chat/ExportChatDialog';
import { Button } from '@/components/ui/Button';
import { EmptyState, Spinner } from '@/components/ui/Primitives';
import { fetcher } from '@/lib/api';
import type { Application } from '@/lib/types';

/** Reference screen 4 — pick an application before drilling into its numbers. */
export default function ChatApplicationsPage() {
  const { data, isLoading } = useSWR<{ applications: Application[] }>('/applications', fetcher, {
    refreshInterval: 30_000,
  });
  const applications = data?.applications ?? [];
  const [exportOpen, setExportOpen] = useState(false);
  const [exported, setExported] = useState<ExportResult | null>(null);

  // The confirmation stays long enough to read, then gets out of the way.
  useEffect(() => {
    if (!exported) return;
    const timer = setTimeout(() => setExported(null), 8000);
    return () => clearTimeout(timer);
  }, [exported]);

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Chat</h1>
          <p className="mt-1 text-sm text-ink-muted">
            Pilih aplikasi untuk melihat percakapannya.
          </p>
        </div>
        <Button
          icon={<Download className="size-4" />}
          onClick={() => setExportOpen(true)}
          disabled={applications.length === 0}
        >
          Export Chat
        </Button>
      </header>

      {exported ? (
        <div
          role="status"
          className="mt-4 flex items-start justify-between gap-3 rounded-control border border-brand-600/25 bg-brand-600/8 px-3 py-2 text-sm text-brand-700"
        >
          <p className="min-w-0 break-words">
            File <span className="font-medium">{exported.filename}</span> berhasil dibuat dan
            diunduh
            {exported.rows !== null ? ` — ${exported.rows.toLocaleString('id-ID')} pesan` : ''}.
          </p>
          <button
            type="button"
            onClick={() => setExported(null)}
            aria-label="Tutup notifikasi"
            className="-mr-1 shrink-0 rounded p-0.5 hover:bg-brand-600/10"
          >
            <X className="size-4" />
          </button>
        </div>
      ) : null}

      <ExportChatDialog
        open={exportOpen}
        onClose={() => setExportOpen(false)}
        applications={applications}
        onExported={setExported}
      />

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
