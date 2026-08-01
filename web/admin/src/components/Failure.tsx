/**
 * What to show when a read fails.
 *
 * Errors state what happened and what to do about it. They do not apologise,
 * and they do not hide the gateway's own wording — an operator comparing this
 * screen against the gateway's logs should see the same sentence in both.
 */

import { ApiError, RouteNotServedError, UnreachableError } from '../api/client';
import { apiBaseUrl } from '../lib/config';
import { Button, EmptyState } from './primitives';

export function Failure({ error, retry }: { error: unknown; retry?: () => void }) {
  const { title, body } = describe(error);

  return (
    <EmptyState
      tone="fault"
      title={title}
      action={
        retry && (
          <Button variant="quiet" onClick={retry}>
            Try again
          </Button>
        )
      }
    >
      {body}
    </EmptyState>
  );
}

/** A proxy in front of the console reports a dead upstream as 502/503/504. */
function isUpstreamDown(error: unknown): boolean {
  return error instanceof ApiError && [502, 503, 504].includes(error.status);
}

function describe(error: unknown): { title: string; body: React.ReactNode } {
  const target = apiBaseUrl || 'this origin';

  if (error instanceof UnreachableError || isUpstreamDown(error)) {
    return {
      title: 'The gateway did not answer',
      body: (
        <>
          <p>
            Nothing answered at <span className="font-mono text-[0.8125rem]">{target}</span>.
          </p>
          <p>
            Start the gateway, or point this console somewhere else with{' '}
            <span className="font-mono text-[0.8125rem]">API_BASE_URL</span>. While it is down the
            gate is still enforcing: capabilities stay withheld, they just cannot be reviewed here.
          </p>
        </>
      ),
    };
  }

  if (error instanceof RouteNotServedError) {
    return {
      title: 'This gateway does not serve that route',
      body: (
        <p>
          <span className="font-mono text-[0.8125rem]">{error.path}</span> is not part of this
          gateway build's API.
        </p>
      ),
    };
  }

  if (error instanceof ApiError) {
    return {
      title: `The gateway refused the request (${error.status})`,
      body: (
        <>
          <p className="font-mono text-[0.8125rem] text-ink">{error.message}</p>
          {error.isConflict && (
            <p>
              Somebody decided this manifest first. Reload the queue to see the decision that was
              recorded.
            </p>
          )}
        </>
      ),
    };
  }

  return {
    title: 'The request failed',
    body: <p className="font-mono text-[0.8125rem] text-ink">{String(error)}</p>,
  };
}
