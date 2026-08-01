/**
 * What is waiting on me.
 *
 * The queue answers one question per row: what is this server trying to
 * change, and how much does that matter? The heaviest change in a manifest
 * sets the row's weight, so a changed input schema does not sit at the same
 * visual pitch as a reworded sentence.
 *
 * The list endpoint carries `changes` as prose lines. The rows read the
 * structured diff instead, so the queue and the detail view rank the same
 * event the same way rather than one parsing sentences and the other reading
 * fields.
 */

import { useMemo } from 'react';
import { useQueries } from '@tanstack/react-query';
import { usePending, keys } from '../api/queries';
import { getManifestDiff } from '../api/client';
import { byAge, groupChanges, toChangeItems, topKind, withheldCount } from '../api/derive';
import type { ChangeItem } from '../api/derive';
import type { PendingManifest } from '../api/types';
import { Link } from '../lib/router';
import { formatAge, formatTimestamp, pluralise } from '../lib/format';
import {
  Count,
  EmptyState,
  Hash,
  HeldMark,
  KIND_WEIGHT,
  Loading,
  PageHeading,
} from '../components/primitives';
import { Failure } from '../components/Failure';

export function PendingView() {
  const { data, isPending, error, refetch } = usePending();

  const queue = useMemo(() => (data ? [...data].sort(byAge) : []), [data]);

  const diffs = useQueries({
    queries: queue.map((m) => ({
      queryKey: keys.diff(m.id),
      queryFn: () => getManifestDiff(m.id),
    })),
  });

  const rows = queue.map((manifest, index) => ({
    manifest,
    items: toChangeItems(diffs[index]?.data ?? null),
  }));

  const totalWithheld = rows.reduce((sum, row) => sum + withheldCount(row.items), 0);

  return (
    <>
      <PageHeading
        eyebrow="Waiting on a decision"
        title="Pending manifests"
        lede="Each of these is a capability set the gateway has fingerprinted and is holding back. Until one is approved or rejected, the capabilities it introduces reach no client."
        aside={
          queue.length > 0 && (
            <div className="flex gap-8">
              <Count value={queue.length} unit={pluralise(queue.length, 'manifest')} />
              <Count value={totalWithheld} unit="withheld" tone="text-held" />
            </div>
          )
        }
      />

      {error ? (
        <Failure error={error} retry={() => void refetch()} />
      ) : isPending ? (
        <Loading label="Reading the queue…" />
      ) : queue.length === 0 ? (
        <EmptyState title="Nothing is waiting">
          <p>
            Every capability set the gateway has seen has a decision recorded against it. Traffic is
            flowing against approved baselines only.
          </p>
          <p>
            A new manifest lands here the moment an upstream server advertises something that does
            not match what you approved.
          </p>
        </EmptyState>
      ) : (
        <ul className="space-y-3">
          {rows.map(({ manifest, items }) => (
            <li key={manifest.id}>
              <QueueRow manifest={manifest} items={items} />
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

function QueueRow({ manifest, items }: { manifest: PendingManifest; items: ChangeItem[] }) {
  const heaviest = topKind(items);
  const held = withheldCount(items);
  const weight = heaviest ? KIND_WEIGHT[heaviest] : null;
  const withheldNames = items.filter((item) => item.withheld).map((item) => item.identity);

  return (
    <Link
      to={`/manifests/${manifest.id}`}
      className="group relative block overflow-hidden rounded-sm border border-rule bg-white transition-shadow hover:shadow-lift"
    >
      <span
        aria-hidden
        className={`absolute inset-y-0 left-0 w-[3px] ${weight ? weight.rule : 'bg-signal'}`}
      />

      <div className="flex flex-col gap-4 px-5 py-4 sm:flex-row sm:items-start sm:gap-6">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h2 className="font-display text-lg font-semibold text-ink">{manifest.server}</h2>
            <Hash value={manifest.hash} />
            <span
              className="font-display text-micro uppercase tracking-[0.12em] text-signal"
              title={formatTimestamp(manifest.created_at)}
            >
              recorded {formatAge(manifest.created_at)} ago
            </span>
          </div>

          {items.length > 0 && (
            <ul className="mt-3 flex flex-wrap gap-x-2 gap-y-1.5">
              {groupChanges(items).map((group) => (
                <li
                  key={group.kind}
                  className={`inline-flex items-center gap-1.5 rounded-sm border px-2 py-0.5 ${KIND_WEIGHT[group.kind].badge}`}
                >
                  <span className="font-display text-micro uppercase tracking-[0.12em]">
                    {group.title}
                  </span>
                  <span className="font-display text-micro font-semibold tabular-nums">
                    {group.items.length}
                  </span>
                </li>
              ))}
            </ul>
          )}

          {withheldNames.length > 0 && (
            <p className="mt-3 truncate font-mono text-[0.8125rem] text-slate/70">
              {withheldNames.slice(0, 4).join('  ·  ')}
              {withheldNames.length > 4 && (
                <span className="ml-3 font-sans text-slate/55">
                  and {withheldNames.length - 4} more
                </span>
              )}
            </p>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-4 sm:flex-col sm:items-end sm:gap-2">
          {held > 0 && <HeldMark label={`${held} withheld`} />}
          <span className="font-sans text-sm text-slate/60 group-hover:text-ink">Review →</span>
        </div>
      </div>
    </Link>
  );
}
