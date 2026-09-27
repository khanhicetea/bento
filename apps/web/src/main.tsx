import { QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ApiError } from "./api/client.ts";
import { keys } from "./api/keys.ts";
import { App } from "./app.tsx";

const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({
    onError(error) {
      // An expired or revoked session sends the operator back to sign-in.
      if (error instanceof ApiError && error.status === 401) {
        queryClient.setQueryData(keys.session, { authenticated: false });
      }
    },
  }),
  defaultOptions: {
    queries: {
      retry: (count, error) => !(error instanceof ApiError && error.status < 500) && count < 2,
      refetchOnWindowFocus: true,
    },
  },
});

const root = document.getElementById("root");
if (!root) throw new Error("Missing #root element");

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
