/**
 * The frame: a 248px sidebar and a single reading column.
 *
 * Three things are pinned in the sidebar because they qualify everything to
 * the right of it — how many manifests are waiting, the name decisions get
 * filed under, and what mode the gate is in. The third one the gateway does
 * not report; see GateMode below.
 */

import type { ReactNode } from 'react';
import { Link, useRoute } from '../lib/router';
import { useAuth } from '../api/auth';
import { useFailedNotifications, useHealth, usePending } from '../api/queries';
import { Tag } from './primitives';
import type { TagTone } from './primitives';

export function Shell({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      <a className="skip-link" href="#main">
        Skip to content
      </a>

      <aside className="flex w-full shrink-0 flex-col gap-[var(--space-6)] border-b p-[var(--space-6)] md:sticky md:top-0 md:h-screen md:w-[248px] md:border-b-0 md:border-r">
        <div>
          <Link
            to="/"
            className="block leading-none text-[color:var(--color-accent-700)] no-underline"
          >
            <span className="font-[family-name:var(--font-heading)] text-[22px]">mcp-shield</span>
          </Link>
          <p className="text-muted m-0 mt-[6px] text-[13px]">Approval dashboard</p>
        </div>

        <PrimaryNav />

        <div className="mt-auto flex flex-col gap-[var(--space-3)] border-t pt-[var(--space-4)]">
          <GatewayReachability />
          <RecordedAs />
          <GateMode />
        </div>
      </aside>

      <main
        id="main"
        className="flex w-full max-w-[1000px] flex-1 flex-col gap-[var(--space-6)] p-[var(--space-6)] md:p-[var(--space-8)]"
      >
        {children}
      </main>
    </div>
  );
}

interface NavItem {
  to: string;
  label: string;
  /** Hidden at zero: an empty queue should not wear a badge saying so. */
  count: number;
  tone: TagTone;
  countNoun: string;
}

function PrimaryNav() {
  const path = useRoute();
  const pending = usePending();
  const failed = useFailedNotifications();

  const items: NavItem[] = [
    {
      to: '/',
      label: 'Pending',
      count: pending.data?.length ?? 0,
      tone: 'accent',
      countNoun: 'waiting for review',
    },
    { to: '/servers', label: 'Servers', count: 0, tone: 'neutral', countNoun: '' },
    { to: '/history', label: 'History', count: 0, tone: 'neutral', countNoun: '' },
    {
      to: '/notifications',
      label: 'Notifications',
      count: failed.data?.length ?? 0,
      tone: 'outline',
      countNoun: 'undelivered',
    },
  ];

  return (
    <nav aria-label="Primary">
      <ul className="flex flex-row flex-wrap gap-[var(--space-2)] md:flex-col md:flex-nowrap">
        {items.map((item) => (
          <li key={item.to}>
            <Link
              to={item.to}
              className="side-nav-item"
              aria-current={isCurrent(item.to, path) ? 'page' : undefined}
            >
              <span>{item.label}</span>
              {item.count > 0 && (
                <span className="ml-auto pl-[var(--space-2)]">
                  <Tag tone={item.tone}>
                    {item.count}
                    <span className="sr-only"> {item.countNoun}</span>
                  </Tag>
                </span>
              )}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}

function isCurrent(to: string, path: string): boolean {
  return to === '/' ? path === '/' : path.startsWith(to);
}

function SidebarLabel({ children }: { children: ReactNode }) {
  return (
    <span className="text-muted block text-[12px] uppercase tracking-[0.04em]">{children}</span>
  );
}

/**
 * The one thing worth interrupting for: a gateway that is not answering makes
 * every other screen stale. Silent while it is answering — a working gateway
 * needs no announcement.
 */
function GatewayReachability() {
  const health = useHealth();
  if (!health.isError) return null;

  return (
    <div>
      <SidebarLabel>Gateway</SidebarLabel>
      <Tag tone="outline">Not answering</Tag>
    </div>
  );
}

/**
 * A caller-supplied attestation, never a verified identity: the gateway
 * stores whatever name is sent with a decision and checks nothing. The word
 * "Recorded" is doing load-bearing work.
 */
function RecordedAs() {
  const { operator } = useAuth();

  return (
    <div>
      <SidebarLabel>Recorded as</SidebarLabel>
      {operator ? (
        <span className="mono text-[13px]">{operator}</span>
      ) : (
        <span className="text-muted text-[13px]">Set when you decide</span>
      )}
    </div>
  );
}

/**
 * FAIL_MODE decides whether the gate withholds or merely observes, and no API
 * route reports it. Rendering "block" here because it is the default would be
 * inventing the single most consequential fact on the screen — an operator
 * running FAIL_MODE=warn would read a console telling them they are protected.
 * So it says what it knows, which is nothing.
 */
function GateMode() {
  return (
    <div className="flex flex-col gap-[var(--space-1)]">
      <SidebarLabel>Gate mode</SidebarLabel>
      <span className="w-fit">
        <Tag tone="neutral">Not reported</Tag>
      </span>
      <p className="text-muted m-0 text-[12px] leading-snug">
        No API route exposes FAIL_MODE, so this console does not claim a value for it.
      </p>
    </div>
  );
}
