"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ShieldCheck, Plus } from "lucide-react";
import { toast } from "sonner";
import type { ClientProfile, Model, Provider } from "@/lib/types";
import { qk } from "@/lib/query-keys";
import { api } from "@/lib/api";
import { useResource } from "@/hooks/use-resource";
import { DataTable } from "@/components/data-table";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { profileColumns } from "./columns";
import { ProfileForm } from "./profile-form";

export default function ProfilesPage() {
  const profiles = useResource<ClientProfile>({
    key: qk.profiles,
    path: "/client-profiles",
  });
  // Providers + models are needed for the target dropdown and the target
  // column's human-readable label.
  const providers = useQuery({
    queryKey: qk.providers,
    queryFn: () => api.list<Provider>("/providers"),
  });
  const models = useQuery({
    queryKey: qk.models,
    queryFn: () => api.list<Model>("/models"),
  });

  const [formOpen, setFormOpen] = useState(false);
  // Bump on each open to remount the form fresh (it's create-only, so every
  // open starts blank — no reset effect needed inside the form).
  const [formKey, setFormKey] = useState(0);
  const [pendingDelete, setPendingDelete] = useState<ClientProfile | null>(
    null,
  );

  const labelById = useMemo(() => {
    const m = new Map<string, string>();
    for (const p of providers.data ?? []) if (p.id) m.set(p.id, p.name);
    for (const m2 of models.data ?? [])
      if (m2.id) m.set(m2.id, m2.display_name || m2.alias);
    return m;
  }, [providers.data, models.data]);

  function targetLabel(scope: string, targetId: string) {
    if (scope === "provider") return labelById.get(targetId) ?? targetId;
    if (scope === "model") return labelById.get(targetId) ?? targetId;
    return targetId;
  }

  function openForm() {
    setFormKey((k) => k + 1);
    setFormOpen(true);
  }

  async function createProfile(body: Partial<ClientProfile>) {
    await profiles.create.mutateAsync(body);
    toast.success("Profile created");
  }

  async function confirmDelete() {
    if (!pendingDelete?.id) return;
    try {
      await profiles.remove.mutateAsync(pendingDelete.id);
      toast.success("Profile deleted");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setPendingDelete(null);
    }
  }

  const columns = profileColumns({
    onDelete: setPendingDelete,
    targetLabel,
  });

  return (
    <div className="space-y-6">
      <PageHeader
        title="Client Profiles"
        description="Egress impersonation profiles — headers/UA injected on the upstream's behalf, scoped globally, per provider, or per model."
        actions={
          <Button size="sm" onClick={openForm}>
            <Plus className="size-4" />
            New profile
          </Button>
        }
      />

      <DataTable
        columns={columns}
        data={profiles.list.data ?? []}
        loading={profiles.list.isLoading}
        searchPlaceholder="Search profiles…"
        empty={
          <EmptyState
            icon={<ShieldCheck className="size-5" />}
            title="No profiles yet"
            description="Create an impersonation profile to control how requests present to upstreams."
            action={
              <Button size="sm" onClick={openForm}>
                <Plus className="size-4" />
                New profile
              </Button>
            }
          />
        }
      />

      <ProfileForm
        key={formKey}
        open={formOpen}
        onOpenChange={setFormOpen}
        onSubmit={createProfile}
        submitting={profiles.create.isPending}
        providers={providers.data ?? []}
        models={models.data ?? []}
      />

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(o) => !o && setPendingDelete(null)}
        title={`Delete “${pendingDelete?.name}”?`}
        description="Requests in this scope will fall back to less specific profiles."
        confirmLabel="Delete profile"
        loading={profiles.remove.isPending}
        onConfirm={confirmDelete}
      />
    </div>
  );
}
