'use client';

import { useParams } from 'next/navigation';

import { StoryDetailPage } from '@/components/campaign/StoryDetailPage';

/** One Story's report: where it is live, and who watched. */
export default function StoryDetailRoute() {
  const params = useParams<{ id: string }>();
  return <StoryDetailPage id={params.id} />;
}
