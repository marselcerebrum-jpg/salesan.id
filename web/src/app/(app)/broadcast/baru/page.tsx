'use client';

import { useSearchParams } from 'next/navigation';
import { Suspense } from 'react';

import { ComposerPage } from '@/components/campaign/ComposerPage';

/**
 * Composing a broadcast. A page, so the recipient lists have room to breathe.
 *
 * Also the way back into one that already exists: ?dari=<id> fills the form from
 * another campaign and saves a new one, ?edit=<id> fills it from a campaign and
 * saves back onto the same row. Both are read-only until the form is saved.
 */
export default function NewBroadcastPage() {
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
    <ComposerPage
      type="broadcast"
      sourceID={edit ?? copy ?? undefined}
      mode={edit ? 'edit' : copy ? 'copy' : 'new'}
    />
  );
}
