'use client';

import { useParams } from 'next/navigation';

import { CampaignDetailPage } from '@/components/campaign/CampaignDetailPage';

/** One broadcast's report, on its own page so it has a URL worth sending. */
export default function BroadcastDetailRoute() {
  const params = useParams<{ id: string }>();
  return <CampaignDetailPage id={params.id} type="broadcast" />;
}
