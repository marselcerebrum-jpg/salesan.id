'use client';

import { CampaignPage } from '@/components/campaign/CampaignPage';

/**
 * Broadcast.
 *
 * Both this and WA Story render the same component: they are one object with
 * two delivery mechanisms, and keeping two copies of the screen would mean
 * keeping two copies of every permission rule and every status word.
 */
export default function BroadcastPage() {
  return <CampaignPage type="broadcast" />;
}
