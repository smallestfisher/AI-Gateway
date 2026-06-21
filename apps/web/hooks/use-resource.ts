"use client";

import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "@/lib/api";

// Generic CRUD hooks for a resource backed by a standard /api/admin/<path>
// collection (GET list / POST create / PUT :id / DELETE :id). Specialised
// resources (policies, users) use bespoke hooks.
export function useResource<T extends { id?: string }>(opts: {
  key: readonly unknown[];
  path: string;
}) {
  const qc = useQueryClient();

  const list = useQuery({
    queryKey: opts.key,
    queryFn: () => api.list<T>(opts.path),
  });

  const create = useMutation({
    mutationFn: (body: Partial<T>) =>
      api.create<{ id: string }>(opts.path, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: opts.key }),
  });

  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<T> }) =>
      api.update(`${opts.path}/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: opts.key }),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.remove(`${opts.path}/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: opts.key }),
  });

  return { list, create, update, remove };
}
