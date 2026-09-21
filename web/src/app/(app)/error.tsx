'use client';

import { AlertTriangle, RotateCcw } from 'lucide-react';
import Link from 'next/link';
import { useEffect } from 'react';

/**
 * What a signed-in page shows when its render throws.
 *
 * There was no boundary here at all, which meant one bad render took the whole
 * route down to the framework's own error screen: no navigation, no way back,
 * and in production no explanation either. For a tool somebody keeps open all
 * day beside a live inbox, that is the difference between "one panel broke"
 * and "the app is gone".
 *
 * The sidebar stays, because this boundary sits inside the (app) layout. So
 * the rest of the product is still reachable while one page is broken, which
 * is the main thing this file buys.
 */
export default function AppError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // The digest is the only handle on the server-side stack in production,
    // so it goes to the console where support can ask for it.
    console.error('Halaman gagal dirender', error);
  }, [error]);

  return (
    <div className="px-5 py-6 lg:px-8 lg:py-8">
      <div className="mx-auto w-full max-w-[560px] rounded-card border border-hairline bg-surface-raised px-6 py-8 shadow-e1">
        <span className="grid size-11 place-items-center rounded-control bg-danger-soft text-danger">
          <AlertTriangle className="size-5" />
        </span>

        <h1 className="mt-4 text-2xl font-semibold tracking-[-0.02em] text-ink">
          Halaman ini gagal dimuat
        </h1>
        <p className="mt-2 text-sm text-ink-soft">
          Bagian lain aplikasi tetap bisa dipakai lewat menu di samping. Coba muat ulang halaman
          ini dulu, dan kalau masih gagal, kirimkan kode di bawah supaya kami bisa menelusurinya.
        </p>

        {error.digest ? (
          <p className="mt-3 rounded-lg bg-surface-sunken px-3 py-2 font-mono text-xs text-ink-soft">
            Kode: {error.digest}
          </p>
        ) : null}

        {/*
         * The message itself, in development only.
         *
         * A digest is minted for server errors and nothing else, so a render
         * that throws in the browser showed this card with no code, no message
         * and no way forward — the reader had to open the console to learn
         * anything at all. In production the message can carry internals and
         * stays hidden; while developing it is the whole point of the screen.
         */}
        {process.env.NODE_ENV !== 'production' && error.message ? (
          <pre className="mt-3 max-h-48 overflow-auto rounded-lg bg-surface-sunken px-3 py-2 font-mono text-xs whitespace-pre-wrap text-danger">
            {error.message}
          </pre>
        ) : null}

        <div className="mt-5 flex flex-wrap gap-2">
          <button
            type="button"
            onClick={reset}
            className="inline-flex h-10 items-center gap-2 rounded-control bg-brand-800 px-4 text-sm font-medium text-white transition-colors hover:bg-brand-900"
          >
            <RotateCcw className="size-4" />
            Muat ulang halaman
          </button>
          <Link
            href="/performa"
            className="inline-flex h-10 items-center rounded-control border border-hairline bg-surface-raised px-4 text-sm font-medium text-ink-soft transition-colors hover:bg-surface-sunken"
          >
            Ke Performa
          </Link>
        </div>
      </div>
    </div>
  );
}
