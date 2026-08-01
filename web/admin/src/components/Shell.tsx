/**
 * The frame: a ledger's masthead, not a console's chrome.
 *
 * The two things always on screen are the two things an administrator needs
 * to trust the rest of the page: whether the gateway is answering, and which
 * name their decisions are being recorded under.
 */

import { useState } from 'react';
import type { ReactNode } from 'react';
import { Link, useRoute } from '../lib/router';
import { useAuth } from '../api/auth';
import { usePending, useHealth } from '../api/queries';

const NAV = [
  { to: '/', label: 'Pending', match: (path: string) => path === '/' },
  { to: '/servers', label: 'Servers', match: (path: string) => path.startsWith('/servers') },
  { to: '/ledger', label: 'Ledger', match: (path: string) => path.startsWith('/ledger') },
  { to: '/delivery', label: 'Delivery', match: (path: string) => path.startsWith('/delivery') },
];

export function Shell({ children }: { children: ReactNode }) {
  const path = useRoute();
  const pending = usePending();
  const [menuOpen, setMenuOpen] = useState(false);
  const waiting = pending.data?.length ?? 0;

  return (
    <div className="min-h-screen bg-paper">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-50 focus:rounded-sm focus:bg-slate focus:px-4 focus:py-2 focus:text-sm focus:text-paper"
      >
        Skip to content
      </a>

      <header className="border-b border-rule bg-white">
        <div className="mx-auto flex max-w-6xl items-center gap-4 px-4 py-3 sm:px-6">
          <Link to="/" className="flex shrink-0 items-center gap-2.5" aria-label="mcp-shield admin">
            <img src="/logo.svg" alt="" width={20} height={22} className="h-[22px] w-5" />
            <span className="font-mono text-[0.9375rem] font-medium tracking-tight text-ink">
              mcp<span className="text-signal">-</span>shield
            </span>
            <span className="hidden font-display text-micro uppercase tracking-[0.16em] text-slate/50 sm:inline">
              Admin
            </span>
          </Link>

          <nav aria-label="Primary" className="ml-auto hidden items-center gap-1 sm:flex">
            {NAV.map((item) => (
              <NavLink key={item.to} to={item.to} active={item.match(path)}>
                {item.label}
                {item.to === '/' && waiting > 0 && <PendingCount count={waiting} />}
              </NavLink>
            ))}
          </nav>

          <div className="ml-auto flex items-center gap-3 sm:ml-0">
            <HealthDot />
            <OperatorTag />
            <button
              type="button"
              className="rounded-sm border border-rule px-2.5 py-1.5 text-sm text-slate sm:hidden"
              aria-expanded={menuOpen}
              aria-controls="mobile-nav"
              onClick={() => setMenuOpen((open) => !open)}
            >
              Menu
            </button>
          </div>
        </div>

        {menuOpen && (
          <nav id="mobile-nav" aria-label="Primary" className="border-t border-rule sm:hidden">
            {NAV.map((item) => (
              <Link
                key={item.to}
                to={item.to}
                onClick={() => setMenuOpen(false)}
                className={`block border-b border-rule px-4 py-3 text-sm last:border-b-0 ${
                  item.match(path) ? 'bg-paper font-medium text-ink' : 'text-slate'
                }`}
              >
                {item.label}
                {item.to === '/' && waiting > 0 && <PendingCount count={waiting} />}
              </Link>
            ))}
          </nav>
        )}
      </header>

      <main id="main" className="mx-auto max-w-6xl px-4 py-8 sm:px-6 sm:py-12">
        {children}
      </main>

      <footer className="mx-auto max-w-6xl px-4 pb-12 sm:px-6">
        <p className="border-t border-rule pt-6 text-xs leading-relaxed text-slate/55">
          Every decision recorded here is permanent. Manifests are never edited — a manifest is
          approved, rejected, or superseded, and its hash and contents stay exactly as the gateway
          first saw them.
        </p>
      </footer>
    </div>
  );
}

function NavLink({ to, active, children }: { to: string; active: boolean; children: ReactNode }) {
  return (
    <Link
      to={to}
      aria-current={active ? 'page' : undefined}
      className={`relative rounded-sm px-3 py-1.5 text-sm transition-colors ${
        active ? 'text-ink' : 'text-slate/70 hover:text-ink'
      }`}
    >
      {children}
      {active && (
        <span aria-hidden className="absolute inset-x-3 -bottom-[13px] h-[2px] bg-slate" />
      )}
    </Link>
  );
}

function PendingCount({ count }: { count: number }) {
  return (
    <span className="ml-1.5 inline-flex min-w-[1.25rem] items-center justify-center rounded-sm bg-signal/12 px-1 font-display text-micro font-semibold tabular-nums text-signal">
      {count}
    </span>
  );
}

/**
 * Health is red or nothing. A gateway that is answering needs no celebration;
 * a gateway that is not is the first thing an administrator has to know,
 * because every screen behind it is then stale.
 */
function HealthDot() {
  const health = useHealth();

  if (health.isPending) return null;

  if (health.isError) {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-sm border border-fault/40 bg-fault/5 px-2 py-1 font-display text-micro uppercase tracking-[0.12em] text-fault">
        <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-fault" />
        Gateway unreachable
      </span>
    );
  }

  return (
    <span className="hidden items-center gap-1.5 font-display text-micro uppercase tracking-[0.12em] text-slate/50 sm:inline-flex">
      <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-slate/35" />
      Gateway {health.data?.status ?? 'ok'}
    </span>
  );
}

/**
 * The operator name, presented as what it is: a name that will be written into
 * the record, not an identity anyone checked.
 */
function OperatorTag() {
  const { operator } = useAuth();

  return (
    <Link
      to="/operator"
      className="flex items-center gap-2 rounded-sm border border-rule px-2.5 py-1.5 text-left transition-colors hover:border-slate/40"
    >
      <span className="label hidden sm:inline">Recorded as</span>
      {operator ? (
        <span className="font-mono text-[0.8125rem] text-ink">{operator}</span>
      ) : (
        <span className="font-sans text-[0.8125rem] text-held">Set a name</span>
      )}
    </Link>
  );
}
