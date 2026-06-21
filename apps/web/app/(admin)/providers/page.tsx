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
      toast.success("供应商已更新");
    } else {
      await create.mutateAsync(body);
      toast.success("供应商已创建");
    }
  }
  async function confirmDelete() {
    if (!pendingDelete?.id) return;
    try {
      await remove.mutateAsync(pendingDelete.id);
      toast.success("供应商已删除");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "删除失败");
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
        title="供应商"
        description="上游 API 供应商及其熔断阈值。"
        actions={
          <Button size="sm" onClick={startNew}>
            <Plus className="size-4" />
            新建供应商
          </Button>
        }
      />

      <DataTable
        columns={columns}
        data={list.data ?? []}
        loading={list.isLoading}
        searchPlaceholder="搜索供应商..."
        empty={
          <EmptyState
            icon={<Network className="size-5" />}
            title="暂无供应商"
            description="添加上游供应商后即可开始路由请求。"
            action={
              <Button size="sm" onClick={startNew}>
                <Plus className="size-4" />
                新建供应商
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
        title={`删除 "${pendingDelete?.name}"？`}
        description="删除后，绑定到该供应商的模型会失去这条通道。"
        confirmLabel="删除供应商"
        loading={remove.isPending}
        onConfirm={confirmDelete}
      />
    </div>
  );
}
