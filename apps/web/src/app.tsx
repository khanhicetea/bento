import { Redirect, Route, Switch } from "wouter";
import { AppShell } from "./components/AppShell.tsx";
import { ApplicationsPage } from "./features/applications/ApplicationsPage.tsx";
import { DataPage } from "./features/data/DataPage.tsx";
import { JobsPage } from "./features/jobs/JobsPage.tsx";
import { OperationsPage } from "./features/operations/OperationsPage.tsx";
import { RoutingPage } from "./features/routing/RoutingPage.tsx";

export function App() {
  return (
    <AppShell>
      <Switch>
        <Route path="/applications" component={ApplicationsPage} />
        <Route path="/data" component={DataPage} />
        <Route path="/routing" component={RoutingPage} />
        <Route path="/jobs" component={JobsPage} />
        <Route path="/operations" component={OperationsPage} />
        <Route path="/">
          <Redirect to="/applications" />
        </Route>
        <Route>
          <Redirect to="/applications" />
        </Route>
      </Switch>
    </AppShell>
  );
}
