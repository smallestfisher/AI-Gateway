"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, ListPlus, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { qk } from "@/lib/query-keys";
import type {
  BulkModelChannelInput,
  BulkModelChannelResult,
  Model,
  ModelChannel,
  Provider,
  UpstreamModel,
} from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";

type Draft = {
  selected?: boolean;
  alias?: string;
};

const aliasPattern = /^[a-zA-Z0-9_.\-/]+$/;

export function ModelSyncSheet({
  provider,
  open,
  onOpenChange,
  models,
  channels,
}: {
  provider: Provider | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  models: Model[];
  channels: ModelChannel[];
}) {
  const queryClient = useQueryClient();
  const providerId = provider?.id ?? "";
  const [search, setSearch] = useState("");
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [weight, setWeight] = useState(1);
  const [priority, setPriority] = useState(0);
  const [result, setResult] = useState<BulkModelChannelResult | null>(null);

  const upstreamModels = useQuery({
    queryKey: qk.modelSync(providerId),
    queryFn: () =>
      api.list<UpstreamModel>(`/providers/${providerId}/upstream-models`),
    enabled: open && Boolean(providerId),
    retry: false,
  });

  const modelByAlias = useMemo(() => {
    const map = new Map<string, Model>();
    for (const model of models) {
      map.set(model.alias, model);
    }
    return map;
  }, [models]);

  const filteredModels = useMemo(() => {
    const q = search.trim().toLowerCase();
    const list = upstreamModels.data ?? [];
    if (!q) return list;
    return list.filter((model) =>
      `${model.id} ${model.display_name ?? ""}`.toLowerCase().includes(q),
    );
  }, [search, upstreamModels.data]);

  const rows = useMemo(
    () =>
      filteredModels.map((upstream) => {
        const draft = drafts[upstream.id] ?? {};
        const alias = draft.alias ?? upstream.id;
        const model = modelByAlias.get(alias);
        const bound =
          Boolean(model?.id) &&
          channels.some(
            (channel) =>
              channel.model_id === model?.id &&
              channel.provider_id === providerId &&
              channel.upstream_model === upstream.id,
          );
        const status = bound ? "bound" : model ? "alias" : "new";
        return {
          upstream,
          alias,
          selected: Boolean(draft.selected) && !bound,
          status,
          validAlias: aliasPattern.test(alias),
        };
      }),
    [channels, drafts, filteredModels, modelByAlias, providerId],
  );

  const selectedRows = rows.filter((row) => row.selected);
  const invalidSelected = selectedRows.some((row) => !row.validAlias);

  const sync = useMutation({
    mutationFn: (body: BulkModelChannelInput) =>
      api.post<BulkModelChannelResult>(
        `/providers/${providerId}/bulk-model-channels`,
        body,
      ),
    onSuccess: (next) => {
      setResult(next);
      queryClient.invalidateQueries({ queryKey: qk.models });
      queryClient.invalidateQueries({ queryKey: qk.channels });
      toast.success(
        `已创建 ${next.created_models} 个模型、${next.created_channels} 条通道`,
      );
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "同步模型失败"),
  });

  function patchDraft(id: string, patch: Draft) {
    setDrafts((prev) => ({
      ...prev,
      [id]: { ...(prev[id] ?? {}), ...patch },
    }));
  }

  function selectMissing() {
    setDrafts((prev) => {
      const next = { ...prev };
      for (const row of rows) {
        if (row.status !== "bound") {
          next[row.upstream.id] = {
            ...(next[row.upstream.id] ?? {}),
            selected: true,
            alias: row.alias,
          };
        }
      }
      return next;
    });
  }

  function clearSelection() {
    setDrafts((prev) => {
      const next = { ...prev };
      for (const id of Object.keys(next)) {
        next[id] = { ...next[id], selected: false };
      }
      return next;
    });
  }

  function runSync() {
    if (!providerId) return;
    if (selectedRows.length === 0) {
      toast.error("请选择要同步的模型");
      return;
    }
    if (invalidSelected) {
      toast.error("别名只能包含字母、数字、_、.、-、/");
      return;
    }
    sync.mutate({
      items: selectedRows.map((row) => ({
        upstream_model: row.upstream.id,
        alias: row.alias,
        display_name: row.upstream.display_name || row.alias,
      })),
      weight,
      priority,
      enabled: true,
    });
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-3xl">
        <SheetHeader>
          <SheetTitle>同步模型</SheetTitle>
          <SheetDescription>
            {provider
              ? `从 ${provider.name} 拉取上游模型，批量创建别名并绑定通道。`
              : "选择供应商后可同步模型。"}
          </SheetDescription>
        </SheetHeader>

        <div className="mt-5 space-y-4">
          <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
            <div className="min-w-0 flex-1 space-y-2">
              <Label>搜索模型</Label>
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="搜索上游模型 ID"
              />
            </div>
            <div className="grid grid-cols-2 gap-2 lg:w-44">
              <div className="space-y-2">
                <Label>权重</Label>
                <Input
                  type="number"
                  min={0}
                  value={weight}
                  onChange={(e) => setWeight(Number(e.target.value) || 0)}
                />
              </div>
              <div className="space-y-2">
                <Label>优先级</Label>
                <Input
                  type="number"
                  value={priority}
                  onChange={(e) => setPriority(Number(e.target.value) || 0)}
                />
              </div>
            </div>
            <Button
              type="button"
              variant="outline"
              size="icon"
              aria-label="刷新上游模型"
              disabled={!providerId || upstreamModels.isFetching}
              onClick={() => upstreamModels.refetch()}
            >
              <RefreshCw
                className={
                  upstreamModels.isFetching
                    ? "size-4 animate-spin"
                    : "size-4"
                }
              />
            </Button>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="text-xs text-muted-foreground">
              已选择 {selectedRows.length} 个，列表 {rows.length} 个
            </div>
            <div className="flex gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={selectMissing}
                disabled={rows.length === 0}
              >
                选择未绑定
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={clearSelection}
                disabled={selectedRows.length === 0}
              >
                清空
              </Button>
            </div>
          </div>

          <div className="max-h-[52vh] overflow-auto rounded-md border">
            {upstreamModels.isLoading ? (
              <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
                <Loader2 className="mr-2 size-4 animate-spin" />
                正在拉取模型...
              </div>
            ) : upstreamModels.isError ? (
              <div className="p-4 text-sm text-destructive">
                无法拉取模型列表，请检查供应商配置或上游 /v1/models 支持。
              </div>
            ) : rows.length === 0 ? (
              <div className="p-4 text-sm text-muted-foreground">
                没有匹配的上游模型。
              </div>
            ) : (
              <div className="divide-y">
                {rows.map((row) => (
                  <div
                    key={row.upstream.id}
                    className="grid gap-3 p-3 md:grid-cols-[24px_minmax(0,1fr)_minmax(180px,240px)_auto]"
                  >
                    <input
                      type="checkbox"
                      className="mt-2 size-4"
                      checked={row.selected}
                      disabled={row.status === "bound"}
                      onChange={(e) =>
                        patchDraft(row.upstream.id, {
                          selected: e.target.checked,
                          alias: row.alias,
                        })
                      }
                      aria-label={`选择 ${row.upstream.id}`}
                    />
                    <div className="min-w-0">
                      <div className="break-all font-mono text-sm">
                        {row.upstream.id}
                      </div>
                      {row.upstream.display_name &&
                        row.upstream.display_name !== row.upstream.id && (
                          <div className="mt-1 text-xs text-muted-foreground">
                            {row.upstream.display_name}
                          </div>
                        )}
                    </div>
                    <Input
                      value={row.alias}
                      onChange={(e) =>
                        patchDraft(row.upstream.id, {
                          alias: e.target.value,
                          selected: row.selected,
                        })
                      }
                      className={
                        row.validAlias
                          ? "font-mono text-xs"
                          : "border-destructive font-mono text-xs"
                      }
                      disabled={row.status === "bound"}
                      aria-label={`${row.upstream.id} 的模型别名`}
                    />
                    <StatusBadge status={row.status} />
                  </div>
                ))}
              </div>
            )}
          </div>

          {result && (
            <div className="rounded-md border bg-muted/30 p-3 text-sm">
              <div className="font-medium">同步结果</div>
              <div className="mt-1 text-xs text-muted-foreground">
                创建模型 {result.created_models} 个，创建通道{" "}
                {result.created_channels} 条，跳过已存在通道{" "}
                {result.skipped_channels} 条。
              </div>
            </div>
          )}

          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={sync.isPending}
            >
              关闭
            </Button>
            <Button
              type="button"
              onClick={runSync}
              disabled={
                sync.isPending ||
                !providerId ||
                selectedRows.length === 0 ||
                invalidSelected
              }
            >
              {sync.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <ListPlus className="size-4" />
              )}
              同步选中模型
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

function StatusBadge({ status }: { status: string }) {
  if (status === "bound") {
    return <Badge variant="secondary">已绑定</Badge>;
  }
  if (status === "alias") {
    return <Badge variant="outline">已有别名</Badge>;
  }
  return <Badge>新模型</Badge>;
}
