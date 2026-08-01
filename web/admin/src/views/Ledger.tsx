/**
 * What happened.
 *
 * Every manifest the gateway has ever recorded, newest first, with the state
 * it settled into. This is the part of the product that is actually the point:
 * a permanent record of human judgement about what software was allowed to do.
 *
 * The attribution — who decided, and why — lives in the gateway's approvals
 * table, which no JSON route exposes yet. Each row asks for it and fills in
 * when a route answers.
 */

import { useMemo, useState } from 'react';
import { useQueries } from '@tanstack/react-query';
import { keys, useManifestIndex } from '../api/queries';
import { getManifestApprovals } from '../api/client';
import type { Manifest, ManifestState } from '../api/types';
import { MANIFEST_STATES } from '../api/types';
import { Link } from '../lib/router';
import { formatTimestamp, pluralise } from '../lib/format';
import { EmptyState, Hash, Loading, PageHeading, StateChip } from '../components/primitives';
import { Failure } from '../components/Failure';
import { DecisionLine } from './DecisionRecord';

type Filter = 'ALL' | ManifestState;
const FILTERS: Filter[] = ['ALL', ...MANIFEST_STATES];
const FILTER_LABEL: Record<Filter, string> = {
  ALL: 'Everything',
  PENDING: 'Pending',
  APPROVED: 'Approved',
  REJECTED: 'Rejected',
  SUPERSEDED: 'Superseded',
};

export function LedgerView() {
  const index = useManifestIndex();
  const [filter, setFilter] = useState<Filter>('ALL');
  const [server, setServer] = useState<string>('ALL');

  const all = useMemo(
    () => [...(index.data?.manifests ?? [])].sort((a, b) => b.id - a.id),
    [index.data],
  );

  const servers = useMemo(() => [...new Set(all.map((m) => m.server))].sort(), [all]);

  const rows = all.filter(
    (m) => (filter === 'ALL' || m.state === filter) && (server === 'ALL' || m.server === server),
  );

  const approvals = useQueries({
    queries: rows.slice(0, 50).map((m) => ({
      queryKey: keys.approvals(m.id),
      queryFn: () => getManifestApprovals(m.id),
      staleTime: 60_000,
    })),
  });

  const attributionServed = approvals.some((query) => query.data !== null && query.data !== undefined);

  return (
    <>
      <PageHeading
        eyebrow="The record"
        title="Ledger"
        lede="Every capability set the gateway has fingerprinted, and what was decided about it. Manifests are never edited: a manifest is approved, rejected, or superseded, and its hash and contents stay exactly as first seen."
        aside={
          all.length > 0 && (
            <p className="font-sans text-sm text-slate/70">
              {all.length} {pluralise(all.length, 'manifest')} recorded
            </p>
          )
        }
      />

      {index.error ? (
        <Failure error={index.error} retry={() => void index.refetch()} />
      ) : index.isPending ? (
        <Loading label="Reading the ledger…" />
      ) : all.length === 0 ? (
        <EmptyState title="Nothing recorded yet">
          <p>
            The gateway has not fingerprinted a capability set. The first entry appears the first
            time a client connects to an upstream server through it.
          </p>
        </EmptyState>
      ) : (
        <>
          <div className="mb-6 flex flex-wrap items-center gap-x-6 gap-y-3 border-b border-rule pb-4">
            <FilterGroup
              legend="State"
              options={FILTERS.map((f) => ({ value: f, label: FILTER_LABEL[f] }))}
              value={filter}
              onChange={(value) => setFilter(value as Filter)}
            />
            {servers.length > 1 && (
              <FilterGroup
                legend="Server"
                options={[
                  { value: 'ALL', label: 'All servers' },
                  ...servers.map((name) => ({ value: name, label: name })),
                ]}
                value={server}
                onChange={setServer}
              />
            )}
          </div>

          {!attributionServed && (
            <div className="mb-6">
              <EmptyState tone="held" title="Attribution is not served by this gateway">
                <p>
                  The gateway records who approved or rejected each manifest, when, and why. There
                  is no JSON route for it, so the ledger below can show what was decided but not who
                  decided it.
                </p>
                <p>
                  Add <span className="font-mono text-[0.8125rem]">GET /api/manifests/{'{id}'}/approvals</span>{' '}
                  and every row fills in.
                </p>
              </EmptyState>
            </div>
          )}

          {rows.length === 0 ? (
            <EmptyState title="Nothing matches that filter">
              <p>
                The record holds no{' '}
                {filter === 'ALL' ? 'manifest' : `${FILTER_LABEL[filter].toLowerCase()} manifest`}
                {server === 'ALL' ? '' : ` for ${server}`}.
              </p>
            </EmptyState>
          ) : (
            <ol className="card divide-y divide-rule">
              {rows.map((manifest, position) => (
                <li key={manifest.id}>
                  <LedgerRow
                    manifest={manifest}
                    approvals={position < 50 ? (approvals[position]?.data ?? null) : null}
                  />
                </li>
              ))}
            </ol>
          )}
        </>
      )}
    </>
  );
}

function LedgerRow({
  manifest,
  approvals,
}: {
  manifest: Manifest;
  approvals: Awaited<ReturnType<typeof getManifestApprovals>>;
}) {
  return (
    <div className="px-5 py-4">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1.5">
        <Link
          to={`/manifests/${manifest.id}`}
          className="underline decoration-rule underline-offset-4"
        >
          <Hash value={manifest.hash} className="text-sm" />
        </Link>
        <span className="font-display text-base font-semibold text-ink">{manifest.server}</span>
        <StateChip state={manifest.state} />
        <span className="ml-auto text-sm text-slate/55">
          recorded {formatTimestamp(manifest.created_at)}
        </span>
      </div>

      {approvals && approvals.length > 0 && (
        <ol className="mt-3 space-y-3 border-l-2 border-rule pl-4">
          {approvals.map((record) => (
            <li key={record.id}>
              <DecisionLine record={record} />
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}

function FilterGroup({
  legend,
  options,
  value,
  onChange,
}: {
  legend: string;
  options: { value: string; label: string }[];
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <fieldset>
      <legend className="label mb-1.5">{legend}</legend>
      <div className="flex flex-wrap gap-1">
        {options.map((option) => (
          <button
            key={option.value}
            type="button"
            aria-pressed={value === option.value}
            onClick={() => onChange(option.value)}
            className={`rounded-sm border px-2.5 py-1 text-sm transition-colors ${
              value === option.value
                ? 'border-slate bg-slate text-paper'
                : 'border-rule bg-white text-slate hover:border-slate/40'
            }`}
          >
            {option.label}
          </button>
        ))}
      </div>
    </fieldset>
  );
}
