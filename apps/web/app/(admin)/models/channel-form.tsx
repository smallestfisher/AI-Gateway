"use client";

import { useEffect, useMemo } from "react";
import { Controller, useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { FormSheet } from "@/components/form-sheet";
import { Field } from "@/components/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ModelChannel, Provider } from "@/lib/types";

const schema = z.object({
  provider_id: z.string().min(1, "Select a provider"),
  upstream_model: z.string().min(1, "Required"),
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
      title="Bind channel"
      description={
        modelAlias
          ? `Route the alias “${modelAlias}” to an upstream model.`
          : "Route a model alias to an upstream model."
      }
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel="Bind channel"
    >
      <Field
        label="Provider"
        required
        error={form.formState.errors.provider_id?.message}
      >
        <Controller
          control={form.control}
          name="provider_id"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger className="w-full">
                <SelectValue
                  placeholder={
                    providersLoading ? "Loading providers…" : "Select provider"
                  }
                />
              </SelectTrigger>
              <SelectContent>
                {orderedProviders.map((p) => (
                  <SelectItem key={p.id} value={p.id ?? ""} disabled={!p.enabled}>
                    {p.name}
                    {!p.enabled && " (disabled)"}
                  </SelectItem>
                ))}
                {orderedProviders.length === 0 && (
                  <div className="px-2 py-1.5 text-xs text-muted-foreground">
                    No providers configured.
                  </div>
                )}
              </SelectContent>
            </Select>
          )}
        />
      </Field>

      <Field
        label="Upstream model"
        required
        error={form.formState.errors.upstream_model?.message}
        hint="The exact model id the provider expects, e.g. gpt-4o-2024-08-06."
      >
        <Input
          {...form.register("upstream_model")}
          placeholder="gpt-4o-2024-08-06"
          autoComplete="off"
        />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="Weight" hint="Higher = more traffic (weighted mode).">
          <Input type="number" {...form.register("weight", NUM)} />
        </Field>
        <Field label="Priority" hint="Lower = tried first (failover tiers).">
          <Input type="number" {...form.register("priority", NUM)} />
        </Field>
      </div>

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
    </FormSheet>
  );
}
