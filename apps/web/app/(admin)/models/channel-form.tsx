"use client";

import { useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Controller, useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { RefreshCw } from "lucide-react";
import { FormSheet } from "@/components/form-sheet";
import { Field } from "@/components/field";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { api } from "@/lib/api";
import { qk } from "@/lib/query-keys";
import type { ModelChannel, Provider, UpstreamModel } from "@/lib/types";

const schema = z.object({
  provider_id: z.string().min(1, "请选择供应商"),
  upstream_model: z.string().min(1, "必填"),
  weight: z.number().int().min(0),
  priority: z.number().int(),
  enabled: z.boolean(),
});

type Values = z.infer<typeof schema>;
const NUM = { valueAsNumber: true } as const;

const EMPTY: Values = {
  provider_id: "",
  upstream_model: "",
  weight: 1,
  priority: 0,
  enabled: true,
};

export function ChannelForm({
  open,
  onOpenChange,
  onSubmit,
  submitting,
  providers,
  providersLoading,
  modelAlias,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (body: Partial<ModelChannel>) => Promise<void>;
  submitting: boolean;
  providers: Provider[];
  providersLoading: boolean;
  modelAlias: string | null;
}) {
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY,
  });
  const selectedProviderID = useWatch({
    control: form.control,
    name: "provider_id",
  });
  const upstreamModels = useQuery({
    queryKey: qk.upstreamModels(selectedProviderID),
    queryFn: () =>
      api.list<UpstreamModel>(`/providers/${selectedProviderID}/upstream-models`),
    enabled: open && Boolean(selectedProviderID),
    retry: false,
  });

  // Providers are create-only enabled ones first, then the rest, for ergonomics.
  const orderedProviders = useMemo(
    () =>
      [...providers].sort((a, b) => {
        if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
        return a.name.localeCompare(b.name);
      }),
    [providers],
  );

  useEffect(() => {
    if (open) form.reset(EMPTY);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  async function handle(v: Values) {
    await onSubmit({
      provider_id: v.provider_id,
      upstream_model: v.upstream_model.trim(),
      weight: v.weight,
      priority: v.priority,
      enabled: v.enabled,
    });
  }

  return (
    <FormSheet
      open={open}
      onOpenChange={onOpenChange}
      title="绑定通道"
      description={
        modelAlias
          ? `将别名“${modelAlias}”路由到上游模型。`
          : "将模型别名路由到上游模型。"
      }
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel="绑定通道"
    >
      <Field
        label="供应商"
        required
        error={form.formState.errors.provider_id?.message}
      >
        <Controller
          control={form.control}
          name="provider_id"
          render={({ field }) => (
            <Select
              value={field.value}
              onValueChange={(value) => {
                field.onChange(value);
                form.setValue("upstream_model", "");
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue
                  placeholder={
                    providersLoading ? "正在加载供应商..." : "选择供应商"
                  }
                />
              </SelectTrigger>
              <SelectContent>
                {orderedProviders.map((p) => (
                  <SelectItem key={p.id} value={p.id ?? ""} disabled={!p.enabled}>
                    {p.name}
                    {!p.enabled && "（已停用）"}
                  </SelectItem>
                ))}
                {orderedProviders.length === 0 && (
                  <div className="px-2 py-1.5 text-xs text-muted-foreground">
                    暂无供应商配置。
                  </div>
                )}
              </SelectContent>
            </Select>
          )}
        />
      </Field>

      <Field
        label="上游模型"
        required
        error={form.formState.errors.upstream_model?.message}
        hint="会优先从供应商 /v1/models 拉取；拉取失败时仍可手动输入。"
      >
        <Controller
          control={form.control}
          name="upstream_model"
          render={({ field }) => (
            <div className="space-y-2">
              <div className="flex gap-2">
                <Select
                  value={field.value}
                  onValueChange={field.onChange}
                  disabled={
                    !selectedProviderID ||
                    upstreamModels.isLoading ||
                    (upstreamModels.data ?? []).length === 0
                  }
                >
                  <SelectTrigger className="min-w-0 flex-1">
                    <SelectValue
                      placeholder={
                        !selectedProviderID
                          ? "先选择供应商"
                          : upstreamModels.isLoading
                            ? "正在拉取模型..."
                            : upstreamModels.isError
                              ? "拉取失败，可手动输入"
                              : (upstreamModels.data ?? []).length === 0
                                ? "未发现模型，可手动输入"
                                : "选择上游模型"
                      }
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {(upstreamModels.data ?? []).map((m) => (
                      <SelectItem key={m.id} value={m.id}>
                        <span className="font-mono">{m.id}</span>
                        {m.display_name && m.display_name !== m.id
                          ? ` · ${m.display_name}`
                          : ""}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label="刷新上游模型"
                  disabled={!selectedProviderID || upstreamModels.isFetching}
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
              <Input
                value={field.value}
                onChange={field.onChange}
                placeholder="也可手动输入，例如 gpt-4o-2024-08-06"
                autoComplete="off"
              />
              {upstreamModels.isError && (
                <p className="text-xs text-destructive">
                  无法从该供应商拉取模型列表，请检查基础 URL、API 密钥或上游是否支持 /v1/models。
                </p>
              )}
            </div>
          )}
        />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="权重" hint="越高分配到的流量越多（weighted 模式）。">
          <Input type="number" {...form.register("weight", NUM)} />
        </Field>
        <Field label="优先级" hint="越低越先尝试（failover 分层）。">
          <Input type="number" {...form.register("priority", NUM)} />
        </Field>
      </div>

      <Controller
        control={form.control}
        name="enabled"
        render={({ field }) => (
          <Field label="启用">
            <Switch
              checked={field.value}
              onCheckedChange={field.onChange}
              className="mt-1.5"
            />
          </Field>
        )}
      />
    </FormSheet>
  );
}
