/**
 * Recording a decision.
 *
 * Two properties matter here and they pull against each other: approving must
 * be hard to do by accident, and it must not be tedious enough that an
 * operator starts clicking through it. So the button on the card only opens
 * this; the decision itself needs a reason typed into a modal, and the reason
 * is the part of the record a reader a year from now will actually need.
 *
 * A native <dialog> is used rather than a hand-built overlay: it traps focus,
 * closes on Escape, and makes the page behind it inert without any of that
 * being reimplemented here and drifting.
 */

import { useId, useLayoutEffect, useRef, useState } from 'react';
import { useAuth } from '../api/auth';
import { useDecision } from '../api/queries';
import type { DecisionKind } from '../api/queries';
import { shortHash } from '../lib/format';
import { pluralise } from '../lib/format';
import { Failure } from './Failure';
import { Mono } from './primitives';

const MIN_REASON = 8;

export interface DecisionTarget {
  id: number;
  server: string;
  hash: string;
  /** How many capabilities this manifest is holding back right now. */
  withheld: number;
}

interface DecisionDialogProps {
  kind: DecisionKind;
  target: DecisionTarget;
  onDismiss: () => void;
}

export function DecisionDialog({ kind, target, onDismiss }: DecisionDialogProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const reasonId = useId();
  const operatorId = useId();

  const { operator, setOperator, fixed } = useAuth();
  const decision = useDecision(kind);

  const [reason, setReason] = useState('');
  const [name, setName] = useState(operator);
  const [problem, setProblem] = useState('');

  // Before paint: the design system styles .dialog as a flex column, which
  // would otherwise beat the browser's own "closed dialogs are hidden" rule
  // for one frame.
  useLayoutEffect(() => {
    dialog.current?.showModal();
  }, []);

  const verb = kind === 'approve' ? 'Approve' : 'Reject';

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    const trimmedName = name.trim();
    const trimmedReason = reason.trim();

    if (trimmedName === '') {
      setProblem('The gateway records who decided and refuses a decision it cannot attribute.');
      return;
    }
    if (trimmedReason.length < MIN_REASON) {
      setProblem(
        `Write at least ${MIN_REASON} characters. This reason is stored permanently and is the only explanation the record will ever carry.`,
      );
      return;
    }

    setProblem('');
    setOperator(trimmedName);
    decision.mutate(
      { id: target.id, username: trimmedName, reason: trimmedReason },
      { onSuccess: onDismiss },
    );
  };

  return (
    <dialog ref={dialog} className="dialog" aria-labelledby={titleId} onCancel={onDismiss}>
      <h2 id={titleId} className="dialog-title m-0">
        {verb} manifest {shortHash(target.hash)}?
      </h2>

      <p className="dialog-body m-0">
        <Consequence kind={kind} target={target} />
      </p>

      <form className="flex flex-col gap-[var(--space-3)]" onSubmit={submit}>
        <div className="field">
          <label htmlFor={reasonId}>
            {kind === 'approve' ? 'Why this is safe to admit' : 'Why this is being refused'}
          </label>
          <textarea
            id={reasonId}
            className="input"
            autoFocus
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder={
              kind === 'approve'
                ? 'Read the new input schema; the added field is a title string, not a path.'
                : 'Adds a filesystem write tool this server has no reason to offer.'
            }
          />
        </div>

        <div className="field">
          <label htmlFor={operatorId}>Recorded as</label>
          <input
            id={operatorId}
            className="input mono"
            value={name}
            readOnly={fixed}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => setName(event.target.value)}
          />
          <p className="text-muted m-0 mt-[var(--space-1)] text-[12px]">
            {fixed
              ? 'Set at deploy time via OPERATOR_NAME and not editable here.'
              : 'Stored with the decision. The gateway does not verify it — it is an attestation, not an identity.'}
          </p>
        </div>

        {problem && (
          <p className="m-0 text-[13px] text-[color:var(--color-accent-700)]" role="alert">
            {problem}
          </p>
        )}

        {decision.error && <Failure error={decision.error} />}

        <div className="dialog-actions">
          <button
            type="button"
            className="btn btn-secondary"
            onClick={onDismiss}
            disabled={decision.isPending}
          >
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" disabled={decision.isPending}>
            {decision.isPending ? `${verb}ing…` : verb}
          </button>
        </div>
      </form>
    </dialog>
  );
}

function Consequence({ kind, target }: { kind: DecisionKind; target: DecisionTarget }) {
  const { server, hash, withheld } = target;

  if (kind === 'approve') {
    return (
      <>
        {withheld > 0 && (
          <>
            Admits {withheld} withheld {pluralise(withheld, 'capability', 'capabilities')} to every
            client of <Mono>{server}</Mono>, and{' '}
          </>
        )}
        makes <Mono>{shortHash(hash)}</Mono> the approved baseline for <Mono>{server}</Mono>.
        Decisions are permanent and cannot be undone from this console.
      </>
    );
  }

  return (
    <>
      Leaves {withheld > 0 ? `${withheld} ` : ''}
      {pluralise(withheld, 'capability', 'capabilities')} withheld from every client of{' '}
      <Mono>{server}</Mono>. Whatever is already approved keeps working. Decisions are permanent and
      cannot be undone from this console.
    </>
  );
}
