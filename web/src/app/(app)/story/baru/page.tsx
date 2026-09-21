'use client';

import { useSearchParams } from 'next/navigation';
import { Suspense } from 'react';

import { StoryComposerPage } from '@/components/campaign/StoryComposerPage';

/**
 * Composing a WA Story. Its own form: a Story has no recipients and no pacing.
 *
 * Also the way back into one that already exists: ?dari=<id> fills the form from
 * another story and saves a new one, ?edit=<id> fills it and saves back onto the
 * same row. Both are read-only until the form is saved.
 */
export default function NewStoryPage() {
  return (
    <Suspense fallback={null}>
      <Composer />
    </Suspense>
  );
}

function Composer() {
  const params = useSearchParams();
  const edit = params.get('edit');
  const copy = params.get('dari');
  return (
    <StoryComposerPage
      sourceID={edit ?? copy ?? undefined}
      mode={edit ? 'edit' : copy ? 'copy' : 'new'}
    />
  );
}
