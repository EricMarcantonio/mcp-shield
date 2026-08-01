/**
 * A five-route console does not need a routing framework, and a security tool
 * is a poor place to carry dependencies it does not use. This is the History
 * API with a subscription: `useRoute` re-renders on navigation, `Link` renders
 * a real anchor so middle-click, copy-link and keyboard focus behave the way
 * the browser already knows how to make them behave.
 */

import { useCallback, useEffect, useMemo, useState } from 'react';
import type { AnchorHTMLAttributes, MouseEvent, ReactNode } from 'react';

const listeners = new Set<() => void>();

function announce(): void {
  for (const listener of listeners) listener();
}

export function navigate(path: string, replace = false): void {
  if (replace) window.history.replaceState({}, '', path);
  else window.history.pushState({}, '', path);
  announce();
  window.scrollTo(0, 0);
}

export function useRoute(): string {
  const [path, setPath] = useState(() => window.location.pathname);

  useEffect(() => {
    const update = () => setPath(window.location.pathname);
    listeners.add(update);
    window.addEventListener('popstate', update);
    return () => {
      listeners.delete(update);
      window.removeEventListener('popstate', update);
    };
  }, []);

  return path;
}

/** Matches "/manifests/:id" style patterns; returns null when it does not. */
export function matchRoute(pattern: string, path: string): Record<string, string> | null {
  const patternParts = pattern.split('/').filter(Boolean);
  const pathParts = path.split('/').filter(Boolean);
  if (patternParts.length !== pathParts.length) return null;

  const params: Record<string, string> = {};
  for (const [i, part] of patternParts.entries()) {
    const actual = pathParts[i];
    if (actual === undefined) return null;
    if (part.startsWith(':')) params[part.slice(1)] = decodeURIComponent(actual);
    else if (part !== actual) return null;
  }
  return params;
}

export function useMatch(pattern: string): Record<string, string> | null {
  const path = useRoute();
  return useMemo(() => matchRoute(pattern, path), [pattern, path]);
}

type LinkProps = AnchorHTMLAttributes<HTMLAnchorElement> & { to: string; children: ReactNode };

export function Link({ to, children, onClick, ...rest }: LinkProps) {
  const handle = useCallback(
    (event: MouseEvent<HTMLAnchorElement>) => {
      onClick?.(event);
      // Leave modified clicks to the browser: new tab, new window, download.
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.shiftKey) return;
      if (event.button !== 0) return;
      event.preventDefault();
      navigate(to);
    },
    [to, onClick],
  );

  return (
    <a href={to} onClick={handle} {...rest}>
      {children}
    </a>
  );
}
