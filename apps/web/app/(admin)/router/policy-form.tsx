"use client";

import { Controller, useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { FormSheet } from "@/components/form-sheet";
import { Field } from "@/components/field";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { RouterPolicy } from "@/lib/types";

type PolicyMode = "failover" | "weighted" | "auto";

const MODES: { value: PolicyMode; label: string; hint: string }[] = [
  {
    value: "failover",
    label: "故障转移",
    hint: "严格按优先级分层；同层内优先选择权重最高的通道。",
  },
  {
    value: "weighted",
    label: "加权",
    hint: "按优先级分层；同层内按权重随机选择，权重越高越容易优先命中。",
  },
  {
    value: "auto",
    label: "自动",
    hint: "当前等同于加权，后续保留给更智能的选择策略。",
  },
];

const schema = z.object({
  mode: z.enum(["failover", "weighted", "auto"]),
  model_id: z.string().optional(),
  params: z.string().optional(),
  enabled: z.boolean(),
});

type Values = z.infer<typeof schema>;

const EMPTY: Values = {
  mode: "failover",
  model_id: "",
  params: "",
  enabled: true,
};

function toValues(p: RouterPolicy | null): Values {
  if (!p) return EMPTY;
  return {
    mode: (p.mode as PolicyMode) ?? "failover",
    model_id: p.model_id ?? "",
    params: p.params ? JSON.stringify(p.params, null, 2) : "",
    enabled: p.enabled,
  };
}

export function PolicyForm({
  open,
  onOpenChange,
  onSubmit,
  submitting,
  scope,
  // For scope=model: the policy being edited (if any), and the models that can
  // be targeted (those without an existing override, plus the one being edited).
  editing,
  modelOptions,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (body: Partial<RouterPolicy>) => Promise<void>;
  submitting: boolean;
  scope: "global" | "model";
  editing: RouterPolicy | null;
  // {id,label} entries selectable as the target (excludes models that already
  // have an override, except the one currently being edited).
  modelOptions: { id: string; label: string }[];
}) {
  // Seed from `editing` at mount time. The parent bumps `key` on each open, so
  // every open = a fresh mount with these defaultValues (the existing policy
  // when editing, EMPTY when creating). No reset effect needed.
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: toValues(editing),
  });

  const mode = useWatch({ control: form.control, name: "mode" });

  async function handle(v: Values) {
    if (scope === "model" && !v.model_id) {
      form.setError("model_id", { message: "请选择模型" });
      return;
    }
    let params: Record<string, unknown> | undefined;
    if (v.params?.trim()) {
      try {
        params = JSON.parse(v.params);
      } catch {
        form.setError("params", { message: "JSON 格式无效" });
        return;
      }
    }
    await onSubmit({
      scope,
      mode: v.mode,
      model_id: scope === "model" ? v.model_id : undefined,
      params,
      enabled: v.enabled,
    });
  }

  const modeMeta = MODES.find((m) => m.value === mode);

  return (
    <FormSheet
      open={open}
      onOpenChange={onOpenChange}
      title={
        scope === "global"
          ? editing
            ? "编辑全局策略"
            : "设置全局策略"
          : editing
            ? "编辑模型覆盖策略"
            : "添加模型覆盖策略"
      }
      description={
        scope === "global"
          ? "默认路由策略，应用于所有没有覆盖策略的模型。"
          : "单模型路由覆盖策略，优先级高于全局策略。"
      }
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel={editing ? "保存" : "保存策略"}
    >
      {scope === "model" && (
        <Field
          label="模型"
          required
          error={form.formState.errors.model_id?.message}
        >
          <Controller
            control={form.control}
            name="model_id"
            render={({ field }) => (
              <Select
                value={field.value}
                onValueChange={field.onChange}
                disabled={!!editing}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择模型" />
                </SelectTrigger>
                <SelectContent>
                  {modelOptions.map((m) => (
                    <SelectItem key={m.id} value={m.id}>
                      {m.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
        </Field>
      )}

      <Field label="模式" hint={modeMeta?.hint} error={form.formState.errors.mode?.message}>
        <Controller
          control={form.control}
          name="mode"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {MODES.map((m) => (
                  <SelectItem key={m.value} value={m.value}>
                    {m.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
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

      <Field
        label="参数（JSON，可选）"
        hint="预留给后续路由调优。目前路由器会忽略该字段，权重仍配置在通道上。"
        error={form.formState.errors.params?.message}
      >
        <Textarea
          {...form.register("params")}
          rows={3}
          className="font-mono text-xs"
          placeholder='{}'
        />
      </Field>
    </FormSheet>
  );
}
