"use client";

import { useState } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Plus, Trash2 } from "lucide-react";
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
    label: "Default",
    hint: "Applies to every request with no more specific profile.",
  },
  {
    value: "provider",
    label: "Per provider",
    hint: "Applies to all models/channels on a provider.",
  },
  {
    value: "model",
    label: "Per model",
    hint: "Applies to a single model alias.",
  },
];

// Target is required when scope is provider/model. That check is enforced in
// handle() (mirroring provider-form's metadata validation) rather than via a
// zod superRefine, to stay version-agnostic and match the codebase pattern.
const schema = z.object({
  name: z.string().min(1, "Required"),
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

  async function handle(v: Values) {
    if (v.scope !== "default" && !v.target_id) {
      form.setError("target_id", {
        message:
          v.scope === "provider" ? "Select a provider" : "Select a model",
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
      title="New client profile"
      description="Egress impersonation profile — injected headers/UA sent to the upstream on this scope's behalf."
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel="Create profile"
    >
      <Field
        label="Name"
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
        label="Scope"
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
          label={scope === "provider" ? "Target provider" : "Target model"}
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
                          ? "Select provider"
                          : "Select model"
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
                        {o.disabled && " (disabled)"}
                      </SelectItem>
                    ))}
                    {options.length === 0 && (
                      <div className="px-2 py-1.5 text-xs text-muted-foreground">
                        {scope === "provider"
                          ? "No providers configured."
                          : "No models configured."}
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
        Impersonation
      </p>
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
        hint="Extra request headers injected on egress. Empty keys are ignored."
      >
        <div className="space-y-2">
          {headerRows.map((row, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                value={row.key}
                onChange={(e) => updateHeaderRow(i, { key: e.target.value })}
                placeholder="header"
                className="font-mono text-xs"
              />
              <Input
                value={row.value}
                onChange={(e) =>
                  updateHeaderRow(i, { value: e.target.value })
                }
                placeholder="value"
                className="font-mono text-xs"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Remove header"
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
            Add header
          </Button>
        </div>
      </Field>

      <div className="grid grid-cols-2 gap-3 pt-1">
        <Controller
          control={form.control}
          name="strip_client_headers"
          render={({ field }) => (
            <Field
              label="Strip client headers"
              hint="Drop inbound client headers before impersonation."
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
            <Field label="Enabled">
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
