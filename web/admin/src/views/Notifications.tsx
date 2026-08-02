/**
 * Whether anyone was actually told.
 *
 * The gate fails closed, so a withheld capability is invisible until somebody
 * looks. A notification that never landed is exactly the silence the feature
 * exists to remove, which is why an exhausted delivery stays queryable here
 * instead of disappearing.
 *
 * No webhook URL appears on this screen. A Slack webhook URL is itself a
 * capability-bearing credential — anyone holding it can post as that
 * integration — and the gateway deliberately redacts even the host out of its
 * connection errors. A console that pasted one back onto the page would undo
 * that in one line.
 */

import { useFailedNotifications } from '../api/queries';
import type { FailedNotification } from '../api/types';
import { formatTimestamp, pluralise } from '../lib/format';
import {
  EmptyState,
  Loading,
  PageHeading,
  SectionKicker,
  TableFrame,
  Tag,
} from '../components/primitives';
import { Failure } from '../components/Failure';

export function NotificationsView() {
  const failed = useFailedNotifications();
  const configured = failed.data !== null;

  return (
    <>
      <PageHeading title="Notifications" />

      {failed.error ? (
        <Failure error={failed.error} retry={() => void failed.refetch()} />
      ) : failed.isPending ? (
        <Loading label="Reading the outbox…" />
      ) : (
        <>
          <WebhookCard configured={configured} />
          <FailedDeliveries events={failed.data} />
        </>
      )}
    </>
  );
}

/**
 * What the console can honestly say about the targets: whether any exist.
 *
 * The gateway serves no route describing them, so their configured names and
 * formats are not available to show — and their URLs would not be shown even
 * if they were.
 */
function WebhookCard({ configured }: { configured: boolean }) {
  return (
    <section className="card elev-sm gap-[var(--space-3)] p-[var(--space-6)]">
      <h2 className="card-kicker m-0">Webhook</h2>

      <div className="flex flex-wrap items-center gap-[var(--space-3)]">
        <Tag tone={configured ? 'accent-2' : 'neutral'}>
          {configured ? 'Configured' : 'Not configured'}
        </Tag>
        <p className="text-muted m-0 text-[13px]">
          {configured
            ? 'This gateway has at least one delivery target.'
            : 'This gateway has no delivery targets, so nothing is sent and nothing can fail.'}
        </p>
      </div>

      <p className="text-muted m-0 text-[13px]">
        {configured ? (
          <>
            An HMAC-signed event fires on every new pending manifest. Targets are defined in{' '}
            <code>config/notify.json</code> and named in the delivery errors below.
          </>
        ) : (
          <>
            Point <code>NOTIFY_CONFIG_PATH</code> at a config with webhook targets to turn this on.
            Until then a withheld capability is only visible to somebody who opens this console.
          </>
        )}
      </p>

      <p className="text-muted m-0 text-[13px]">
        Webhook URLs are never shown here. A webhook URL is a credential — anyone holding it can
        post as that integration — and the gateway redacts even the host out of its connection
        errors. This gateway serves no route describing its targets, so their configured names and
        formats are not available to this console either.
      </p>
    </section>
  );
}

function FailedDeliveries({ events }: { events: FailedNotification[] | null }) {
  if (events === null) {
    return (
      <section>
        <SectionKicker>Failed deliveries</SectionKicker>
        <EmptyState title="Nothing to deliver">
          <p>
            No targets are configured, so the gateway reports this surface as absent rather than
            showing an empty list — an empty list would read as &ldquo;everything was
            delivered&rdquo;.
          </p>
        </EmptyState>
      </section>
    );
  }

  if (events.length === 0) {
    return (
      <section>
        <SectionKicker>Failed deliveries</SectionKicker>
        <EmptyState title="Nothing failed">
          <p>
            Every notification has been delivered. Delivery is at-least-once with persisted backoff,
            so an event that is still retrying does not appear here.
          </p>
        </EmptyState>
      </section>
    );
  }

  return (
    <section>
      <SectionKicker>Failed deliveries</SectionKicker>
      <p className="text-muted m-0 mb-[var(--space-3)] text-sm">
        {events.length} {pluralise(events.length, 'event')} reached the retry limit and will not be
        sent again. Whoever these were meant to reach does not know the gateway is holding
        something.
      </p>
      <TableFrame>
        <table className="table">
          <thead>
            <tr>
              <th scope="col">Server</th>
              <th scope="col">Event</th>
              <th scope="col">Attempts</th>
              <th scope="col">Last error</th>
              {/* The API reports when the event was queued, not when it was
                  last retried, so the column says the thing it actually holds. */}
              <th scope="col">Event queued</th>
            </tr>
          </thead>
          <tbody>
            {events.map((event) => (
              <tr key={event.event_id}>
                <td>{event.server}</td>
                <td>
                  <span className="mono text-[13px]">{event.event}</span>
                </td>
                <td>
                  <Tag tone="outline">
                    {event.attempts}
                    <span className="sr-only"> {pluralise(event.attempts, 'attempt')}</span>
                  </Tag>
                </td>
                <td className="text-muted max-w-[40ch] break-words text-[13px]">
                  {event.last_error}
                </td>
                <td className="text-muted text-[13px]">
                  <time dateTime={event.created_at}>{formatTimestamp(event.created_at)}</time>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableFrame>
    </section>
  );
}
