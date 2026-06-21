"use client";

import { useEffect } from "react";
import { Controller, useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { FormSheet } from "@/components/form-sheet";
import { Field } from "@/components/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { Provider } from "@/lib/types";

const PROTOCOLS = [
  { value: "openai_chat", label: "OpenAI Chat" },
  { value: "anthropic_messages", label: "Anthropic Messages" },
  { value: "openai_responses", label: "OpenAI Responses (Codex)" },
];

const schema = z.object({
  name: z.string().min(1, "必填"),
  protocol: z.string().min(1),
  base_url: z.string().min(1, "必填"),
  api_key: z.string().optional(),
  timeout_ms: z.number().int().positive(),
  connect_timeout_ms: z.number().int().positive(),
  max_retries: z.number().int().min(0),
  weight: z.number().int().min(0),
  priority: z.number().int(),
  hc_error_rate: z.number().min(0).max(1),
  hc_p95_ttft_ms: z.number().int().positive(),
  hc_window_sec: z.number().int().positive(),
  hc_cooldown_sec: z.number().int().positive(),
  enabled: z.boolean(),
  metadata: z.string().optional(),
});

type Values = z.infer<typeof schema>;
const NUM = { valueAsNumber: true } as const;

const EMPTY: Values = {
  name: "",
  protocol: "openai_chat",
  base_url: "",
  api_key: "",
  timeout_ms: 60000,
  connect_timeout_ms: 10000,
  max_retries: 2,
  weight: 1,
  priority: 0,
  hc_error_rate: 0.3,
  hc_p95_ttft_ms: 8000,
  hc_window_sec: 60,
  hc_cooldown_sec: 30,
  enabled: true,
  metadata: "",
};

function toValues(p: Provider | null): Values {
  if (!p) return EMPTY;
  return {
    ...EMPTY,
    ...p,
    api_key: "",
    metadata: p.metadata ? JSON.stringify(p.metadata, null, 2) : "",
  };
}

export function ProviderForm({
  provider,
  open,
  onOpenChange,
  onSubmit,
  submitting,
}: {
  provider: Provider | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (body: Partial<Provider>) => Promise<void>;
  submitting: boolean;
}) {
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY,
  });

  useEffect(() => {
    if (open) form.reset(toValues(provider));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, provider]);

  async function handle(v: Values) {
    let metadata: Record<string, unknown> | undefined;
    if (v.metadata?.trim()) {
      try {
        metadata = JSON.parse(v.metadata);
      } catch {
        form.setError("metadata", { message: "JSON 格式无效" });
        return;
      }
    }
    await onSubmit({
      name: v.name,
      protocol: v.protocol,
      base_url: v.base_url,
      api_key: v.api_key || undefined,
      timeout_ms: v.timeout_ms,
      connect_timeout_ms: v.connect_timeout_ms,
      max_retries: v.max_retries,
      weight: v.weight,
      priority: v.priority,
      hc_error_rate: v.hc_error_rate,
      hc_p95_ttft_ms: v.hc_p95_ttft_ms,
      hc_window_sec: v.hc_window_sec,
      hc_cooldown_sec: v.hc_cooldown_sec,
      enabled: v.enabled,
      metadata,
    });
  }

  return (
    <FormSheet
      open={open}
      onOpenChange={onOpenChange}
      title={provider ? "编辑供应商" : "新建供应商"}
      description="供应商配置会热加载到路由，无需重启网关。"
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel={provider ? "保存修改" : "创建供应商"}
    >
      <Field label="名称" required error={form.formState.errors.name?.message}>
        <Input {...form.register("name")} placeholder="OpenAI 官方" />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="协议" required>
          <Controller
            control={form.control}
            name="protocol"
            render={({ field }) => (
              <Select value={field.value} onValueChange={field.onChange}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PROTOCOLS.map((p) => (
                    <SelectItem key={p.value} value={p.value}>
                      {p.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
        </Field>
        <Field label="优先级">
          <Input type="number" {...form.register("priority", NUM)} />
        </Field>
      </div>

      <Field label="基础 URL" required error={form.formState.errors.base_url?.message}>
        <Input
          {...form.register("base_url")}
          placeholder="https://api.openai.com"
        />
      </Field>

      <Field
        label="API 密钥"
        hint={provider ? "留空表示保留现有密钥。" : undefined}
      >
        <Input
          type="password"
          {...form.register("api_key")}
          placeholder="sk-…"
          autoComplete="off"
        />
      </Field>

      <div className="grid grid-cols-3 gap-3">
        <Field label="权重">
          <Input type="number" {...form.register("weight", NUM)} />
        </Field>
        <Field label="最大重试">
          <Input type="number" {...form.register("max_retries", NUM)} />
        </Field>
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
      </div>

      <div className="grid grid-cols-2 gap-3">
        <Field label="超时 (ms)">
          <Input type="number" {...form.register("timeout_ms", NUM)} />
        </Field>
        <Field label="连接超时 (ms)">
          <Input type="number" {...form.register("connect_timeout_ms", NUM)} />
        </Field>
      </div>

      <p className="pt-2 text-xs font-medium text-muted-foreground">
        熔断阈值
      </p>
      <div className="grid grid-cols-2 gap-3">
        <Field label="错误率" hint="0-1">
          <Input
            type="number"
            step="0.05"
            {...form.register("hc_error_rate", NUM)}
          />
        </Field>
        <Field label="p95 首字延迟 (ms)">
          <Input type="number" {...form.register("hc_p95_ttft_ms", NUM)} />
        </Field>
        <Field label="窗口 (s)">
          <Input type="number" {...form.register("hc_window_sec", NUM)} />
        </Field>
        <Field label="冷却 (s)">
          <Input type="number" {...form.register("hc_cooldown_sec", NUM)} />
        </Field>
      </div>

      <Field label="元数据 (JSON)" error={form.formState.errors.metadata?.message}>
        <Textarea
          {...form.register("metadata")}
          rows={3}
          className="font-mono text-xs"
          placeholder='{"key":"value"}'
        />
      </Field>
    </FormSheet>
  );
}
