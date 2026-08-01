/**
 * The name decisions are filed under.
 *
 * This is not a login. The console has no authentication and is not going to
 * grow one — the gateway's approval API is meant to be fronted by an identity
 * provider. What the API does require is a `username` on approve and reject,
 * because it refuses to record a decision it cannot attribute, and refuses to
 * invent a name on the caller's behalf.
 *
 * So this page collects one, and is honest about what it is worth.
 */

import { useState } from 'react';
import { useAuth } from '../api/auth';
import { navigate } from '../lib/router';
import { Button, EmptyState, PageHeading } from '../components/primitives';

export function OperatorView() {
  const { operator, setOperator, fixed } = useAuth();
  const [draft, setDraft] = useState(operator);
  const [saved, setSaved] = useState(false);

  return (
    <>
      <PageHeading
        eyebrow="Attribution"
        title="Recorded as"
        lede="The gateway writes this name into its approvals table beside every decision you make here, and stores it permanently."
      />

      {fixed ? (
        <EmptyState title="This name comes from configuration">
          <p>
            Decisions from this console are recorded as{' '}
            <span className="font-mono text-[0.8125rem] text-ink">{operator}</span>, set at deploy
            time via <span className="font-mono text-[0.8125rem]">OPERATOR_NAME</span>. It cannot be
            changed from the browser.
          </p>
        </EmptyState>
      ) : (
        <form
          className="card max-w-xl px-5 py-5"
          onSubmit={(event) => {
            event.preventDefault();
            setOperator(draft);
            setSaved(true);
          }}
        >
          <label htmlFor="operator" className="label block">
            Name
          </label>
          <input
            id="operator"
            value={draft}
            onChange={(event) => {
              setDraft(event.target.value);
              setSaved(false);
            }}
            autoComplete="off"
            spellCheck={false}
            placeholder="e.g. eric"
            className="mt-2 w-full rounded-sm border border-rule bg-white px-3 py-2 font-mono text-sm text-ink placeholder:font-sans placeholder:text-slate/35"
          />
          <p className="mt-3 text-sm leading-relaxed text-slate/75">
            Kept in this browser only. It is a caller-supplied attestation, not a verified identity
            — the gateway stores whatever is sent and checks nothing. Use a name that means
            something to whoever reads the record later.
          </p>

          <div className="mt-5 flex flex-wrap items-center gap-3">
            <Button type="submit" variant="primary" disabled={draft.trim() === ''}>
              Save
            </Button>
            <Button variant="quiet" onClick={() => navigate('/')}>
              Back to the queue
            </Button>
            {saved && (
              <span className="font-display text-micro uppercase tracking-[0.12em] text-slate/60">
                Saved
              </span>
            )}
          </div>
        </form>
      )}

      <section className="mt-8 max-w-2xl">
        <h2 className="font-display text-lg font-semibold text-ink">Why there is no login</h2>
        <p className="mt-2 text-sm leading-relaxed text-slate/75">
          The approval API has no authentication and is meant to be bound to localhost or a trusted
          network. Access control belongs in front of it, not in this console. Every request this
          console makes goes through one module with one place to attach an{' '}
          <span className="font-mono text-[0.8125rem]">Authorization</span> header, so putting an
          identity provider in front is a single edit rather than a rewrite.
        </p>
      </section>
    </>
  );
}
