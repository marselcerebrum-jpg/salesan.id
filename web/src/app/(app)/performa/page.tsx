'use client';

import clsx from 'clsx';
import { ArrowLeft, ChevronRight } from 'lucide-react';
import { Suspense, useState } from 'react';
import useSWR from 'swr';

import { ActivityHistory } from '@/components/analytics/ActivityHistory';
import { AppChip } from '@/components/analytics/AppBadge';
import type { DrilldownKind } from '@/components/analytics/DrilldownPanel';
import { FilterFields } from '@/components/analytics/FilterFields';
import { PeriodMenu } from '@/components/analytics/PeriodMenu';
import { MemberBreakdownTable } from '@/components/analytics/MemberBreakdownTable';
import { PerformanceSummary } from '@/components/analytics/PerformanceSummary';
import {
  EmptyState,
  ErrorState,
  PageShell,
  ROLE_LABEL,
  RowSkeleton,
  StateBadge,
  formatHours,
  formatRangeID,
} from '@/components/analytics/Primitives';
import { inputClassSm } from '@/components/ui/control';
import { WaitingPanel } from '@/components/analytics/WaitingPanel';
import { fetcher, teamPerformancePath, type AnalyticsQuery } from '@/lib/api';
import {
  todayWIB,
  useAnalyticsFilter,
  type PerformanceMode,
  type ViewPatch,
  type ViewState,
} from '@/lib/useAnalyticsFilter';
import type { Me, MemberPerformance, TeamReport } from '@/lib/types';

/**
 * Performa — the one place operational figures live.
 *
 * Two subtabs at most, and only a Leader has both:
 *
 *   Performa Saya   my own work, or everything in my reach
 *   Performa PIC    each PIC, then their team, then one person
 *
 * Inside a tab the scope is a toggle rather than another tab. "Pribadi" and
 * "Gabungan" are the same question asked of a different set, and making them
 * tabs would imply they are different reports — they are one report, twice.
 *
 * Activity is never a tab. It sits at the bottom of whichever performance is
 * being read, where it explains the numbers above it; a history one navigation
 * step away from its own figures is a history nobody opens.
 */
export default function PerformaPage() {
  return (
    // useSearchParams needs a boundary; the filter lives in the URL so the
    // whole page depends on it.
    <Suspense
      fallback={
        <PageShell>
          <RowSkeleton count={4} />
        </PageShell>
      }
    >
      <Performa />
    </Suspense>
  );
}

function Performa() {
  // Today, not this month. The question somebody opens this page with is "how
  // are we doing right now": who has not been answered, what is still running.
  // A month-to-date figure answers a different question, and answers it in a
  // way that hides today inside an average of thirty days.
  const { query, view, setQuery, setView } = useAnalyticsFilter({ date: todayWIB() });
  const [drilldown, setDrilldown] = useState<DrilldownKind | null>(null);
  const [waitingOpen, setWaitingOpen] = useState(false);

  const team = useSWR<{ report: TeamReport }>(teamPerformancePath(query), fetcher, {
    refreshInterval: 300_000,
    // The figures already on screen stay there while the next ones load, so
    // changing a filter never blanks the page. `isValidating` is what the
    // toolbar turns into "Memperbarui data".
    keepPreviousData: true,
  });

  const report = team.data?.report;
  const role = report?.role ?? '';
  const members = report?.members ?? [];

  /*
   * Which row is mine: matched by id, never by role.
   *
   * This used to take the first member with the reader's role, which is the
   * reader only when they are the sole person in that role. With two Leaders
   * and three PICs it was somebody else: a PIC opening "Performa Saya" was
   * reading the first PIC's figures under the first PIC's name.
   */
  const { data: profile } = useSWR<Me>('/me', fetcher);
  const myId = profile?.user.id;
  const me = (myId && members.find((m) => m.user_id === myId)) || null;

  const isLeader = role === 'leader';
  const isFreelance = role === 'freelance';
  // A Freelance cannot land anywhere but their own figures, whatever the URL
  // says — the server enforces the same thing; this only keeps the page honest.
  const tab = isLeader ? view.tab : 'saya';
  const mode: PerformanceMode = isFreelance ? 'pribadi' : view.mode;

  /*
   * Whose figures the page is showing.
   *
   * Read once here because two things need it: the application list in the
   * filter row, and nothing else may disagree with it. On the PIC tab an
   * opened Freelance wins over the PIC, because that is who is on screen.
   */
  const focus =
    tab === 'freelance' || (tab === 'pic' && view.member)
      ? members.find((m) => m.user_id === view.member) ??
        members.find((m) => m.role === 'freelance' && m.user_id) ??
        null
      : tab === 'pic'
        ? members.find((m) => m.user_id === view.pic) ??
          members.find((m) => m.role === 'pic' && m.user_id) ??
          null
        : me;

  const shared = {
    onDrill: setDrilldown,
    drilldown,
    onCloseDrill: () => setDrilldown(null),
    onOpenWaiting: () => setWaitingOpen(true),
    // Pressing a row of the per-application breakdown writes the filter every
    // other control writes, so the whole page follows and the URL still
    // describes what is on screen.
    onPickApplication: (id: string | null) =>
      setQuery({ ...query, application_id: id ?? undefined }),
  };

  return (
    <PageShell>
      {/* Semibold rather than bold, with the tracking pulled in slightly. At
          24px Inter's bold weight shouts; the heading only needs to be first,
          not loud. */}
      {/* The same header the Dashboard has: what this page is on the left, the
          period it is reading on the right. */}
      <header className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.02em] text-ink">Performa</h1>
          <p className="mt-1 text-sm text-ink-muted">
            Pantau pekerjaan Anda sendiri. Rekap seluruh operasional ada di Dashboard.
            {role ? ` · ${ROLE_LABEL[role] ?? role}` : ''}
          </p>
        </div>

        <div className="flex flex-wrap items-start gap-4">
          <p className="text-2xs leading-snug text-ink-muted">
            <span className="flex items-center gap-1.5">
              <span
                aria-hidden
                className={clsx(
                  'size-1.5 rounded-full',
                  team.isValidating ? 'bg-warn' : 'bg-brand-600',
                )}
              />
              Periode terbaca
            </span>
            <span className="nums mt-0.5 block">
              {report ? formatRangeID(report.from, report.to) : 'Hari ini'}
            </span>
          </p>

          <PeriodMenu value={query} onChange={setQuery} />
        </div>
      </header>

      {isLeader ? (
        <div className="mt-6 flex flex-wrap gap-6 border-b border-hairline">
          {([
            { id: 'saya', label: 'Performa Saya' },
            { id: 'pic', label: 'Performa PIC' },
            // The same people the PIC walk ends at, reached without having to
            // know whose team they are on.
            { id: 'freelance', label: 'Performa Freelance' },
          ] as const).map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => setView({ tab: t.id, pic: '', member: '' })}
              aria-current={tab === t.id ? 'page' : undefined}
              className={clsx(
                '-mb-px border-b-2 pb-2.5 text-sm font-medium transition-colors',
                tab === t.id
                  ? 'border-brand-700 text-ink'
                  : 'border-transparent text-ink-muted hover:text-ink-soft',
              )}
            >
              {t.label}
            </button>
          ))}
        </div>
      ) : null}

      {/*
       * Where and what kind. Not who.
       *
       * Whose figures these are is decided by the tabs and by opening a row —
       * a dropdown that does the same thing is a second door to the same room,
       * and the two eventually disagree about which one is open. What is left
       * is the narrowing that applies to whoever is being read: which
       * application, which number, which kind of chat.
       */}
      <section
        aria-label="Filter"
        className="mt-4 rounded-card border border-hairline bg-surface-raised px-4 py-3.5 shadow-e1"
      >
        <FilterFields
          value={query}
          onChange={setQuery}
          people={false}
          // Only the applications the person on screen actually holds. On a
          // Leader's own tab they hold none in particular, so the full list
          // stands.
          applications={focus?.applications.length ? focus.applications : undefined}
          leading={
            tab === 'pic' ? (
              <PersonSelect
                label="PIC"
                value={view.pic ?? ''}
                people={members.filter((m) => m.role === 'pic' && m.user_id)}
                onChange={(id) => setView({ pic: id, member: '' })}
              />
            ) : tab === 'freelance' ? (
              <PersonSelect
                label="Freelance"
                value={view.member ?? ''}
                people={members.filter((m) => m.role === 'freelance' && m.user_id)}
                onChange={(id) => setView({ member: id })}
              />
            ) : null
          }
        />
      </section>

      {team.error ? (
        <div className="mt-5">
          <ErrorState
            message={team.error instanceof Error ? team.error.message : 'Gagal memuat.'}
            onRetry={() => void team.mutate()}
          />
        </div>
      ) : null}

      {tab === 'freelance' ? (
        <FreelancePerformance query={query} report={report} view={view} {...shared} />
      ) : tab === 'saya' ? (
        <OwnPerformance
          query={query}
          me={me}
          role={role}
          mode={mode}
          showToggle={!isFreelance}
          onMode={(m) => setView({ mode: m })}
          report={report}
          view={view}
          setView={setView}
          {...shared}
        />
      ) : (
        <PICPerformance
          query={query}
          report={report}
          view={view}
          setView={setView}
          {...shared}
        />
      )}

      <WaitingPanel open={waitingOpen} query={query} onClose={() => setWaitingOpen(false)} />
    </PageShell>
  );
}

/* --- Performa Saya --------------------------------------------------------- */

interface ScopeProps {
  query: AnalyticsQuery;
  onDrill: (k: DrilldownKind) => void;
  drilldown: DrilldownKind | null;
  onCloseDrill: () => void;
  onOpenWaiting: () => void;
  onPickApplication: (id: string | null) => void;
}

/**
 * The reader's own tab.
 *
 * "Pribadi" narrows to their account id, which is what keeps a PIC's own
 * figures free of their team's work and everyone's free of the phone activity
 * nobody can be credited with. "Gabungan" drops that filter: for a Leader it is
 * the whole organisation, for a PIC it is their applications — and in both
 * cases the unattributed device activity is included in the totals but shown
 * separately below, because dividing it among whoever was on shift would be a
 * guess.
 */
function OwnPerformance({
  query,
  me,
  role,
  mode,
  showToggle,
  onMode,
  report,
  view,
  setView,
  ...rest
}: ScopeProps & {
  me: MemberPerformance | null;
  role: string;
  mode: PerformanceMode;
  showToggle: boolean;
  onMode: (m: PerformanceMode) => void;
  report: TeamReport | undefined;
  view: ViewState;
  setView: (v: ViewPatch) => void;
}) {
  const isLeader = role === 'leader';

  // A person opened from the list below shows their own performance.
  const opened = view.member
    ? (report?.members ?? []).find((m) => m.user_id === view.member) ?? null
    : null;

  if (opened?.user_id) {
    return (
      <MemberDetail
        member={opened}
        query={{ ...query, freelance_id: opened.user_id, pic_id: undefined }}
        onBack={() => setView({ member: '' })}
        backLabel={isLeader ? 'Performa Saya' : 'Tim saya'}
        {...rest}
      />
    );
  }

  const scoped: AnalyticsQuery =
    mode === 'pribadi' && me?.user_id
      ? { ...query, freelance_id: me.user_id, pic_id: undefined }
      : isLeader
        ? { ...query, freelance_id: undefined }
        : // A PIC's "gabungan" is their applications: their own work, their
          // Freelance's, and the device activity on those numbers.
          { ...query, freelance_id: undefined, pic_id: me?.user_id ?? query.pic_id };

  return (
    <>
      {showToggle ? (
        <ScopeToggle
          value={mode}
          onChange={onMode}
          combinedLabel="Tim"
          hint={
            mode === 'pribadi'
              ? 'Hanya aktivitas yang dilakukan oleh akun Anda.'
              : isLeader
                ? 'Gabungan aktivitas Anda, PIC, dan Freelance dalam cakupan akses Anda. Termasuk aktivitas perangkat tanpa pelaksana teridentifikasi.'
                : 'Gabungan aktivitas Anda dan Freelance yang berada dalam tanggung jawab Anda.'
          }
        />
      ) : null}

      {me ? (
        <MemberHeader member={me} />
      ) : null}

      {mode === 'pribadi' ? <DeviceActivityHint report={report} /> : null}

      <PerformanceSummary query={scoped} showApplicationSplit {...rest} />

      {mode === 'gabungan' ? <UnattributedNote report={report} /> : null}

      {/*
        * A PIC only.
        *
        * Not a Leader: they have the Performa PIC subtab, which is the same
        * walk done properly (every PIC, then their team, then one person), and
        * repeating the top of it here would be a second door to the same room
        * that the two would eventually disagree about.
        *
        * Not a Freelance either: nobody reports to them, so the list narrows to
        * the one account already being read, and a table whose only row is the
        * person looking at it tells them nothing they are not already reading
        * above it.
        */}
      {role === 'pic' ? (
        <MemberBreakdownTable
          query={{ ...query, freelance_id: undefined, pic_id: undefined }}
          role="freelance"
          title="Freelance"
          description="Angka pada baris adalah pekerjaan akun Freelance itu sendiri. Tekan satu baris untuk membuka performa dan riwayat aktivitasnya."
          subjectLabel="Freelance"
          csvName="freelance-saya"
          emptyTitle="Belum ada Freelance."
          emptyHint="Buat akunnya di halaman Pengaturan."
          onOpen={(m) => m.user_id && setView({ member: m.user_id })}
        />
      ) : null}

      <div className="mt-3">
        <ActivityHistory
          query={scoped}
          description={
            mode === 'pribadi'
              ? 'Aktivitas yang benar-benar dilakukan akun ini lewat web. Pesan dan perubahan label yang dilakukan dari HP tidak masuk ke sini, karena WhatsApp tidak menyebut pelakunya.'
              : 'Seluruh aktivitas dalam lingkup akun Anda.'
          }
        />
      </div>
    </>
  );
}

/* --- Performa Freelance ----------------------------------------------------- */

/**
 * One Freelance, open on arrival.
 *
 * A list somebody has to press before seeing anything is a step that earns
 * nothing here: this tab exists to read one person, and it opens on one. The
 * chooser is the filter row's first field, which is also where somebody looks
 * for it after using the three fields beside it.
 *
 * Comparing people against each other is a different question, and it has its
 * own answer: the team ranking on the Dashboard, where every Freelance sits
 * under their PIC with the same three figures.
 */
function FreelancePerformance({
  query,
  report,
  view,
  ...rest
}: ScopeProps & {
  report: TeamReport | undefined;
  // Read, not written: which Freelance is open is set by the field in the
  // filter row, so this view only follows it.
  view: ViewState;
}) {
  const people = (report?.members ?? []).filter((m) => m.role === 'freelance' && m.user_id);
  // The first one until somebody chooses otherwise, so the tab is never a
  // blank page waiting to be told what to show.
  const opened = people.find((m) => m.user_id === view.member) ?? people[0] ?? null;

  if (!opened?.user_id) {
    return (
      <div className="mt-5">
        <EmptyState
          title="Belum ada Freelance."
          hint="Buat akunnya di halaman Pengaturan."
        />
      </div>
    );
  }

  const scoped: AnalyticsQuery = {
    ...query,
    freelance_id: opened.user_id,
    pic_id: undefined,
  };

  return (
    <>
      <MemberHeader member={opened} />
      <PerformanceSummary query={scoped} showApplicationSplit workHours {...rest} />
      <div className="mt-3">
        <ActivityHistory query={scoped} />
      </div>
    </>
  );
}

/** One person, chosen from a list, as a field in the filter row. */
function PersonSelect({
  label,
  value,
  people,
  onChange,
}: {
  label: string;
  value: string;
  people: MemberPerformance[];
  onChange: (id: string) => void;
}) {
  // The effective choice, which is the first person until one is made. Shown
  // as selected rather than left blank, because blank would describe a page
  // that is not what is on screen.
  const current = people.some((m) => m.user_id === value) ? value : (people[0]?.user_id ?? '');

  return (
    <label className="mb-3 block max-w-xs min-w-0">
      <span className="mb-1.5 block truncate text-2xs font-medium text-ink-muted">{label}</span>
      <select
        value={current}
        onChange={(e) => onChange(e.target.value)}
        disabled={people.length === 0}
        className={`w-full min-w-0 ${inputClassSm}`}
      >
        {people.length === 0 ? <option value="">Belum ada</option> : null}
        {people.map((m) => (
          <option key={m.user_id} value={m.user_id ?? ''}>
            {m.name}
          </option>
        ))}
      </select>
    </label>
  );
}

/* --- Performa PIC ---------------------------------------------------------- */

/**
 * One PIC, open on arrival.
 *
 * The walk used to start at a list of every PIC. Opening on the first one
 * removes a press that told nobody anything: this tab is about one team, and
 * which team is a field in the filter row above. Pressing a Freelance in the
 * table below still opens that person, and still has its own URL, so a Leader
 * can send a colleague the exact view they were reading.
 */
function PICPerformance({
  query,
  report,
  view,
  setView,
  ...rest
}: ScopeProps & {
  report: TeamReport | undefined;
  view: ViewState;
  setView: (v: ViewPatch) => void;
}) {
  const members = report?.members ?? [];
  const pics = members.filter((m) => m.role === 'pic' && m.user_id);
  const openedPIC = pics.find((m) => m.user_id === view.pic) ?? pics[0] ?? null;
  const openedMember = view.member ? members.find((m) => m.user_id === view.member) ?? null : null;

  if (openedMember?.user_id && openedPIC) {
    return (
      <MemberDetail
        member={openedMember}
        query={{ ...query, freelance_id: openedMember.user_id, pic_id: undefined }}
        onBack={() => setView({ member: '' })}
        backLabel={openedPIC.name}
        {...rest}
      />
    );
  }

  if (openedPIC?.user_id) {
    const scoped: AnalyticsQuery =
      view.picMode === 'pribadi'
        ? { ...query, freelance_id: openedPIC.user_id, pic_id: undefined }
        : { ...query, freelance_id: undefined, pic_id: openedPIC.user_id };

    return (
      <>
        <ScopeToggle
          value={view.picMode}
          onChange={(m) => setView({ picMode: m })}
          personalLabel="Pribadi PIC"
          combinedLabel="Tim PIC"
          hint={
            view.picMode === 'pribadi'
              ? `Hanya aktivitas akun ${openedPIC.name}.`
              : `Aktivitas ${openedPIC.name}, Freelance di bawahnya, dan perangkat pada aplikasinya.`
          }
        />
        {view.picMode === 'pribadi' ? (
          <MemberHeader member={openedPIC} />
        ) : null}

        <PerformanceSummary query={scoped} showApplicationSplit workHours {...rest} />

        {view.picMode === 'gabungan' ? <UnattributedNote report={report} /> : null}

        <MemberBreakdownTable
          // The Freelance under this PIC. The query already carries pic_id,
          // which is what narrows the list to their team.
          query={{ ...query, pic_id: openedPIC.user_id, freelance_id: undefined }}
          role="freelance"
          title="Freelance"
          description="Angka pada baris adalah pekerjaan akun Freelance itu sendiri. Tekan satu baris untuk membuka performa dan riwayat aktivitasnya."
          subjectLabel="Freelance"
          csvName={`freelance-${openedPIC.name}`}
          emptyTitle="PIC ini belum punya Freelance."
          emptyHint="Buat akunnya di halaman Pengaturan."
          onOpen={(m) => m.user_id && setView({ member: m.user_id })}
        />

        <div className="mt-3">
          <ActivityHistory query={scoped} />
        </div>
      </>
    );
  }

  return (
    <div className="mt-5">
      <EmptyState title="Belum ada PIC." hint="Buat akun PIC di halaman Pengaturan." />
    </div>
  );
}

/* --- shared pieces --------------------------------------------------------- */

/** Personal versus combined, as one control rather than two tabs. */
function ScopeToggle({
  value,
  onChange,
  personalLabel = 'Pribadi',
  combinedLabel,
  hint,
}: {
  value: PerformanceMode;
  onChange: (m: PerformanceMode) => void;
  personalLabel?: string;
  combinedLabel: string;
  hint: string;
}) {
  return (
    <div className="mt-4 flex flex-wrap items-center gap-3">
      <div className="inline-flex rounded-lg border border-hairline bg-surface-raised p-0.5">
        {([
          { id: 'pribadi' as const, label: personalLabel },
          { id: 'gabungan' as const, label: combinedLabel },
        ]).map((o) => (
          <button
            key={o.id}
            type="button"
            onClick={() => onChange(o.id)}
            aria-pressed={value === o.id}
            className={clsx(
              'rounded-lg px-3 py-1.5 text-sm font-medium transition-colors',
              value === o.id
                ? 'bg-brand-700 text-white'
                : 'text-ink-muted hover:text-ink-soft',
            )}
          >
            {o.label}
          </button>
        ))}
      </div>
      <p className="text-xs text-ink-muted">{hint}</p>
    </div>
  );
}

/**
 * Device activity nobody can be credited with.
 *
 * Shown as its own block on every combined view rather than folded into the
 * cards silently: it is inside the totals, and the reader should know that the
 * difference between the totals and the sum of the people is this.
 */
function UnattributedNote({ report }: { report: TeamReport | undefined }) {
  const u = report?.unattributed;
  const total = u ? u.outbound_manual + u.group_replies + u.contacts_served : 0;
  if (!u || total === 0) return null;

  return (
    <section className="mt-6 rounded-card border border-hairline bg-surface-sunken/40 px-4 py-3.5">
      <h3 className="text-base font-semibold text-ink">Aktivitas perangkat</h3>
      <p className="mt-0.5 text-xs text-ink-muted">
        Dikirim langsung dari HP. WhatsApp tidak memberi tahu siapa pelakunya, jadi ini termasuk
        dalam angka gabungan di atas tetapi tidak dibebankan ke performa siapa pun.
      </p>
      <div className="mt-2.5 grid gap-2 sm:grid-cols-3">
        <Small label="Pesan Keluar" value={String(u.outbound_manual)} />
        <Small label="Kontak Ditangani" value={String(u.contacts_served)} />
        <Small label="Pesan Keluar Grup" value={String(u.group_replies)} />
      </div>
    </section>
  );
}

/**
 * Why a personal view can read zero on a busy day.
 *
 * Shown on every personal view, not only when the cards are empty: the
 * figures below count what was done signed in on the web, and a team that
 * answers from the phone all day has its work on the number, not on a person.
 * Without this the three personal tabs read as one identical, broken page.
 */
function DeviceActivityHint({ report }: { report: TeamReport | undefined }) {
  const u = report?.unattributed;
  const total = u ? u.outbound_manual + u.group_replies : 0;
  if (!u || total === 0) return null;

  return (
    <p className="mt-3 rounded-lg border border-hairline-strong bg-surface-sunken px-3 py-2 text-xs leading-relaxed text-ink-soft">
      <span className="font-medium text-ink">
        {total.toLocaleString('id-ID')} pesan keluar
      </span>{' '}
      pada periode ini dikirim langsung dari HP. WhatsApp tidak menyebut siapa yang mengirimnya,
      jadi pesan itu tercatat atas nomornya dan tidak masuk ke angka pribadi siapa pun. Hanya
      pekerjaan yang dilakukan lewat web yang tercatat atas nama orang; lihat rinciannya di
      &quot;Tim&quot; atau di Dashboard.
    </p>
  );
}

/** One person's own performance, with their history under it. */
function MemberDetail({
  member,
  query,
  onBack,
  backLabel,
  crumbs = [],
  ...rest
}: ScopeProps & {
  member: MemberPerformance;
  onBack: () => void;
  backLabel: string;
  crumbs?: { label: string; onClick?: () => void }[];
}) {
  return (
    <>
      <Breadcrumb
        items={[...crumbs, { label: backLabel, onClick: onBack }, { label: member.name }]}
      />
      <MemberHeader member={member} />
      {/* Same structure as every other scope on this page: the cards, then the
          day by day table, then the split per application. Somebody who holds
          three applications is read the same way whether they are looking at
          themselves or being looked at by their PIC.

          The conversation chart counts only their shift hours: this view is
          about one person, and the traffic that arrived while they were off is
          not theirs. */}
      <PerformanceSummary query={query} showApplicationSplit workHours {...rest} />
      <div className="mt-3">
        <ActivityHistory query={query} />
      </div>
    </>
  );
}

function Breadcrumb({ items }: { items: { label: string; onClick?: () => void }[] }) {
  // Opening a PIC's own row from inside their team detail puts their name in
  // the trail twice. Dropping the repeat is honest: the trail describes a path,
  // and a path does not visit the same place twice in a row.
  const trail = items.filter((item, i) => i === 0 || item.label !== items[i - 1].label);

  return (
    <nav aria-label="Jalur" className="mt-4 flex flex-wrap items-center gap-1.5 text-sm">
      {trail.map((item, i) => (
        <span key={`${item.label}-${i}`} className="flex items-center gap-1.5">
          {i > 0 ? <ChevronRight className="size-3.5 text-ink-muted" /> : null}
          {item.onClick ? (
            <button
              type="button"
              onClick={item.onClick}
              className="inline-flex items-center gap-1 text-brand-700 underline-offset-2 hover:underline"
            >
              {i === 0 ? <ArrowLeft className="size-3.5" /> : null}
              {item.label}
            </button>
          ) : (
            <span className="font-medium text-ink">{item.label}</span>
          )}
        </span>
      ))}
    </nav>
  );
}

/**
 * Who the figures below belong to.
 *
 * The application chips say which applications this person is measured on, and
 * nothing more. They used to be the filter as well — press one to narrow the
 * page — which made two controls for one choice: the chips and the Aplikasi
 * field in the filter row. Two doors to the same room eventually disagree
 * about which one is open, and the field is the one somebody looks for.
 */
function MemberHeader({ member }: { member: MemberPerformance }) {
  return (
    <div className="mt-4 rounded-card border border-hairline bg-surface-raised px-4 py-3.5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-lg font-semibold tracking-[-0.01em] text-ink">{member.name}</p>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-ink-muted">
            <span>{ROLE_LABEL[member.role] ?? 'Tanpa peran'}</span>
            {member.pic_name ? <span>· PIC {member.pic_name}</span> : null}
            {member.applications.length > 0 ? (
              <span className="flex flex-wrap items-center gap-1.5">
                ·
                {member.applications.map((a) => (
                  <AppChip key={a.id} code={a.code} color={a.color} name={a.name} />
                ))}
              </span>
            ) : (
              <span>· Belum ditugaskan ke aplikasi</span>
            )}
          </div>
        </div>
        <StateBadge
          label={member.on_duty ? 'Bertugas' : 'Tidak bertugas'}
          tone={member.on_duty ? 'good' : 'neutral'}
        />
      </div>

      <div className="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
        <Small label="Pesan Keluar" value={String(member.personal.outbound_manual)} />
        <Small label="Kontak Ditangani" value={String(member.personal.contacts_served)} />
        <Small label="SLA Tercapai" value={String(member.personal.sla_achieved)} />
        <Small label="Follow-up" value={String(member.personal.follow_ups)} />
        <Small label="Jam Kerja" value={formatHours(member.personal.work_seconds)} />
        <Small label="Di Luar Jadwal" value={String(member.personal.activities_out_of_schedule)} />
      </div>
    </div>
  );
}

function Small({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-lg bg-surface-sunken/60 px-2.5 py-2">
      <p className="truncate text-2xs text-ink-muted">{label}</p>
      <p className="truncate text-sm font-semibold text-ink nums">{value}</p>
    </div>
  );
}
