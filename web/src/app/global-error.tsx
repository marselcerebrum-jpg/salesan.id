'use client';

import { useEffect } from 'react';

/**
 * The last boundary: a failure in the root layout itself.
 *
 * Next.js replaces the entire document when this renders, so it has to carry
 * its own <html> and <body> and cannot rely on globals.css having loaded or on
 * any provider being alive. That is why the styles here are inline and the
 * palette is repeated rather than read from tokens: everything this file would
 * normally depend on is exactly what has just failed.
 *
 * It is deliberately plain. The route-level boundary in (app)/error.tsx is the
 * one people should ever see; reaching this one means the shell itself broke.
 */
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error('Aplikasi gagal dimuat', error);
  }, [error]);

  return (
    <html lang="id">
      <body
        style={{
          margin: 0,
          minHeight: '100dvh',
          display: 'grid',
          placeItems: 'center',
          background: '#eef3ef',
          color: '#262d2e',
          fontFamily: 'ui-sans-serif, system-ui, sans-serif',
          padding: '24px',
        }}
      >
        <div style={{ maxWidth: 460 }}>
          <h1 style={{ fontSize: 28, fontWeight: 600, letterSpacing: '-0.02em', margin: 0 }}>
            salesan.id gagal dimuat
          </h1>
          <p style={{ fontSize: 15, lineHeight: 1.5, color: '#4d5654', marginTop: 8 }}>
            Terjadi kesalahan sebelum aplikasi sempat tampil. Muat ulang halaman; kalau masih
            gagal, kirimkan kode di bawah.
          </p>
          {error.digest ? (
            <p
              style={{
                fontSize: 13,
                fontFamily: 'ui-monospace, monospace',
                color: '#4d5654',
                background: '#e6ede8',
                padding: '8px 12px',
                borderRadius: 10,
                marginTop: 12,
              }}
            >
              Kode: {error.digest}
            </p>
          ) : null}
          <button
            type="button"
            onClick={reset}
            style={{
              marginTop: 20,
              height: 40,
              padding: '0 16px',
              borderRadius: 10,
              border: 'none',
              background: '#2e4e3c',
              color: '#ffffff',
              fontSize: 15,
              fontWeight: 500,
              cursor: 'pointer',
            }}
          >
            Muat ulang
          </button>
        </div>
      </body>
    </html>
  );
}
