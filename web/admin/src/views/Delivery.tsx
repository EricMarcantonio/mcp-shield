/**
 * What is broken.
 *
 * Two failures, and they are both real failures — so this is the one place in
 * the console that is allowed to be red.
 *
 *  1. The gateway not answering. Every other screen is stale the moment this
 *     is true.
 *  2. A notification the dispatcher gave up on. The gate fails closed, so a
 *     withheld capability is invisible until somebody looks — a notification
 *     that never landed is exactly the silence the notification feature exists
 *     to remove.
 */

import { useFailedNotifications, useHealth } from '../api/queries';
import type { FailedNotification } from '../api/types';
import { Link } from '../lib/router';
import { formatTimestamp, pluralise } from '../lib/format';
import { EmptyState, Loading, PageHeading } from '../components/primitives';
import { Failure } from '../components/Failure';

export function DeliveryView() {
  const health = useHealth();
  const failed = useFailedNotifications();

  return (
    <>
      <PageHeading
        eyebrow="Operational state"
        title="Delivery"
        lede="Whether the gateway is answering, and whether anyone was actually told about the manifests it is holding."
      />

      <div className="space-y-8">
        <section>
          <h2 className="mb-3 font-display text-lg font-semibold text-ink">Gateway</h2>
          {health.isPending ? (
            <Loading label="Checking…" />
          ) : health.isError ? (
            <Failure error={health.error} retry={() => void health.refetch()} />
          ) : (
            <div className="card px-5 py-4">
              <p className="flex flex-wrap items-baseline gap-x-3">
                <span className="font-display text-micro uppercase tracking-[0.12em] text-slate/60">
                  /healthz
                </span>
                <span className="font-mono text-[0.8125rem] text-ink">
                  {health.data?.status ?? 'ok'}
                </span>
              </p>
              <p className="mt-2 max-w-2xl text-sm text-slate/70">
                The approval API is answering. This says nothing about the upstream MCP servers
                themselves — the gateway re-fetches their capabilities on every call, and a dead
                upstream shows up as a failed request to the client, not here.
              </p>
            </div>
          )}
        </section>

        <section>
          <h2 className="mb-3 font-display text-lg font-semibold text-ink">Failed notifications</h2>
          {failed.error ? (
            <Failure error={failed.error} retry={() => void failed.refetch()} />
          ) : failed.isPending ? (
            <Loading label="Reading the outbox…" />
          ) : failed.data === null ? (
            <EmptyState title="Notifications are not configured">
              <p>
                No webhook targets are set, so there is nothing to deliver and nothing to fail. The
                gateway reports this surface as absent rather than showing an empty list, because an
                empty list would read as "everything was delivered".
              </p>
              <p>
                Point <span className="font-mono text-[0.8125rem]">NOTIFY_CONFIG_PATH</span> at a
                config with webhook targets to turn this on. Until then a withheld capability is
                only visible to somebody who opens this console.
              </p>
            </EmptyState>
          ) : failed.data.length === 0 ? (
            <EmptyState title="Everything was delivered">
              <p>
                No event has exhausted its retries. Delivery is at-least-once with persisted
                backoff, so an event that is still retrying does not appear here.
              </p>
            </EmptyState>
          ) : (
            <>
              <p className="mb-3 text-sm text-slate/70">
                {failed.data.length} {pluralise(failed.data.length, 'event')} reached the retry
                limit and will not be sent again. Whoever these were meant to reach does not know
                the gateway is holding something.
              </p>
              <ol className="card divide-y divide-rule">
                {failed.data.map((event) => (
                  <li key={event.event_id}>
                    <FailedRow event={event} />
                  </li>
                ))}
              </ol>
            </>
          )}
        </section>
      </div>
    </>
  );
}

function FailedRow({ event }: { event: FailedNotification }) {
  return (
    <div className="relative px-5 py-4">
      <span aria-hidden className="absolute inset-y-0 left-0 w-[3px] bg-fault" />

      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="font-mono text-[0.8125rem] text-ink">{event.event}</span>
        <span className="font-display text-base font-semibold text-ink">{event.server}</span>
        <Link
          to={`/manifests/${event.manifest_id}`}
          className="text-sm text-slate/70 underline decoration-rule underline-offset-4 hover:text-ink"
        >
          manifest {event.manifest_id}
        </Link>
        <span className="ml-auto text-sm text-slate/55">{formatTimestamp(event.created_at)}</span>
      </div>

      <p className="mt-2 break-words font-mono text-[0.8125rem] leading-relaxed text-fault">
        {event.last_error}
      </p>
      <p className="mt-1.5 text-xs text-slate/55">
        {event.attempts} {pluralise(event.attempts, 'attempt')}. The gateway names the target but
        never its URL — a webhook URL is a credential.
      </p>
    </div>
  );
}
