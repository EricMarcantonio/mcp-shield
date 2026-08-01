/**
 * What is approved right now.
 *
 * One card per upstream server, and the first thing on it is the hash of the
 * manifest currently admitted — because that hash *is* the approved schema
 * version in this product. Underneath it, the capability set that hash covers.
 *
 * A server with no approved baseline is the important case and gets the most
 * space, not the least: everything it advertises is withheld, `tools/list`
 * comes back empty and every `tools/call` is refused. That is the gate working
 * from a cold start, so it is drawn in `held` amber and explained, never in
 * red and never as an error.
 */

import { useApprovedCapabilities, useServers } from '../api/queries';
import { baselineChain } from '../api/derive';
import type { ServerLedger } from '../api/derive';
import { Link } from '../lib/router';
import { formatTimestamp, pluralise } from '../lib/format';
import {
  EmptyState,
  Hash,
  HeldMark,
  Identity,
  Loading,
  PageHeading,
  StateChip,
} from '../components/primitives';
import { Failure } from '../components/Failure';

export function ServersView() {
  const { data, isPending, error, refetch } = useServers();

  const withBaseline = data?.ledgers.filter((l) => l.approved).length ?? 0;
  const total = data?.ledgers.length ?? 0;

  return (
    <>
      <PageHeading
        eyebrow="Currently trusted"
        title="Servers"
        lede="For each upstream server: the manifest hash that is approved, and the capabilities that hash admits. Anything an upstream advertises that does not match this baseline byte for byte is withheld."
        aside={
          total > 0 && (
            <p className="font-sans text-sm text-slate/70">
              {withBaseline} of {total} {pluralise(total, 'server')} {withBaseline === 1 ? 'has' : 'have'}{' '}
              an approved baseline
            </p>
          )
        }
      />

      {error ? (
        <Failure error={error} retry={refetch} />
      ) : isPending || !data ? (
        <Loading label="Reading the servers…" />
      ) : data.ledgers.length === 0 ? (
        <EmptyState title="No servers registered">
          <p>
            A server appears here the first time a client connects to it through the gateway.
            Register upstream servers in <span className="font-mono text-[0.8125rem]">config/servers.json</span>{' '}
            and connect once.
          </p>
        </EmptyState>
      ) : (
        <div className="space-y-6">
          {data.ledgers.map((ledger) => (
            <ServerCard key={ledger.server.id} ledger={ledger} />
          ))}
        </div>
      )}
    </>
  );
}

function ServerCard({ ledger }: { ledger: ServerLedger }) {
  const { server, approved, pending, history } = ledger;

  return (
    <article className="card overflow-hidden">
      <header className="flex flex-col gap-3 border-b border-rule px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 className="font-display text-xl font-semibold text-ink">{server.name}</h2>
          <span className="font-display text-micro uppercase tracking-[0.12em] text-slate/50">
            first seen {formatTimestamp(server.createdAt)}
          </span>
        </div>
        {pending.length > 0 && (
          <Link
            to={`/manifests/${pending[0]?.id}`}
            className="inline-flex items-center gap-2 self-start rounded-sm border border-signal/40 bg-signal/8 px-3 py-1.5 text-sm text-signal hover:bg-signal/12"
          >
            <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-signal" />
            {pending.length} pending {pluralise(pending.length, 'manifest')} — review →
          </Link>
        )}
      </header>

      {approved ? <ApprovedBaseline ledger={ledger} /> : <NoBaseline hasPending={pending.length > 0} pendingId={pending[0]?.id} />}

      {history.length > 0 && <ManifestHistory ledger={ledger} />}
    </article>
  );
}

function ApprovedBaseline({ ledger }: { ledger: ServerLedger }) {
  const approved = ledger.approved!;
  const capabilities = useApprovedCapabilities(ledger);
  const chain = baselineChain(ledger);

  return (
    <div className="px-5 py-5">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <p className="label">Approved manifest</p>
        <Link to={`/manifests/${approved.id}`} className="underline decoration-rule underline-offset-4">
          <Hash value={approved.hash} className="text-base" />
        </Link>
        <span className="font-display text-micro uppercase tracking-[0.12em] text-slate/50">
          recorded {formatTimestamp(approved.created_at)}
        </span>
      </div>

      <p className="mt-1.5 max-w-2xl text-sm text-slate/70">
        Approved by whom and when is stored by the gateway but not served on any JSON route —{' '}
        <span className="font-mono text-[0.8125rem]">
          GET /api/manifests/{approved.id}/approvals
        </span>{' '}
        would fill this in.
      </p>

      <div className="mt-5">
        {capabilities.isPending ? (
          <Loading label="Reading the capability set…" />
        ) : capabilities.data?.reconstruction.sound ? (
          <CapabilityTable
            set={capabilities.data.reconstruction.set}
            source={capabilities.data.source}
            chainLength={chain.length}
          />
        ) : (
          <EmptyState tone="held" title="The capability set cannot be shown">
            <p>
              This gateway serves no route for a manifest's contents, and the set could not be
              reconstructed from the stored diffs
              {capabilities.data?.reconstruction.sound === false && (
                <>
                  {' '}
                  (<span className="font-mono text-[0.8125rem]">
                    {capabilities.data.reconstruction.reason}
                  </span>)
                </>
              )}
              .
            </p>
            <p>
              The approved hash above is still authoritative. Add{' '}
              <span className="font-mono text-[0.8125rem]">
                GET /api/manifests/{approved.id}/contents
              </span>{' '}
              to show the tools it covers.
            </p>
          </EmptyState>
        )}
      </div>
    </div>
  );
}

function CapabilityTable({
  set,
  source,
  chainLength,
}: {
  set: { tools: string[]; prompts: string[]; resources: string[] };
  source: 'served' | 'replayed';
  chainLength: number;
}) {
  const sections: [string, string[]][] = [
    ['Tools', set.tools],
    ['Prompts', set.prompts],
    ['Resources', set.resources],
  ];
  const populated = sections.filter(([, names]) => names.length > 0);

  return (
    <div>
      <p className="label">Admitted capabilities</p>

      {populated.length === 0 ? (
        <p className="mt-3 text-sm text-slate/70">
          The approved manifest advertises nothing. Every call is refused.
        </p>
      ) : (
        <div className="mt-3 space-y-4">
          {populated.map(([heading, names]) => (
            <div key={heading}>
              <p className="font-display text-micro uppercase tracking-[0.12em] text-slate/60">
                {heading} · {names.length}
              </p>
              <ul className="mt-2 flex flex-wrap gap-1.5">
                {names.map((name) => (
                  <li
                    key={name}
                    className="rounded-sm border border-rule bg-paper px-2 py-1 leading-none"
                  >
                    <Identity>{name}</Identity>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}

      {source === 'replayed' && (
        <p className="mt-4 max-w-3xl border-t border-rule pt-3 text-xs leading-relaxed text-slate/55">
          Reconstructed from the {chainLength} stored {pluralise(chainLength, 'diff')} along this
          server's approval chain, and checked against each of them. The gateway serves no route for
          manifest contents, so descriptions and input schemas are not shown here.
        </p>
      )}
    </div>
  );
}

function NoBaseline({ hasPending, pendingId }: { hasPending: boolean; pendingId?: number }) {
  return (
    <div className="px-5 py-5">
      <EmptyState
        tone="held"
        title="No approved manifest"
        action={
          hasPending && pendingId !== undefined ? (
            <Link
              to={`/manifests/${pendingId}`}
              className="inline-flex items-center rounded-sm bg-slate px-4 py-2 text-sm font-medium text-paper hover:bg-ink"
            >
              Review the pending manifest →
            </Link>
          ) : undefined
        }
      >
        <p className="flex items-center gap-2">
          <HeldMark label="Everything withheld" />
        </p>
        <p>
          Nothing has been approved for this server, so its safe set is empty.{' '}
          <span className="font-mono text-[0.8125rem]">tools/list</span> returns no tools — an empty
          array, not an error — and every <span className="font-mono text-[0.8125rem]">tools/call</span>{' '}
          is refused. Methods the gateway does not parse item by item are blocked outright.
        </p>
        <p>
          This is the fail-closed starting state, not a fault. It ends when you approve a first
          manifest and give the server a baseline to be diffed against.
        </p>
      </EmptyState>
    </div>
  );
}

function ManifestHistory({ ledger }: { ledger: ServerLedger }) {
  return (
    <details className="group border-t border-rule">
      <summary className="cursor-pointer list-none px-5 py-3 text-sm text-slate/70 hover:text-ink">
        <span className="font-display text-micro uppercase tracking-[0.12em]">
          {ledger.history.length} {pluralise(ledger.history.length, 'manifest')} recorded
        </span>
        <span className="ml-2 text-slate/45 group-open:hidden">show</span>
        <span className="ml-2 hidden text-slate/45 group-open:inline">hide</span>
      </summary>
      <ul className="divide-y divide-rule border-t border-rule">
        {ledger.history.map((manifest) => (
          <li key={manifest.id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-2.5">
            <Link
              to={`/manifests/${manifest.id}`}
              className="underline decoration-rule underline-offset-4"
            >
              <Hash value={manifest.hash} />
            </Link>
            <StateChip state={manifest.state} />
            <span className="ml-auto text-sm text-slate/55">
              {formatTimestamp(manifest.created_at)}
            </span>
          </li>
        ))}
      </ul>
    </details>
  );
}
