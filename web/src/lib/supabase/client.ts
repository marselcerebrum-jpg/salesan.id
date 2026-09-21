'use client';

import { createBrowserClient } from '@supabase/ssr';

/**
 * Browser-side Supabase client. Used for auth only: all application data goes
 * through the Go API so that WhatsApp sessions and workspace scoping stay
 * server-side.
 */
export function createClient() {
  return createBrowserClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
  );
}

let singleton: ReturnType<typeof createBrowserClient> | null = null;

/** Reuses one client per tab so the auth listener is not duplicated. */
export function getSupabaseBrowserClient() {
  if (!singleton) {
    singleton = createClient();
  }
  return singleton;
}
