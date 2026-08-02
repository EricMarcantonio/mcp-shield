/**
 * What is waiting on me.
 *
 * One card per manifest the gateway is holding. The card answers the whole
 * question on its own — what changed, what it leaves alone, and the two
 * buttons that settle it — so reviewing a queue is not a tour of the app.
 *
 * The headline is a description of the diff and nothing more. This product
 * carries no risk classification by decision (D8): a label ranking a
 * capability as dangerous would be a judgement the tool cannot make, and an
 * approver who starts trusting it is worse off than one reading the diff.
 */

import { useMemo, useState } from 'react';
import { usePending, useApprovedCapabilities, useManifestDiff, useServers } from '../api/queries';
import {
  byAge,
  changedCapabilities,
  summariseChanges,
  unchangedCapabilities,
  withheldCount,
  EMPTY_CAPABILITY_SET,
  STATUS_COPY,
} from '../api/derive';
import type { Capability, ServerLedger } from '../api/derive';
import type { DecisionKind } from '../api/queries';
import type { PendingManifest } from '../api/types';
import { formatAge, formatTimestamp, pluralise, shortHash } from '../lib/format';
import {
  CapabilityTag,
  EmptyState,
  Loading,
  PageHeading,
  StatusTag,
  Tag,
  Unavailable,
} from '../components/primitives';
import { Failure } from '../components/Failure';
import { DecisionDialog } from '../components/DecisionDialog';

export function PendingView() {
  const pending = usePending();
  const servers = useServers();

  const queue = useMemo(() => (pending.data ? [...pending.data].sort(byAge) : []), [pending.data]);

  return (
    <>
      <PageHeading title="Pending approvals" note="New or changed tools waiting for review" />

      {pending.error ? (
        <Failure error={pending.error} retry={() => void pending.refetch()} />
      ) : pending.isPending ? (
        <Loading label="Reading the queue…" />
      ) : queue.length === 0 ? (
        <EmptyState title="All caught up">
          <p>
            No manifests are waiting for review. New or changed capabilities will show up here.
          </p>
        </EmptyState>
      ) : (
        <ul className="flex flex-col gap-[var(--space-4)]">
          {queue.map((manifest) => (
            <li key={manifest.id}>
              <PendingCard
                manifest={manifest}
                ledger={servers.data?.ledgers.find((l) => l.server.name === manifest.server) ?? null}
              />
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

function PendingCard({
  manifest,
  ledger,
}: {
  manifest: PendingManifest;
  ledger: ServerLedger | null;
}) {
  const diff = useManifestDiff(manifest.id);
  const baseline = useApprovedCapabilities(ledger);

  const [expanded, setExpanded] = useState(false);
  const [showUnchanged, setShowUnchanged] = useState(false);
  const [decision, setDecision] = useState<DecisionKind | null>(null);

  const changed = useMemo(() => changedCapabilities(diff.data ?? null), [diff.data]);

  const reconstruction = baseline.data?.reconstruction;
  const baselineSet = reconstruction?.sound ? reconstruction.set : EMPTY_CAPABILITY_SET;
  const unchanged = useMemo(
    () => unchangedCapabilities(baselineSet, changed),
    [baselineSet, changed],
  );

  const hasBaseline = ledger?.approved != null;
  const withheld = withheldCount(changed);
  const detailsId = `manifest-${manifest.id}-details`;

  return (
    <article className="card elev-sm gap-[var(--space-4)] p-[var(--space-6)]">
      <div className="flex flex-col gap-[var(--space-4)] sm:flex-row sm:items-start sm:justify-between">
        <div>
          <p className="card-kicker m-0">
            {manifest.server} · <span className="mono normal-case">{shortHash(manifest.hash)}</span>
          </p>
          <h2 className="card-title m-0">{summariseChanges(changed, hasBaseline)}</h2>
          <p className="text-muted m-0 mt-[4px] text-[13px]">
            Recorded <time dateTime={manifest.created_at}>{formatTimestamp(manifest.created_at)}</time>{' '}
            · waiting {formatAge(manifest.created_at)}
          </p>
        </div>

        <div className="flex shrink-0 flex-wrap gap-[var(--space-2)]">
          <button
            type="button"
            className="btn btn-ghost"
            aria-expanded={expanded}
            aria-controls={detailsId}
            onClick={() => setExpanded((open) => !open)}
          >
            {expanded ? 'Hide details' : 'Details'}
          </button>
          <button type="button" className="btn btn-secondary" onClick={() => setDecision('reject')}>
            Reject
          </button>
          <button type="button" className="btn btn-primary" onClick={() => setDecision('approve')}>
            Approve
          </button>
        </div>
      </div>

      {diff.isPending ? (
        <Loading label="Reading the diff…" />
      ) : (
        <div className="flex flex-wrap items-center gap-[var(--space-2)]">
          {changed.map((capability) => (
            <CapabilityTag key={identityOf(capability)} capability={capability} />
          ))}
          {unchanged.length > 0 && <Tag tone="neutral">+{unchanged.length} unchanged</Tag>}
        </div>
      )}

      {expanded && (
        <div
          id={detailsId}
          className="flex flex-col gap-[var(--space-2)] border-t pt-[var(--space-3)]"
        >
          {changed.map((capability) => (
            <CapabilityRow key={identityOf(capability)} capability={capability} />
          ))}

          {hasBaseline && !reconstruction?.sound && (
            <Unavailable>
              The capabilities this manifest leaves unchanged cannot be listed: this gateway serves
              no route for a manifest&rsquo;s contents, and the set could not be replayed from the
              stored diffs
              {reconstruction ? ` (${reconstruction.reason})` : ''}.
            </Unavailable>
          )}

          {unchanged.length > 0 && (
            <>
              <button
                type="button"
                className="btn btn-ghost w-fit"
                aria-expanded={showUnchanged}
                onClick={() => setShowUnchanged((open) => !open)}
              >
                {showUnchanged ? 'Hide' : 'Show'} {unchanged.length} unchanged{' '}
                {collectiveNoun(unchanged)}
              </button>
              {showUnchanged &&
                unchanged.map((capability) => (
                  <CapabilityRow key={identityOf(capability)} capability={capability} />
                ))}
            </>
          )}
        </div>
      )}

      {decision && (
        <DecisionDialog
          kind={decision}
          target={{
            id: manifest.id,
            server: manifest.server,
            hash: manifest.hash,
            withheld,
          }}
          onDismiss={() => setDecision(null)}
        />
      )}
    </article>
  );
}

/**
 * One capability inside an expanded manifest.
 *
 * The design puts a description and the tool's parameters behind this
 * disclosure. No route serves a manifest's canonical JSON, so neither exists
 * to show; what is here instead is what the gateway does know — the kind of
 * change, whether the gate is holding it, and a plain statement of the gap.
 */
function CapabilityRow({ capability }: { capability: Capability }) {
  const [open, setOpen] = useState(false);
  const bodyId = `capability-${identityOf(capability)}`;

  return (
    <div className="capability-row">
      <button
        type="button"
        className="flex w-full items-center gap-[var(--space-3)] text-left"
        aria-expanded={open}
        aria-controls={bodyId}
        onClick={() => setOpen((wasOpen) => !wasOpen)}
      >
        <StatusTag status={capability.status} />
        <span className="font-[family-name:var(--font-heading)] text-[15px]">
          {capability.identity}
        </span>
        <span className="ml-auto shrink-0 text-[13px] text-[color:var(--color-accent-700)]">
          {open ? 'Hide' : 'Details'}
        </span>
      </button>

      {open && (
        <div
          id={bodyId}
          className="text-muted mt-[var(--space-2)] flex flex-col gap-[var(--space-1)] text-[13px]"
        >
          <p className="m-0">
            {capitalise(capability.domain)} · {STATUS_COPY[capability.status].note}
          </p>
          <p className="m-0">
            {capability.withheld
              ? 'Withheld from every client until this manifest is decided.'
              : 'Not withheld by this manifest.'}
          </p>
          <Unavailable>
            No route serves a manifest&rsquo;s contents, so the description and input schema{' '}
            {capability.identity} advertises are not shown here.
          </Unavailable>
        </div>
      )}
    </div>
  );
}

function identityOf(capability: Capability): string {
  return `${capability.domain}:${capability.identity}`;
}

/** "tools" when they all are, "capabilities" when the set is mixed. */
function collectiveNoun(capabilities: Capability[]): string {
  const first = capabilities[0];
  if (!first) return 'capabilities';
  const uniform = capabilities.every((capability) => capability.domain === first.domain);
  return uniform
    ? pluralise(capabilities.length, first.domain)
    : pluralise(capabilities.length, 'capability', 'capabilities');
}

function capitalise(word: string): string {
  return word.charAt(0).toUpperCase() + word.slice(1);
}
