'use client';

import useSWR from 'swr';

import { fetcher } from '@/lib/api';
import type { Application } from '@/lib/types';

/**
 * The logo for an application, looked up by its code.
 *
 * Every application tile in the product is drawn by an AppMark that knows the
 * code and the colour and nothing else. Threading a logo URL into each of those
 * call sites would be a change to every screen; asking one shared list is not.
 * SWR keeps a single request and a single cache for the whole page however many
 * tiles ask, and it is the same list the settings page edits, so an upload shows
 * up everywhere as soon as that list is refreshed.
 *
 * The links in the list expire after an hour, so it is re-read every half hour
 * while the page is open.
 */
export function useAppIcon(code: string | null | undefined): string | null {
  const { data } = useSWR<{ applications: Application[] }>('/applications', fetcher, {
    refreshInterval: 30 * 60 * 1000,
    revalidateOnFocus: false,
    dedupingInterval: 60 * 1000,
  });
  if (!code) return null;
  const needle = code.toUpperCase();
  return data?.applications.find((a) => a.code.toUpperCase() === needle)?.icon_url ?? null;
}
