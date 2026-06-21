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
    label: "Failover",
    hint: "Strict priority tiers; within a tier, highest weight wins. Deterministic.",
  },
  {
    value: "weighted",
    label: "Weighted",
    hint: "Priority tiers; within a tier, weighted-random selection (heavier = more likely first).",
  },
  {
    value: "auto",
    label: "Auto",
    hint: "Same as weighted today (reserved for smarter selection later).",
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
      form.setError("model_id", { message: "Select a model" });
      return;
    }
    let params: Record<string, unknown> | undefined;
    if (v.params?.trim()) {
      try {
        params = JSON.parse(v.params);
      } catch {
        form.setError("params", { message: "Invalid JSON" });
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
            ? "Edit global policy"
            : "Set global policy"
          : editing
            ? "Edit model override"
            : "Add model override"
      }
      description={
        scope === "global"
          ? "The default routing policy applied to every model without an override."
          : "A per-model routing override, taking precedence over the global policy."
      }
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel={editing ? "Save" : "Save policy"}
    >
      {scope === "model" && (
        <Field
          label="Model"
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
                  <SelectValue placeholder="Select model" />
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

      <Field label="Mode" hint={modeMeta?.hint} error={form.formState.errors.mode?.message}>
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
          <Field label="Enabled">
            <Switch
              checked={field.value}
              onCheckedChange={field.onChange}
              className="mt-1.5"
            />
          </Field>
        )}
      />

      <Field
        label="Params (JSON, optional)"
        hint="Reserved for future router tuning. The router currently ignores this field — weights live on channels."
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
