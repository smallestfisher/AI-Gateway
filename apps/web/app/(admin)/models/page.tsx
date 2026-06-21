"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Boxes, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import type { Model, ModelChannel, Provider } from "@/lib/types";
import { qk } from "@/lib/query-keys";
import { api } from "@/lib/api";
import { useResource } from "@/hooks/use-resource";
import { DataTable } from "@/components/data-table";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { modelColumns } from "./columns";
import { ModelForm } from "./model-form";
import { ChannelForm } from "./channel-form";

export default function ModelsPage() {
  const models = useResource<Model>({ key: qk.models, path: "/models" });
  const channels = useResource<ModelChannel>({
    key: qk.channels,
    path: "/model-channels",
  });
  // Providers are needed to render provider names in channel rows and to
  // populate the bind-channel dropdown.
  const providers = useQuery({
    queryKey: qk.providers,
    queryFn: () => api.list<Provider>("/providers"),
  });

  const [modelFormOpen, setModelFormOpen] = useState(false);
  const [pendingDeleteModel, setPendingDeleteModel] = useState<Model | null>(
    null,
  );
  // The model a channel is being bound to (null => form closed).
  const [channelTarget, setChannelTarget] = useState<Model | null>(null);
  const [pendingDeleteChannel, setPendingDeleteChannel] =
    useState<ModelChannel | null>(null);

  const providerName = useMemo(() => {
    const m = new Map<string, string>();
    for (const p of providers.data ?? []) {
      if (p.id) m.set(p.id, p.name);
    }
    return m;
  }, [providers.data]);

  const channelsByModel = useMemo(() => {
    const m = new Map<string, ModelChannel[]>();
    for (const c of channels.list.data ?? []) {
      if (!c.model_id) continue;
      const arr = m.get(c.model_id);
      if (arr) arr.push(c);
      else m.set(c.model_id, [c]);
    }
    return m;
  }, [channels.list.data]);

  async function createModel(body: Partial<Model>) {
    await models.create.mutateAsync(body);
    toast.success("Model created");
  }

  async function confirmDeleteModel() {
    if (!pendingDeleteModel?.id) return;
    try {
      await models.remove.mutateAsync(pendingDeleteModel.id);
      // Channels may cascade (FK) or be orphaned; either way refresh both.
      channels.list.refetch();
      toast.success("Model deleted");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setPendingDeleteModel(null);
    }
  }

  async function createChannel(body: Partial<ModelChannel>) {
    if (!channelTarget?.id) return;
    await channels.create.mutateAsync({ ...body, model_id: channelTarget.id });
    toast.success("Channel bound");
  }

  async function confirmDeleteChannel() {
    if (!pendingDeleteChannel?.id) return;
    try {
      await channels.remove.mutateAsync(pendingDeleteChannel.id);
      toast.success("Channel removed");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Remove failed");
    } finally {
      setPendingDeleteChannel(null);
    }
  }

  const columns = modelColumns({
    onDelete: setPendingDeleteModel,
    channelsByModel,
  });

  function renderExpanded(model: Model) {
    const list = channelsByModel.get(model.id ?? "") ?? [];
    return (
      <div className="px-4 py-3">
        <div className="mb-2 flex items-center justify-between">
          <p className="text-xs font-medium text-muted-foreground">
            Upstream channels ({list.length})
          </p>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setChannelTarget(model)}
          >
            <Plus className="size-3.5" />
            Bind channel
          </Button>
        </div>

        {list.length === 0 ? (
          <p className="rounded-md border border-dashed py-6 text-center text-xs text-muted-foreground">
            No channels bound — this alias won&apos;t route. Bind a provider to
            serve it.
          </p>
        ) : (
          <div className="space-y-1">
            {list.map((c) => (
              <div
                key={c.id}
                className="flex items-center justify-between rounded-md border bg-background px-3 py-2"
              >
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
                  <Badge variant="outline" className="font-mono text-[11px]">
                    {providerName.get(c.provider_id) ?? c.provider_id}
                  </Badge>
                  <span className="font-mono text-xs text-muted-foreground">
                    {c.upstream_model}
                  </span>
                  <span className="text-xs text-muted-foreground tabular-nums">
                    w:{c.weight} · p:{c.priority}
                  </span>
                  <Badge
                    variant={c.enabled ? "default" : "secondary"}
                    className="text-[10px]"
                  >
                    {c.enabled ? "on" : "off"}
                  </Badge>
                </div>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Remove channel"
                  onClick={() => setPendingDeleteChannel(c)}
                >
                  <Trash2 className="size-3.5 text-destructive" />
                </Button>
              </div>
            ))}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Models"
        description="Model aliases clients request, each routed to one or more upstream channels."
        actions={
          <Button size="sm" onClick={() => setModelFormOpen(true)}>
            <Plus className="size-4" />
            New model
          </Button>
        }
      />

      <DataTable
        columns={columns}
        data={models.list.data ?? []}
        loading={models.list.isLoading}
        searchPlaceholder="Search models…"
        renderExpanded={renderExpanded}
        empty={
          <EmptyState
            icon={<Boxes className="size-5" />}
            title="No models yet"
            description="Create a model alias, then bind upstream channels to route it."
            action={
              <Button size="sm" onClick={() => setModelFormOpen(true)}>
                <Plus className="size-4" />
                New model
              </Button>
            }
          />
        }
      />

      <ModelForm
        open={modelFormOpen}
        onOpenChange={setModelFormOpen}
        onSubmit={createModel}
        submitting={models.create.isPending}
      />

      <ChannelForm
        open={channelTarget !== null}
        onOpenChange={(o) => !o && setChannelTarget(null)}
        onSubmit={createChannel}
        submitting={channels.create.isPending}
        providers={providers.data ?? []}
        providersLoading={providers.isLoading}
        modelAlias={channelTarget?.alias ?? null}
      />

      <ConfirmDialog
        open={pendingDeleteModel !== null}
        onOpenChange={(o) => !o && setPendingDeleteModel(null)}
        title={`Delete “${pendingDeleteModel?.alias}”?`}
        description="Removing the model also unbinds its channels. Clients requesting this alias will get no-channel errors."
        confirmLabel="Delete model"
        loading={models.remove.isPending}
        onConfirm={confirmDeleteModel}
      />

      <ConfirmDialog
        open={pendingDeleteChannel !== null}
        onOpenChange={(o) => !o && setPendingDeleteChannel(null)}
        title="Remove this channel?"
        description="The alias will no longer route through this provider."
        confirmLabel="Remove channel"
        loading={channels.remove.isPending}
        onConfirm={confirmDeleteChannel}
      />
    </div>
  );
}
