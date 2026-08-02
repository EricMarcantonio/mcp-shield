/**
 * What was decided.
 *
 * One row per decision, and a decision in this system approves or rejects a
 * *manifest* — the whole fingerprinted capability set — never an individual
 * tool. Splitting a decision into one row per tool would read as though each
 * had been judged separately, which would misrepresent the audit record this
 * product exists to produce. So the "capabilities" column names what the
 * decision covered, and the manifest hash beneath it is what was signed off.
 */

import { useQueries } from '@tanstack/react-query';
import { getManifestDiff } from '../api/client';
import { keys, useDecisions } from '../api/queries';
import { changedCapabilities } from '../api/derive';
import type { Capability } from '../api/derive';
import type { ApprovalRecord } from '../api/types';
import { formatTimestamp, shortHash } from '../lib/format';
import { EmptyState, Loading, PageHeading, TableFrame, Tag } from '../components/primitives';
import { Failure } from '../components/Failure';

const NAMES_SHOWN = 3;

export function HistoryView() {
  const decisions = useDecisions();
  const records = decisions.data ?? [];

  // What each decision covered lives on the manifest's stored diff, one read
  // per row and cached for a minute — the ledger does not move on its own.
  const diffs = useQueries({
    queries: records.map((record) => ({
      queryKey: keys.diff(record.manifest_id),
      queryFn: () => getManifestDiff(record.manifest_id),
      staleTime: 60_000,
    })),
  });

  return (
    <>
      <PageHeading
        title="Approval history"
        note="One row per decision. A decision covers a whole manifest, not a single tool."
      />

      {decisions.error ? (
        <Failure error={decisions.error} retry={() => void decisions.refetch()} />
      ) : decisions.isPending ? (
        <Loading label="Reading the record…" />
      ) : decisions.data === null ? (
        <EmptyState title="This gateway does not serve the decision log">
          <p>
            The gateway records every decision in an append-only table, but this build serves no{' '}
            <code>GET /api/decisions</code> route, so the record cannot be read from here.
          </p>
        </EmptyState>
      ) : records.length === 0 ? (
        <EmptyState title="No decisions yet">
          <p>Approved and rejected manifests will be logged here.</p>
        </EmptyState>
      ) : (
        <TableFrame>
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Server</th>
                <th scope="col">Capabilities</th>
                <th scope="col">Decision</th>
                <th scope="col">Reason</th>
                <th scope="col" className="whitespace-nowrap">
                  Recorded as
                </th>
                <th scope="col">When</th>
              </tr>
            </thead>
            <tbody>
              {records.map((record, position) => (
                <DecisionRow
                  key={record.id}
                  record={record}
                  covered={changedCapabilities(diffs[position]?.data ?? null)}
                />
              ))}
            </tbody>
          </table>
        </TableFrame>
      )}
    </>
  );
}

function DecisionRow({ record, covered }: { record: ApprovalRecord; covered: Capability[] }) {
  const approved = record.decision === 'APPROVED';

  return (
    <tr>
      <td>{record.server}</td>
      <td>
        <Covered capabilities={covered} />
        <span className="text-muted mono block text-[12px]">{shortHash(record.manifest_hash)}</span>
      </td>
      <td>
        <Tag tone={approved ? 'accent-2' : 'outline'}>{approved ? 'Approved' : 'Rejected'}</Tag>
      </td>
      <td className="text-muted max-w-[32ch] text-[13px]">
        {record.reason || <span className="italic">No reason was recorded.</span>}
      </td>
      <td>
        <span className="mono text-[13px]">{record.username}</span>
      </td>
      <td className="text-muted text-[13px]">
        <time dateTime={record.decided_at}>{formatTimestamp(record.decided_at)}</time>
      </td>
    </tr>
  );
}

function Covered({ capabilities }: { capabilities: Capability[] }) {
  if (capabilities.length === 0) {
    return <span className="text-muted text-[13px]">manifest as a whole</span>;
  }

  const shown = capabilities.slice(0, NAMES_SHOWN).map((capability) => capability.identity);
  const rest = capabilities.length - shown.length;

  return (
    <span className="mono text-[13px]">
      {shown.join(', ')}
      {rest > 0 && <span className="text-muted"> +{rest} more</span>}
    </span>
  );
}
