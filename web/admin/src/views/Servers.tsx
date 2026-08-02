/**
 * What is approved right now.
 *
 * One row per upstream server, and the column that matters is the manifest
 * hash: that hash *is* the approved schema version in this product. Anything
 * an upstream advertises that does not match it byte for byte is withheld.
 *
 * A server with no approved baseline is the important case, not an error. Its
 * safe set is empty, `tools/list` comes back empty and every `tools/call` is
 * refused — the gate working from a cold start.
 */

import { useApprovedCapabilities, useDecisions, useServers } from '../api/queries';
import { capabilityCount } from '../api/derive';
import type { Reconstruction, ServerLedger } from '../api/derive';
import type { ApprovalRecord } from '../api/types';
import { formatTimestamp, pluralise } from '../lib/format';
import {
  EmptyState,
  Hash,
  Loading,
  PageHeading,
  TableFrame,
  Tag,
} from '../components/primitives';
import { Failure } from '../components/Failure';

export function ServersView() {
  const servers = useServers();
  const decisions = useDecisions();

  return (
    <>
      <PageHeading title="Servers" note="The approved baseline behind each upstream server" />

      {servers.error ? (
        <Failure error={servers.error} retry={servers.refetch} />
      ) : servers.isPending || !servers.data ? (
        <Loading label="Reading the servers…" />
      ) : servers.data.ledgers.length === 0 ? (
        <EmptyState title="No servers configured">
          <p>
            Add an entry to <code>config/servers.json</code> and connect a client to see it here.
          </p>
        </EmptyState>
      ) : (
        <TableFrame>
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Server</th>
                <th scope="col">Status</th>
                <th scope="col">Capabilities</th>
                <th scope="col">Manifest hash</th>
                <th scope="col">Last action</th>
              </tr>
            </thead>
            <tbody>
              {servers.data.ledgers.map((ledger) => (
                <ServerRow
                  key={ledger.server.id}
                  ledger={ledger}
                  lastDecision={latestFor(ledger.server.name, decisions.data)}
                />
              ))}
            </tbody>
          </table>
        </TableFrame>
      )}
    </>
  );
}

function ServerRow({
  ledger,
  lastDecision,
}: {
  ledger: ServerLedger;
  lastDecision: ApprovalRecord | null;
}) {
  const capabilities = useApprovedCapabilities(ledger);
  const { server, approved, pending } = ledger;

  return (
    <tr>
      <td className="font-[family-name:var(--font-heading)]">{server.name}</td>
      <td>
        <span className="flex flex-wrap gap-[var(--space-1)]">
          {approved ? (
            <Tag tone="accent-2">Baseline approved</Tag>
          ) : (
            <Tag tone="outline">No baseline — all withheld</Tag>
          )}
          {pending.length > 0 && (
            <Tag tone="accent">
              {pending.length} pending {pluralise(pending.length, 'manifest')}
            </Tag>
          )}
        </span>
      </td>
      <td>
        <AdmittedCount
          hasBaseline={approved !== null}
          reconstruction={capabilities.data?.reconstruction}
        />
      </td>
      <td>{approved ? <Hash value={approved.hash} /> : <Absent />}</td>
      <td className="text-muted text-[13px]">
        {lastDecision ? (
          <>
            {lastDecision.decision === 'APPROVED' ? 'Approved' : 'Rejected'} by{' '}
            <span className="mono">{lastDecision.username}</span>
            <br />
            {formatTimestamp(lastDecision.decided_at)}
          </>
        ) : (
          'No decision recorded'
        )}
      </td>
    </tr>
  );
}

/**
 * How many capabilities the approved manifest admits.
 *
 * Zero and unknown are different answers and are never conflated: zero means
 * the gate is admitting nothing, which is exactly what a server with no
 * baseline does.
 */
function AdmittedCount({
  hasBaseline,
  reconstruction,
}: {
  hasBaseline: boolean;
  reconstruction: Reconstruction | undefined;
}) {
  if (!hasBaseline) return <span className="tabular-nums">0</span>;
  if (!reconstruction) return <span className="text-muted text-[13px]">reading…</span>;
  if (!reconstruction.sound) {
    return (
      <span className="text-muted text-[13px]" title={reconstruction.reason}>
        unknown
      </span>
    );
  }
  return <span className="tabular-nums">{capabilityCount(reconstruction.set)}</span>;
}

function Absent() {
  return (
    <span className="text-muted text-[13px]" aria-label="none">
      —
    </span>
  );
}

function latestFor(server: string, decisions: ApprovalRecord[] | null | undefined) {
  return (decisions ?? []).find((record) => record.server === server) ?? null;
}
