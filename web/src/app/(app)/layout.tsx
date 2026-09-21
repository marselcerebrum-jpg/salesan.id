import { redirect } from 'next/navigation';

import { AppShell } from '@/components/layout/AppShell';
import { createClient } from '@/lib/supabase/server';

/**
 * Guards every authenticated route. `middleware.ts` already redirects signed-out
 * visitors; this second check protects against a stale cookie reaching a Server
 * Component directly.
 */
export default async function AppLayout({ children }: { children: React.ReactNode }) {
  const supabase = await createClient();
  const {
    data: { user },
  } = await supabase.auth.getUser();

  if (!user) redirect('/login');

  return <AppShell>{children}</AppShell>;
}
