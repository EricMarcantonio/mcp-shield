/**
 * Recording a decision.
 *
 * Two properties matter here and they pull against each other: this must be
 * hard to do by accident, and it must not be tedious enough that an operator
 * starts clicking through it. So: the decision is chosen, then confirmed, and
 * the confirmation asks for the one thing that makes the record worth keeping
 * — why. A reason is required. The button that finishes the job is the only
 * one on screen that says "Approve" or "Reject" in the imperative.
 *
 * The name the decision is filed under is shown, not hidden, and described as
 * what it is: a name the gateway records and does not verify.
 */

import { useEffect, useRef, useState } from 'react';
import { useAuth } from '../api/auth';
import { useDecision } from '../api/queries';
import type { DecisionKind } from '../api/queries';
import { Button } from '../components/primitives';
import { Failure } from '../components/Failure';
import { Link } from '../lib/router';
import { pluralise, shortHash } from '../lib/format';

const MIN_REASON = 8;

interface DecisionPanelProps {
  manifestId: number;
  server: string;
  hash: string;
  withheld: number;
  onSettled: () => void;
}

export function DecisionPanel({
  manifestId,
  server,
  hash,
  withheld,
  onSettled,
}: DecisionPanelProps) {
  const { operator } = useAuth();
  const [kind, setKind] = useState<DecisionKind | null>(null);

  if (!operator) {
    return (
      <section className="card px-5 py-5">
        <h2 className="font-display text-lg font-semibold text-ink">Decide</h2>
        <p className="mt-2 max-w-2xl text-sm leading-relaxed text-slate/80">
          The gateway records who made each decision and refuses one it cannot attribute. Set the
          name this console files decisions under before approving or rejecting.
        </p>
        <div className="mt-4">
          <Link
            to="/operator"
            className="inline-flex items-center rounded-sm bg-slate px-4 py-2 text-sm font-medium text-paper hover:bg-ink"
          >
            Set a name
          </Link>
        </div>
      </section>
    );
  }

  return (
    <section className="card px-5 py-5">
      <h2 className="font-display text-lg font-semibold text-ink">Decide</h2>
      <p className="mt-2 max-w-2xl text-sm leading-relaxed text-slate/80">
        {withheld > 0 ? (
          <>
            Approving admits {withheld} {pluralise(withheld, 'capability', 'capabilities')} to every
            client of <span className="font-mono text-[0.8125rem]">{server}</span> and makes{' '}
            <span className="font-mono text-[0.8125rem]">{shortHash(hash)}</span> the new baseline.
            Rejecting leaves them withheld; the tools you already approved keep working either way.
          </>
        ) : (
          <>
            Approving makes <span className="font-mono text-[0.8125rem]">{shortHash(hash)}</span>{' '}
            the new baseline for <span className="font-mono text-[0.8125rem]">{server}</span>.
          </>
        )}
      </p>

      {kind === null ? (
        <div className="mt-5 flex flex-wrap gap-3">
          <Button variant="primary" onClick={() => setKind('approve')}>
            Approve…
          </Button>
          <Button variant="danger" onClick={() => setKind('reject')}>
            Reject…
          </Button>
        </div>
      ) : (
        <ConfirmForm
          kind={kind}
          manifestId={manifestId}
          operator={operator}
          onCancel={() => setKind(null)}
          onDone={onSettled}
        />
      )}
    </section>
  );
}

function ConfirmForm({
  kind,
  manifestId,
  operator,
  onCancel,
  onDone,
}: {
  kind: DecisionKind;
  manifestId: number;
  operator: string;
  onCancel: () => void;
  onDone: () => void;
}) {
  const decision = useDecision(kind);
  const [reason, setReason] = useState('');
  const [touched, setTouched] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    field.current?.focus();
  }, []);

  const tooShort = reason.trim().length < MIN_REASON;
  const showError = touched && tooShort;

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    setTouched(true);
    if (tooShort || decision.isPending) return;
    decision.mutate(
      { id: manifestId, username: operator, reason: reason.trim() },
      { onSuccess: onDone },
    );
  };

  const verb = kind === 'approve' ? 'Approve' : 'Reject';
  const working = kind === 'approve' ? 'Approving…' : 'Rejecting…';

  return (
    <form
      className="mt-5 border-t border-rule pt-5"
      onSubmit={submit}
      onKeyDown={(event) => {
        if (event.key === 'Escape') onCancel();
      }}
    >
      <label htmlFor="reason" className="label block">
        Why {kind === 'approve' ? 'this is safe to admit' : 'this is being refused'}
      </label>
      <p className="mt-1.5 text-sm text-slate/70">
        This is the part of the record a reader a year from now will actually need. Name what you
        checked.
      </p>
      <textarea
        id="reason"
        ref={field}
        rows={3}
        value={reason}
        onChange={(event) => setReason(event.target.value)}
        onBlur={() => setTouched(true)}
        aria-invalid={showError}
        aria-describedby={showError ? 'reason-error' : undefined}
        className={`mt-3 w-full rounded-sm border bg-white px-3 py-2 font-sans text-sm text-ink placeholder:text-slate/35 ${
          showError ? 'border-fault' : 'border-rule'
        }`}
        placeholder={
          kind === 'approve'
            ? 'Read the new input schema; the added field is a title string, not a path.'
            : 'Adds a filesystem write tool this server has no reason to offer.'
        }
      />
      {showError && (
        <p id="reason-error" className="mt-2 text-sm text-fault">
          Write at least {MIN_REASON} characters. The gateway stores this reason permanently and it
          is the only explanation the record will ever carry.
        </p>
      )}

      <p className="mt-4 text-sm text-slate/70">
        Recorded as <span className="font-mono text-[0.8125rem] text-ink">{operator}</span>. The
        gateway stores this name with the decision. It does not verify it.
      </p>

      {decision.error && (
        <div className="mt-4">
          <Failure error={decision.error} />
        </div>
      )}

      <div className="mt-5 flex flex-wrap items-center gap-3">
        <Button
          type="submit"
          variant={kind === 'approve' ? 'primary' : 'danger'}
          disabled={decision.isPending}
        >
          {decision.isPending ? working : verb}
        </Button>
        <Button variant="quiet" onClick={onCancel} disabled={decision.isPending}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
