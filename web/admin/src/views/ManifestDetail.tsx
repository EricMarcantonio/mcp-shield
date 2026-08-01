/**
 * One manifest, across the seam.
 *
 * The approved baseline is left, what the upstream is advertising now is
 * right, and every difference crosses between them. Approving closes the seam:
 * the halves meet, the rule settles out of signal into slate, and the page
 * stops being a question.
 */

import { useEffect, useMemo, useState } from 'react';
import {
  useApprovedCapabilities,
  useManifest,
  useManifestApprovals,
  useManifestDiff,
  useServers,
} from '../api/queries';
import { groupChanges, toChangeItems, withheldCount } from '../api/derive';
import { Link, navigate } from '../lib/router';
import { formatTimestamp, pluralise, shortHash } from '../lib/format';
import { ChangeSection, Seam } from '../components/Seam';
import type { SeamPhase } from '../components/Seam';
import {
  Count,
  EmptyState,
  Hash,
  Loading,
  PageHeading,
  StateChip,
} from '../components/primitives';
import { Failure } from '../components/Failure';
import { DecisionPanel } from './DecisionPanel';
import { DecisionRecord } from './DecisionRecord';

export function ManifestDetailView({ id }: { id: number }) {
  const manifest = useManifest(id);
  const diff = useManifestDiff(id);
  const servers = useServers();
  const approvals = useManifestApprovals(id);

  const [phase, setPhase] = useState<SeamPhase>('open');

  const ledger = useMemo(
    () => servers.data?.ledgers.find((l) => l.server.name === manifest.data?.server) ?? null,
    [servers.data, manifest.data?.server],
  );
  const baseline = ledger?.approved ?? null;
  const capabilities = useApprovedCapabilities(ledger);

  const items = useMemo(() => toChangeItems(diff.data ?? null), [diff.data]);
  const groups = useMemo(() => groupChanges(items), [items]);
  const held = withheldCount(items);

  // The seam closes, then the page moves on. If motion is off the animation is
  // effectively instant, so the same timer still reads correctly.
  useEffect(() => {
    if (phase !== 'closing') return;
    const timer = window.setTimeout(() => setPhase('closed'), 700);
    return () => window.clearTimeout(timer);
  }, [phase]);

  if (manifest.error) return <Failure error={manifest.error} retry={() => void manifest.refetch()} />;
  if (manifest.isPending || !manifest.data) return <Loading label="Reading the manifest…" />;

  const record = manifest.data;
  const live = record.state === 'PENDING';
  const isBaseline = baseline?.id === record.id;

  return (
    <>
      <PageHeading
        eyebrow={
          <>
            <Link to="/servers" className="underline decoration-rule underline-offset-4">
              {record.server}
            </Link>{' '}
            · manifest
          </>
        }
        title={shortHash(record.hash)}
        lede={
          <>
            <span className="block break-all font-mono text-[0.75rem] leading-relaxed text-slate/60">
              {record.hash}
            </span>
            <span className="mt-2 block">
              The SHA-256 of this server's canonicalised capability set. It is the manifest's
              identity: what the gateway logs, what the diff below is taken against, and what an
              auditor would compare.
            </span>
          </>
        }
        aside={
          <div className="flex flex-col items-start gap-3 sm:items-end">
            <StateChip state={record.state} />
            <span className="font-display text-micro uppercase tracking-[0.12em] text-slate/50">
              recorded {formatTimestamp(record.created_at)}
            </span>
            {live && held > 0 && <Count value={held} unit="withheld" tone="text-held" />}
          </div>
        }
      />

      {isBaseline && (
        <div className="mb-6">
          <EmptyState title="This is the approved baseline">
            <p>
              Everything in this manifest is admitted right now. Anything an upstream advertises
              that does not match it byte for byte is withheld until you decide on it.
            </p>
          </EmptyState>
        </div>
      )}

      <div className="space-y-6">
        <Seam
          phase={phase}
          live={live}
          left={
            baseline ? (
              <BaselineSummary
                hash={baseline.hash}
                sameManifest={isBaseline}
                toolCount={
                  capabilities.data?.reconstruction.sound
                    ? capabilities.data.reconstruction.set.tools.length
                    : null
                }
              />
            ) : (
              <NoBaseline />
            )
          }
          right={
            <div className="space-y-1">
              <Hash value={record.hash} className="text-base" />
              <p className="text-sm text-slate/70">
                {items.length} {pluralise(items.length, 'difference')} from the baseline
                {held > 0 && (
                  <>
                    ,{' '}
                    {live ? (
                      <span className="text-held">{held} withheld</span>
                    ) : record.state === 'REJECTED' ? (
                      <span className="text-held">{held} still refused</span>
                    ) : (
                      <span>{held} admitted</span>
                    )}
                  </>
                )}
                .
              </p>
            </div>
          }
        >
          {diff.isPending ? (
            <Loading label="Reading the diff…" />
          ) : groups.length === 0 ? (
            <div className="px-5 py-6 text-sm text-slate/70">
              The gateway recorded no differences for this manifest.
            </div>
          ) : (
            groups.map((group) => (
              <ChangeSection
                key={group.kind}
                group={group}
                closing={phase !== 'open'}
                state={record.state}
              />
            ))
          )}
        </Seam>

        {live && (
          <DecisionPanel
            manifestId={record.id}
            server={record.server}
            hash={record.hash}
            withheld={held}
            onSettled={() => {
              setPhase('closing');
              window.setTimeout(() => navigate('/'), 900);
            }}
          />
        )}

        <DecisionRecord manifestId={record.id} approvals={approvals.data ?? null} state={record.state} />
      </div>
    </>
  );
}

function BaselineSummary({
  hash,
  sameManifest,
  toolCount,
}: {
  hash: string;
  sameManifest: boolean;
  toolCount: number | null;
}) {
  return (
    <div className="space-y-1">
      <Hash value={hash} className="text-base" />
      <p className="text-sm text-slate/70">
        {sameManifest
          ? 'This manifest is the baseline.'
          : toolCount !== null
            ? `${toolCount} ${pluralise(toolCount, 'tool')} admitted.`
            : 'The capability set approved for this server.'}
      </p>
    </div>
  );
}

function NoBaseline() {
  return (
    <div className="space-y-1">
      <span className="font-display text-base font-semibold text-held">No approved baseline</span>
      <p className="text-sm text-slate/70">
        Nothing has ever been approved for this server, so everything below is new and everything is
        withheld. Fail closed.
      </p>
    </div>
  );
}
