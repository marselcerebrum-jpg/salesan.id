'use client';

import { ArrowRight, Eye, EyeOff, Loader2, Lock, Mail } from 'lucide-react';
import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useState, type FormEvent } from 'react';

import {
  BRAND_MINT,
  SalesanLockup,
  SalesanMark,
  SalesanWordmark,
} from '@/components/ui/BrandLogo';
import { getSupabaseBrowserClient } from '@/lib/supabase/client';

export default function LoginPage() {
  return (
    <Suspense
      fallback={
        <div className="grid min-h-dvh place-items-center">
          <Loader2 className="size-5 animate-spin text-ink-muted" />
        </div>
      }
    >
      <LoginScreen />
    </Suspense>
  );
}

/**
 * The sign-in screen: one card in the middle of the page, the brand on its
 * left half and the form on its right.
 *
 * Sign-in only. Accounts are made by a Leader from Anggota & Peran, so there is
 * no "Daftar" here — a stranger cannot open a workspace of their own from this
 * page. "Lupa password?" says who to ask for the same reason: passwords are
 * reset by the Leader, and a reset email would go to an address nobody reads.
 */
function LoginScreen() {
  return (
    <div className="grid min-h-dvh place-items-center bg-surface px-4 py-8">
      <div className="grid w-full max-w-[920px] grid-cols-1 overflow-hidden rounded-[20px] border border-hairline bg-surface-raised shadow-e3 lg:grid-cols-[1.05fr_1fr]">
        <BrandPanel />
        <main className="flex min-w-0 items-center justify-center px-6 py-10 sm:px-12 lg:py-14">
          <LoginForm />
        </main>
      </div>
    </div>
  );
}

/* --- brand ---------------------------------------------------------------- */

function BrandPanel() {
  return (
    // The artwork's own mint, in both themes, so the panel reads as part of the
    // logo rather than a card the logo was placed on.
    <aside
      style={{ background: `linear-gradient(160deg, #eaf7ef 0%, ${BRAND_MINT} 55%, #f7fdf9 100%)` }}
      className="relative flex flex-col justify-between overflow-hidden px-8 py-7 lg:px-10 lg:py-9"
    >
      {/* Two large, faint copies of the mark, cropped by the panel edges. The
          artwork is on transparency, so they can pass behind the logo itself. */}
      <SalesanMark className="pointer-events-none absolute -top-14 -left-20 w-[260px] opacity-[0.07] lg:w-[320px]" />
      <SalesanMark className="pointer-events-none absolute -right-24 -bottom-24 w-[240px] opacity-[0.06] lg:w-[300px]" />

      <div className="relative flex items-center gap-2.5 lg:hidden">
        <SalesanMark className="w-10" />
        <SalesanWordmark className="text-xl text-[#1a2333]" />
      </div>

      {/* The lock-up as supplied: mark, wordmark and tagline in one picture. */}
      <div className="relative hidden flex-1 items-center justify-center py-2 lg:flex">
        <SalesanLockup className="w-full max-w-[350px]" />
      </div>

      <div className="relative hidden lg:block">
        <span className="mb-3 block h-0.5 w-8 bg-[#22b16c]" aria-hidden />
        <p className="text-sm leading-relaxed text-[#4b5563]">
          Solusi sederhana
          <br />
          untuk tim sales yang lebih produktif.
        </p>
      </div>
    </aside>
  );
}

/* --- form ----------------------------------------------------------------- */

function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const next = params.get('next') ?? '/accounts';

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [forgot, setForgot] = useState(false);
  const [busy, setBusy] = useState(false);

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const supabase = getSupabaseBrowserClient();
      const { error } = await supabase.auth.signInWithPassword({ email, password });
      if (error) throw error;
      router.replace(next);
      router.refresh();
    } catch (err) {
      setError(readable(err));
      setBusy(false);
    }
  }

  return (
    <div className="w-full max-w-[360px]">
      <h1 className="text-[26px] font-bold tracking-[-0.02em] text-ink">Selamat Datang</h1>
      <p className="mt-1.5 text-sm text-ink-muted">Masuk untuk melanjutkan ke Salesan.id</p>

      <form onSubmit={onSubmit} className="mt-7 space-y-5">
        <Field
          label="E-Mail"
          icon={Mail}
          type="email"
          value={email}
          onChange={setEmail}
          placeholder="nama@perusahaan.id"
          autoComplete="email"
          required
        />
        <Field
          label="Password"
          icon={Lock}
          type={showPassword ? 'text' : 'password'}
          value={password}
          onChange={setPassword}
          placeholder="Kata sandi"
          autoComplete="current-password"
          required
          trailing={
            <button
              type="button"
              onClick={() => setShowPassword((v) => !v)}
              aria-label={showPassword ? 'Sembunyikan kata sandi' : 'Tampilkan kata sandi'}
              aria-pressed={showPassword}
              className="grid size-8 place-items-center rounded-md text-ink-muted transition-colors hover:text-ink"
            >
              {showPassword ? <Eye className="size-4" /> : <EyeOff className="size-4" />}
            </button>
          }
        />

        <div className="flex items-center justify-end">
          <button
            type="button"
            onClick={() => setForgot((v) => !v)}
            aria-expanded={forgot}
            className="text-sm font-medium text-brand-700 underline-offset-2 hover:underline"
          >
            Lupa password?
          </button>
        </div>

        {forgot ? (
          <p className="rounded-control border border-brand-600/25 bg-brand-600/10 px-3.5 py-2.5 text-sm leading-relaxed text-ink-soft">
            Kata sandi diatur ulang oleh Leader workspace Anda lewat menu Pengaturan → Anggota
            & Peran → Ganti kata sandi. Hubungi Leader Anda, lalu masuk dengan kata sandi yang
            baru.
          </p>
        ) : null}

        {error ? (
          <p
            role="alert"
            className="rounded-control border border-danger/25 bg-danger-soft px-3.5 py-2.5 text-sm text-danger"
          >
            {error}
          </p>
        ) : null}

        <button
          type="submit"
          disabled={busy}
          className="flex h-12 w-full items-center justify-center gap-2 rounded-control bg-brand-800 text-[15px] font-semibold text-white transition-colors hover:bg-brand-900 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand-800 disabled:opacity-60"
        >
          {busy ? <Loader2 className="size-4 animate-spin" /> : null}
          Masuk
          {!busy ? <ArrowRight className="size-4" aria-hidden /> : null}
        </button>
      </form>
    </div>
  );
}

function Field({
  label,
  icon: Icon,
  value,
  onChange,
  trailing,
  ...rest
}: {
  label: string;
  icon: typeof Mail;
  value: string;
  onChange: (value: string) => void;
  trailing?: React.ReactNode;
} & Omit<React.InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'>) {
  return (
    <label className="block">
      <span className="mb-2 block text-sm font-medium text-ink">{label}</span>
      <span className="flex h-12 items-center rounded-control border border-hairline bg-surface pr-1.5 pl-3.5 transition-colors focus-within:border-brand-700 focus-within:ring-2 focus-within:ring-brand-700/15">
        <Icon className="size-4 shrink-0 text-ink-muted" aria-hidden />
        <input
          {...rest}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="h-full min-w-0 flex-1 bg-transparent px-3 text-sm text-ink outline-none placeholder:text-ink-muted/70"
        />
        {trailing}
      </span>
    </label>
  );
}

/** Supabase's messages are English and terse; the ones people actually hit
 *  are said in the language of the rest of the screen. */
function readable(err: unknown): string {
  const text = err instanceof Error ? err.message : '';
  if (/invalid login credentials/i.test(text)) return 'Email atau kata sandi salah.';
  if (/email not confirmed/i.test(text)) return 'Email belum dikonfirmasi. Hubungi Leader Anda.';
  if (/rate limit|too many/i.test(text)) return 'Terlalu banyak percobaan. Tunggu sebentar lalu coba lagi.';
  return text || 'Gagal masuk. Coba lagi.';
}
