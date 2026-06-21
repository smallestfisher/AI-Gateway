"use client";

import { useState } from "react";
import { Network, Plus } from "lucide-react";
import { toast } from "sonner";
import type { Provider } from "@/lib/types";
import { qk } from "@/lib/query-keys";
import { useResource } from "@/hooks/use-resource";
import { DataTable } from "@/components/data-table";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { providerColumns } from "./columns";
import { ProviderForm } from "./provider-form";

export default function ProvidersPage() {
  const { list, create, update, remove } = useResource<Provider>({
    key: qk.providers,
    path: "/providers",
  });
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<Provider | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Provider | null>(null);

  function startNew() {
    setEditing(null);
    setOpen(true);
  }
  function startEdit(p: Provider) {
    setEditing(p);
    setOpen(true);
  }
  async function submit(body: Partial<Provider>) {
    if (editing?.id) {
      await update.mutateAsync({ id: editing.id, body });
      toast.success("Provider updated");
    } else {
      await create.mutateAsync(body);
      toast.success("Provider created");
    }
  }
  async function confirmDelete() {
    if (!pendingDelete?.id) return;
    try {
      await remove.mutateAsync(pendingDelete.id);
      toast.success("Provider deleted");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setPendingDelete(null);
    }
  }

  const columns = providerColumns({
    onEdit: startEdit,
    onDelete: setPendingDelete,
  });

  return (
    <div className="space-y-6">
      <PageHeader
        title="Providers"
        description="Upstream API providers and their circuit-breaker thresholds."
        actions={
          <Button size="sm" onClick={startNew}>
            <Plus className="size-4" />
            New provider
          </Button>
        }
      />

      <DataTable
        columns={columns}
        data={list.data ?? []}
        loading={list.isLoading}
        searchPlaceholder="Search providers…"
        empty={
          <EmptyState
            icon={<Network className="size-5" />}
            title="No providers yet"
            description="Add an upstream provider to start routing."
            action={
              <Button size="sm" onClick={startNew}>
                <Plus className="size-4" />
                New provider
              </Button>
            }
          />
        }
      />

      <ProviderForm
        provider={editing}
        open={open}
        onOpenChange={setOpen}
        onSubmit={submit}
        submitting={create.isPending || update.isPending}
      />

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(o) => !o && setPendingDelete(null)}
        title={`Delete "${pendingDelete?.name}"?`}
        description="This removes the provider. Models bound to it will lose this channel."
        confirmLabel="Delete provider"
        loading={remove.isPending}
        onConfirm={confirmDelete}
      />
    </div>
  );
}
