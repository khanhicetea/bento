import { useQuery } from "@tanstack/react-query";
import { api, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";

/**
 * Public URL of an Ingress host: scheme from its TLS mode, and the edge's
 * bound port when it is not the scheme default.
 */
export function hostUrl(name: string, host: T.Host | undefined, edge: T.EdgeSettings | undefined) {
  const secure = host ? host.route.tls !== "none" : false;
  const port = secure ? edge?.httpsPort : edge?.httpPort;
  const fallback = secure ? 443 : 80;
  const suffix = port && port !== fallback ? `:${port}` : "";
  return `${secure ? "https" : "http"}://${name}${suffix}`;
}

/** Resolves host names to clickable URLs using the hosts list and edge settings. */
export function useHostUrl() {
  const hosts = useQuery({ queryKey: keys.hosts, queryFn: ({ signal }) => api.hosts.list(signal) });
  const edge = useQuery({ queryKey: keys.edge, queryFn: ({ signal }) => api.edge.get(signal) });
  return (name: string) =>
    hostUrl(
      name,
      hosts.data?.hosts.find((host) => host.name === name),
      edge.data?.settings,
    );
}
