import { matchRoute, useRoute } from './lib/router';
import { Shell } from './components/Shell';
import { PendingView } from './views/Pending';
import { ServersView } from './views/Servers';
import { LedgerView } from './views/Ledger';
import { DeliveryView } from './views/Delivery';
import { OperatorView } from './views/Operator';
import { ManifestDetailView } from './views/ManifestDetail';
import { EmptyState } from './components/primitives';
import { Link } from './lib/router';

export function App() {
  const path = useRoute();

  return <Shell>{route(path)}</Shell>;
}

function route(path: string) {
  if (path === '/') return <PendingView />;
  if (path === '/servers') return <ServersView />;
  if (path === '/ledger') return <LedgerView />;
  if (path === '/delivery') return <DeliveryView />;
  if (path === '/operator') return <OperatorView />;

  const manifest = matchRoute('/manifests/:id', path);
  if (manifest?.id) {
    const id = Number(manifest.id);
    if (Number.isInteger(id) && id > 0) return <ManifestDetailView key={id} id={id} />;
  }

  return (
    <EmptyState
      title="No such page"
      action={
        <Link
          to="/"
          className="inline-flex items-center rounded-sm bg-slate px-4 py-2 text-sm font-medium text-paper hover:bg-ink"
        >
          Back to the queue
        </Link>
      }
    >
      <p>
        <span className="font-mono text-[0.8125rem]">{path}</span> is not a page in this console.
      </p>
    </EmptyState>
  );
}
