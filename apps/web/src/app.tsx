import { Redirect, Route, Switch } from "wouter";
import { AppShell } from "./components/AppShell.tsx";
import { ApplicationsPage } from "./features/applications/ApplicationsPage.tsx";
import { ComingSoonPage } from "./pages/ComingSoonPage.tsx";

export function App() {
  return (
    <AppShell>
      <Switch>
        <Route path="/applications" component={ApplicationsPage} />
        <Route path="/data">
          <ComingSoonPage domain="Data & runtimes" />
        </Route>
        <Route path="/routing">
          <ComingSoonPage domain="Routing & TLS" />
        </Route>
        <Route path="/jobs">
          <ComingSoonPage domain="Jobs & workers" />
        </Route>
        <Route path="/operations">
          <ComingSoonPage domain="Operations" />
        </Route>
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
