/**
 * What was decided about this manifest, and by whom.
 *
 * The gateway records every decision in an append-only `approvals` table and
 * its server-rendered dashboard reads it, but no JSON route exposes it yet.
 * When one lands this section fills in on its own; until then it says which
 * route it is waiting on rather than showing an empty list that would read as
 * "nobody has decided".
 */

import type { ApprovalRecord, ManifestState } from '../api/types';
import { formatTimestamp } from '../lib/format';
import { EmptyState } from '../components/primitives';

export function DecisionRecord({
  manifestId,
  approvals,
  state,
}: {
  manifestId: number;
  approvals: ApprovalRecord[] | null;
  state: ManifestState;
}) {
  if (approvals === null) {
    return (
      <section>
        <h2 className="mb-3 font-display text-lg font-semibold text-ink">Decision record</h2>
        <EmptyState tone="held" title="Attribution is not served by this gateway">
          <p>
            The gateway stores who decided this manifest, when, and why — the record is in its
            approvals table and its own dashboard renders it. There is no JSON route for it, so this
            console cannot show it.
          </p>
          <p>
            Add <span className="font-mono text-[0.8125rem]">GET /api/manifests/{manifestId}/approvals</span>{' '}
            and this section fills in with no further change here.
          </p>
        </EmptyState>
      </section>
    );
  }

  if (approvals.length === 0) {
    return (
      <section>
        <h2 className="mb-3 font-display text-lg font-semibold text-ink">Decision record</h2>
        <EmptyState title="No decision yet">
          <p>
            {state === 'PENDING'
              ? 'This manifest is still waiting. Everything it introduces stays withheld until somebody decides.'
              : 'The gateway holds no decision row for this manifest.'}
          </p>
        </EmptyState>
      </section>
    );
  }

  return (
    <section>
      <h2 className="mb-3 font-display text-lg font-semibold text-ink">Decision record</h2>
      <ol className="card divide-y divide-rule">
        {approvals.map((record) => (
          <li key={record.id} className="px-5 py-4">
            <DecisionLine record={record} />
          </li>
        ))}
      </ol>
    </section>
  );
}

export function DecisionLine({ record }: { record: ApprovalRecord }) {
  const approved = record.decision === 'APPROVED';

  return (
    <>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <span
          className={`font-display text-micro uppercase tracking-[0.12em] ${
            approved ? 'text-slate' : 'text-fault'
          }`}
        >
          {approved ? 'Approved' : 'Rejected'}
        </span>
        <span className="text-sm text-slate/70">by</span>
        <span className="font-mono text-[0.8125rem] text-ink">{record.username}</span>
        <span className="text-sm text-slate/60">{formatTimestamp(record.decided_at)}</span>
      </div>
      {record.reason ? (
        <p className="mt-1.5 max-w-2xl text-sm leading-relaxed text-slate/85">{record.reason}</p>
      ) : (
        <p className="mt-1.5 text-sm italic text-slate/50">No reason was recorded.</p>
      )}
      <p className="mt-2 text-xs text-slate/50">
        Recorded, not verified — the gateway stores the name the caller supplied.
      </p>
    </>
  );
}
