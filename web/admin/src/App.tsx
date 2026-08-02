import { useRoute } from './lib/router';
import { Shell } from './components/Shell';
import { PendingView } from './views/Pending';
import { ServersView } from './views/Servers';
import { HistoryView } from './views/History';
import { NotificationsView } from './views/Notifications';
import { EmptyState, Mono } from './components/primitives';
import { Link } from './lib/router';

export function App() {
  const path = useRoute();

  return <Shell>{route(path)}</Shell>;
}

function route(path: string) {
  if (path === '/') return <PendingView />;
  if (path === '/servers') return <ServersView />;
  if (path === '/history') return <HistoryView />;
  if (path === '/notifications') return <NotificationsView />;

  return (
    <EmptyState
      title="No such page"
      action={
        <Link to="/" className="btn btn-primary">
          Back to the queue
        </Link>
      }
    >
      <p>
        <Mono>{path}</Mono> is not a page in this console.
      </p>
    </EmptyState>
  );
}
