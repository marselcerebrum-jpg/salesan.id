'use client';

import { CampaignPage } from '@/components/campaign/CampaignPage';

/** WA Story. See the note in the Broadcast page for why they share a component. */
export default function StoryPage() {
  return <CampaignPage type="story" />;
}
