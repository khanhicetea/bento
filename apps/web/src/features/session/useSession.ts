import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, setCsrfToken } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";

export function useSession() {
  return useQuery({
    queryKey: keys.session,
    queryFn: async ({ signal }) => {
      const session = await api.session.get(signal);
      setCsrfToken(session.csrfToken);
      return session;
    },
    staleTime: 60_000,
  });
}

export function useLogout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: api.session.logout,
    onSuccess() {
      setCsrfToken("");
      // Update the mounted session query in place (clearing the cache would
      // detach its observer), then drop every other cached server response.
      queryClient.setQueryData(keys.session, { authenticated: false });
      queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== keys.session[0] });
    },
  });
}
