"use client";

import { useState } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Plus, Sparkles, Trash2 } from "lucide-react";
import { FormSheet } from "@/components/form-sheet";
import { Field } from "@/components/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ClientProfile, Model, Provider } from "@/lib/types";

type Scope = "default" | "provider" | "model";

const SCOPES: { value: Scope; label: string; hint: string }[] = [
  {
    value: "default",
    label: "默认",
    hint: "没有更具体配置时应用到所有请求。",
  },
  {
    value: "provider",
    label: "按供应商",
    hint: "应用到某个供应商下的所有模型/通道。",
  },
  {
    value: "model",
    label: "按模型",
    hint: "应用到单个模型别名。",
  },
];

// Target is required when scope is provider/model. That check is enforced in
// handle() (mirroring provider-form's metadata validation) rather than via a
// zod superRefine, to stay version-agnostic and match the codebase pattern.
const schema = z.object({
  name: z.string().min(1, "必填"),
  scope: z.enum(["default", "provider", "model"]),
  target_id: z.string().optional(),
  user_agent: z.string().optional(),
  origin: z.string().optional(),
  referer: z.string().optional(),
  strip_client_headers: z.boolean(),
  enabled: z.boolean(),
});

type Values = z.infer<typeof schema>;

const EMPTY: Values = {
  name: "",
  scope: "default",
  target_id: "",
  user_agent: "",
  origin: "",
  referer: "",
  strip_client_headers: false,
  enabled: true,
};

type HeaderRow = { key: string; value: string };

type HeaderPreset = {
  id: string;
  label: string;
  name: string;
  userAgent: string;
  origin?: string;
  referer?: string;
  headers: HeaderRow[];
};

const HEADER_PRESETS: HeaderPreset[] = [
  {
    id: "codex-cli",
    label: "Codex CLI",
    name: "Codex CLI",
    userAgent: "codex-cli/0.1.0 (external, cli)",
    headers: [
      { key: "OpenAI-Beta", value: "responses=v1" },
      { key: "X-Client-Name", value: "codex-cli" },
    ],
  },
  {
    id: "claude-code",
    label: "Claude Code",
    name: "Claude Code",
    userAgent: "claude-cli/1.0 (external, cli)",
    headers: [
      { key: "anthropic-version", value: "2023-06-01" },
      { key: "anthropic-beta", value: "claude-code-20250219" },
    ],
  },
];

export function ProfileForm({
  open,
  onOpenChange,
  onSubmit,
  submitting,
  providers,
  models,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (body: Partial<ClientProfile>) => Promise<void>;
  submitting: boolean;
  providers: Provider[];
  models: Model[];
}) {
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY,
  });

  // Headers are a dynamic k-v map; managed as a simple row list outside RHF
  // and folded into a Record on submit. The parent remounts this form (via a
  // `key`) on each open, so form + rows start fresh without a reset effect.
  const [headerRows, setHeaderRows] = useState<HeaderRow[]>([
    { key: "", value: "" },
  ]);

  const scope = useWatch({ control: form.control, name: "scope" });

  function updateHeaderRow(i: number, patch: Partial<HeaderRow>) {
    setHeaderRows((rows) =>
      rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)),
    );
  }
  function removeHeaderRow(i: number) {
    setHeaderRows((rows) => rows.filter((_, idx) => idx !== i));
  }
  function addHeaderRow() {
    setHeaderRows((rows) => [...rows, { key: "", value: "" }]);
  }
  function applyPreset(preset: HeaderPreset) {
    const currentName = form.getValues("name").trim();
    if (!currentName) form.setValue("name", preset.name);
    form.setValue("user_agent", preset.userAgent);
    form.setValue("origin", preset.origin ?? "");
    form.setValue("referer", preset.referer ?? "");
    form.setValue("strip_client_headers", true);
    setHeaderRows((rows) => mergeHeaderRows(rows, preset.headers));
  }

  async function handle(v: Values) {
    if (v.scope !== "default" && !v.target_id) {
      form.setError("target_id", {
        message:
          v.scope === "provider" ? "请选择供应商" : "请选择模型",
      });
      return;
    }
    const headers: Record<string, string> = {};
    for (const r of headerRows) {
      const k = r.key.trim();
      if (k) headers[k] = r.value;
    }
    await onSubmit({
      name: v.name,
      scope: v.scope,
      // default scope must not carry a target; backend rejects it implicitly.
      target_id:
        v.scope === "default" || !v.target_id ? undefined : v.target_id,
      user_agent: v.user_agent?.trim() || undefined,
      origin: v.origin?.trim() || undefined,
      referer: v.referer?.trim() || undefined,
      strip_client_headers: v.strip_client_headers,
      enabled: v.enabled,
      headers,
    });
  }

  const scopeMeta = SCOPES.find((s) => s.value === scope);

  return (
    <FormSheet
      open={open}
      onOpenChange={onOpenChange}
      title="新建客户端伪装"
      description="在指定作用域内，为发往上游的请求注入 Header / UA。"
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel="创建配置"
    >
      <Field
        label="名称"
        required
        error={form.formState.errors.name?.message}
      >
        <Input
          {...form.register("name")}
          placeholder="openai-browser"
          autoComplete="off"
        />
      </Field>

      <Field
        label="作用域"
        hint={scopeMeta?.hint}
      error={form.formState.errors.scope?.message}
      >
        <Controller
          control={form.control}
          name="scope"
          render={({ field }) => (
            <Select
              value={field.value}
              onValueChange={(v) => {
                field.onChange(v);
                form.setValue("target_id", "");
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SCOPES.map((s) => (
                  <SelectItem key={s.value} value={s.value}>
                    {s.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </Field>

      {scope !== "default" && (
        <Field
          label={scope === "provider" ? "目标供应商" : "目标模型"}
          required
          error={form.formState.errors.target_id?.message}
        >
          <Controller
            control={form.control}
            name="target_id"
            render={({ field }) => {
              const options =
                scope === "provider"
                  ? providers.map((p) => ({
                      value: p.id ?? "",
                      label: p.name,
                      disabled: !p.enabled,
                    }))
                  : models.map((m) => ({
                      value: m.id ?? "",
                      label: m.display_name || m.alias,
                      disabled: !m.enabled,
                    }));
              return (
                <Select
                  value={field.value}
                  onValueChange={field.onChange}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue
                      placeholder={
                        scope === "provider"
                          ? "选择供应商"
                          : "选择模型"
                      }
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {options.map((o) => (
                      <SelectItem
                        key={o.value}
                        value={o.value}
                        disabled={o.disabled}
                      >
                        {o.label}
                        {o.disabled && "（已停用）"}
                      </SelectItem>
                    ))}
                    {options.length === 0 && (
                      <div className="px-2 py-1.5 text-xs text-muted-foreground">
                        {scope === "provider"
                          ? "暂无供应商配置。"
                          : "暂无模型配置。"}
                      </div>
                    )}
                  </SelectContent>
                </Select>
              );
            }}
          />
        </Field>
      )}

      <p className="pt-2 text-xs font-medium text-muted-foreground">
        伪装内容
      </p>
      <div className="rounded-lg border bg-muted/20 p-3">
        <div className="mb-2 flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Sparkles className="size-3.5" />
          常用 CLI 预设
        </div>
        <div className="flex flex-wrap gap-2">
          {HEADER_PRESETS.map((preset) => (
            <Button
              key={preset.id}
              type="button"
              variant="outline"
              size="sm"
              onClick={() => applyPreset(preset)}
            >
              {preset.label}
            </Button>
          ))}
        </div>
      </div>
      <Field label="User-Agent">
        <Input
          {...form.register("user_agent")}
          placeholder="Mozilla/5.0 …"
          autoComplete="off"
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Origin">
          <Input
            {...form.register("origin")}
            placeholder="https://chat.openai.com"
            autoComplete="off"
          />
        </Field>
        <Field label="Referer">
          <Input
            {...form.register("referer")}
            placeholder="https://chat.openai.com/"
            autoComplete="off"
          />
        </Field>
      </div>

      <Field
        label="Headers"
        hint="出站请求额外注入的 Header。空键会被忽略。"
      >
        <div className="space-y-2">
          {headerRows.map((row, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                value={row.key}
                onChange={(e) => updateHeaderRow(i, { key: e.target.value })}
                placeholder="Header 名称"
                className="font-mono text-xs"
              />
              <Input
                value={row.value}
                onChange={(e) =>
                  updateHeaderRow(i, { value: e.target.value })
                }
                placeholder="Header 值"
                className="font-mono text-xs"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="移除 Header"
                onClick={() => removeHeaderRow(i)}
                disabled={headerRows.length === 1}
              >
                <Trash2 className="size-3.5 text-muted-foreground" />
              </Button>
            </div>
          ))}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={addHeaderRow}
          >
            <Plus className="size-3.5" />
            添加 Header
          </Button>
        </div>
      </Field>

      <div className="grid grid-cols-2 gap-3 pt-1">
        <Controller
          control={form.control}
          name="strip_client_headers"
          render={({ field }) => (
            <Field
              label="剥离客户端 Header"
              hint="执行伪装前丢弃入站客户端 Header。"
            >
              <Switch
                checked={field.value}
                onCheckedChange={field.onChange}
                className="mt-1.5"
              />
            </Field>
          )}
        />
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
    </FormSheet>
  );
}

function mergeHeaderRows(existing: HeaderRow[], preset: HeaderRow[]) {
  const merged = existing
    .map((row) => ({ key: row.key.trim(), value: row.value }))
    .filter((row) => row.key !== "");
  for (const header of preset) {
    const idx = merged.findIndex(
      (row) => row.key.toLowerCase() === header.key.toLowerCase(),
    );
    if (idx >= 0) {
      merged[idx] = header;
    } else {
      merged.push(header);
    }
  }
  return merged.length > 0 ? merged : [{ key: "", value: "" }];
}
