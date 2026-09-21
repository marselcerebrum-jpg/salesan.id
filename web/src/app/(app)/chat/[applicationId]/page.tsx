'use client';

import { ArrowLeft, Info, LogIn, Users } from 'lucide-react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { useState } from 'react';
import useSWR from 'swr';

import { AccountDetailModal } from '@/components/accounts/AccountDetailModal';
import { Button } from '@/components/ui/Button';
import { EmptyState, Spinner } from '@/components/ui/Primitives';
import { fetcher } from '@/lib/api';
import { accountStatusTone } from '@/lib/format';
import type { Account, Application } from '@/lib/types';

/** Reference screen 5 — the numbers linked to one application. */
export default function ChatAccountsPage() {
  const params = useParams<{ applicationId: string }>();
  const router = useRouter();
  const applicationId = params.applicationId;

  const [detail, setDetail] = useState<Account | null>(null);

  const { data: appsData } = useSWR<{ applications: Application[] }>('/applications', fetcher);
  const application = appsData?.applications.find((a) => a.id === applicationId);

  const { data, isLoading, mutate } = useSWR<{ accounts: Account[] }>(
    `/applications/${applicationId}/accounts`,
    fetcher,
    { refreshInterval: 30_000 },
  );
  const accounts = data?.accounts ?? [];

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <Link
        href="/chat"
        className="inline-flex items-center gap-1.5 text-sm text-ink-muted transition-colors hover:text-ink"
      >
        <ArrowLeft className="size-4" />
        Semua aplikasi
      </Link>

      <header className="mt-3">
        <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">
          {application?.name ?? 'Aplikasi'}
        </h1>
        <p className="mt-1 text-sm text-ink-muted">
          Pilih nomor: masuk ke inbox atau lihat ringkasan performanya.
        </p>
      </header>

      {isLoading ? (
        <Spinner label="Memuat nomor…" />
      ) : accounts.length === 0 ? (
        <EmptyState
          title="Belum ada nomor di aplikasi ini"
          description="Tambahkan akun WhatsApp dan pilih aplikasi ini saat membuatnya."
          action={<Button onClick={() => router.push('/accounts')}>Ke Akun WhatsApp</Button>}
        />
      ) : (
        <div className="mt-6 grid gap-4 xl:grid-cols-2">
          {accounts.map((account) => {
            const tone = accountStatusTone(account.status);
            return (
              <div
                key={account.id}
                className="flex items-center gap-3.5 rounded-card border border-hairline shadow-e1 transition-shadow hover:shadow-e2 bg-surface-raised px-4 py-3.5"
              >
                <span className="grid size-11 shrink-0 place-items-center rounded-xl bg-surface-sunken text-ink-soft">
                  <Users className="size-5" />
                </span>

                <div className="min-w-0 flex-1">
                  <p className="truncate text-lg font-semibold text-ink">
                    {account.label ?? account.name}
                  </p>
                  <p className="truncate text-xs text-ink-muted">
                    {account.phone_number ?? 'Belum tertaut'}
                    {account.label ? ` · ${account.label}` : ''}
                  </p>
                </div>

                <span
                  className={`size-2 shrink-0 rounded-full ${tone.dot}`}
                  title={account.status}
                  aria-hidden
                />

                {/*
                 * Chats waiting for an answer.
                 *
                 * This was conversation_count — every chat the number has ever
                 * had. In a warning-coloured pill it read as a pile of work,
                 * but it did not go down when the work was done, and it did not
                 * agree with the badge in the sidebar, which was counting
                 * unopened messages instead. Two numbers, neither of them the
                 * one anybody wanted.
                 */}
                {account.unanswered_count > 0 ? (
                  <span
                    title={`${account.unanswered_count} chat belum dibalas`}
                    className="rounded-full bg-warn-soft px-2.5 py-1 text-2xs font-semibold text-warn"
                  >
                    {account.unanswered_count > 99 ? '99+' : account.unanswered_count}
                  </span>
                ) : null}

                <Button
                  variant="primary"
                  size="sm"
                  icon={<LogIn className="size-4" />}
                  onClick={() => router.push(`/chat/${applicationId}/${account.id}`)}
                >
                  Masuk
                </Button>
                <Button size="sm" icon={<Info className="size-4" />} onClick={() => setDetail(account)}>
                  Detail
                </Button>
              </div>
            );
          })}
        </div>
      )}

      <AccountDetailModal
        account={detail}
        applications={appsData?.applications ?? []}
        onClose={() => setDetail(null)}
        onChanged={() => void mutate()}
      />
    </div>
  );
}
