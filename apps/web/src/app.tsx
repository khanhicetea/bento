import { Redirect, Route, Switch } from "wouter";
import { AppShell } from "./components/AppShell.tsx";
import { DomainLoading } from "./components/DomainState.tsx";
import { ApplicationsPage } from "./features/applications/ApplicationsPage.tsx";
import { BackupsPage } from "./features/backups/BackupsPage.tsx";
import { DatabasesPage } from "./features/data/DatabasesPage.tsx";
import { OperationTrackerProvider } from "./features/operations/OperationTracker.tsx";
import { OperationsPage } from "./features/operations/OperationsPage.tsx";
import { RoutingPage } from "./features/routing/RoutingPage.tsx";
import { LoginPage } from "./features/session/LoginPage.tsx";
import { useSession } from "./features/session/useSession.ts";

export function App() {
  const session = useSession();
  if (session.isPending) return <DomainLoading label="session" />;
  if (!session.data?.authenticated) return <LoginPage />;
  return (
    <OperationTrackerProvider>
      <AppShell>
        <Switch>
          <Route path="/applications" component={ApplicationsPage} />
          <Route path="/databases" component={DatabasesPage} />
          <Route path="/backups" component={BackupsPage} />
          <Route path="/routing" component={RoutingPage} />
          <Route path="/operations" component={OperationsPage} />
          <Route>
            <Redirect to="/applications" />
          </Route>
        </Switch>
      </AppShell>
    </OperationTrackerProvider>
  );
}
