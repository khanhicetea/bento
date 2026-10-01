import { Redirect, Route, Switch } from "wouter";
import { AppShell } from "./components/AppShell.tsx";
import { DomainLoading } from "./components/DomainState.tsx";
import { ApplicationPage } from "./features/applications/ApplicationDetail.tsx";
import { ApplicationsPage } from "./features/applications/ApplicationsPage.tsx";
import { CreateApplicationPage } from "./features/applications/CreateApplicationPage.tsx";
import { BackupsPage } from "./features/backups/BackupsPage.tsx";
import { OperationTrackerProvider } from "./features/operations/OperationTracker.tsx";
import { OperationsPage } from "./features/operations/OperationsPage.tsx";
import { OverviewPage } from "./features/overview/OverviewPage.tsx";
import { RoutingPage } from "./features/routing/RoutingPage.tsx";
import { LoginPage } from "./features/session/LoginPage.tsx";
import { useSession } from "./features/session/useSession.ts";
import { SystemPage } from "./features/system/SystemPage.tsx";

export function App() {
  const session = useSession();
  if (session.isPending) return <DomainLoading label="session" />;
  if (!session.data?.authenticated) return <LoginPage />;
  return (
    <OperationTrackerProvider>
      <AppShell>
        <Switch>
          <Route path="/" component={OverviewPage} />
          <Route path="/apps" component={ApplicationsPage} />
          <Route path="/apps/new" component={CreateApplicationPage} />
          <Route path="/apps/:slug/monitoring">
            {(params) => <ApplicationPage slug={params.slug} tab="monitoring" />}
          </Route>
          <Route path="/apps/:slug/logs">{(params) => <ApplicationPage slug={params.slug} tab="logs" />}</Route>
          <Route path="/apps/:slug/terminal">{(params) => <ApplicationPage slug={params.slug} tab="terminal" />}</Route>
          <Route path="/apps/:slug/data">{(params) => <ApplicationPage slug={params.slug} tab="data" />}</Route>
          <Route path="/apps/:slug/backup">{(params) => <ApplicationPage slug={params.slug} tab="backup" />}</Route>
          <Route path="/apps/:slug/scheduler">
            {(params) => <ApplicationPage slug={params.slug} tab="scheduler" />}
          </Route>
          <Route path="/apps/:slug/deploy">{(params) => <ApplicationPage slug={params.slug} tab="deploy" />}</Route>
          <Route path="/apps/:slug/settings">{(params) => <ApplicationPage slug={params.slug} tab="settings" />}</Route>
          <Route path="/apps/:slug">{(params) => <ApplicationPage slug={params.slug} />}</Route>
          <Route path="/backups" component={BackupsPage} />
          <Route path="/ingress" component={RoutingPage} />
          <Route path="/activity/:id">{(params) => <OperationsPage selectedId={params.id} />}</Route>
          <Route path="/activity">{() => <OperationsPage />}</Route>
          <Route path="/system" component={SystemPage} />
          <Route path="/applications">
            <Redirect to="/apps" />
          </Route>
          <Route path="/data">
            <Redirect to="/system" />
          </Route>
          <Route path="/databases">
            <Redirect to="/system" />
          </Route>
          <Route path="/routing">
            <Redirect to="/ingress" />
          </Route>
          <Route path="/operations">
            <Redirect to="/activity" />
          </Route>
          <Route>
            <Redirect to="/" />
          </Route>
        </Switch>
      </AppShell>
    </OperationTrackerProvider>
  );
}
