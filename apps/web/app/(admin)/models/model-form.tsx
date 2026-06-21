"use client";

import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { FormSheet } from "@/components/form-sheet";
import { Field } from "@/components/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { Controller } from "react-hook-form";
import type { Model } from "@/lib/types";

// The backend has no PUT /models, so models are create-only; edit is
// delete-and-recreate. Display name defaults to the alias server-side.
const schema = z.object({
  alias: z
    .string()
    .min(1, "Required")
    .regex(/^[a-zA-Z0-9_.\-\/]+$/, "Letters, digits, _ . - / only"),
  display_name: z.string().optional(),
  description: z.string().optional(),
  enabled: z.boolean(),
});

type Values = z.infer<typeof schema>;

const EMPTY: Values = {
  alias: "",
  display_name: "",
  description: "",
  enabled: true,
};

export function ModelForm({
  open,
  onOpenChange,
  onSubmit,
  submitting,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (body: Partial<Model>) => Promise<void>;
  submitting: boolean;
}) {
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY,
  });

  useEffect(() => {
    if (open) form.reset(EMPTY);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  async function handle(v: Values) {
    await onSubmit({
      alias: v.alias,
      display_name: v.display_name?.trim() || undefined,
      description: v.description?.trim() || undefined,
      enabled: v.enabled,
    });
  }

  return (
    <FormSheet
      open={open}
      onOpenChange={onOpenChange}
      title="New model"
      description="Create a model alias. Then bind upstream channels to it."
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel="Create model"
    >
      <Field
        label="Alias"
        required
        error={form.formState.errors.alias?.message}
        hint="The name clients request, e.g. gpt-4o or claude-3.5-sonnet."
      >
        <Input
          {...form.register("alias")}
          placeholder="gpt-4o"
          autoComplete="off"
        />
      </Field>

      <Field
        label="Display name"
        hint="Optional. Defaults to the alias if left blank."
      >
        <Input
          {...form.register("display_name")}
          placeholder="GPT-4o"
        />
      </Field>

      <Field label="Description">
        <Textarea
          {...form.register("description")}
          rows={3}
          placeholder="What this alias is for…"
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
    </FormSheet>
  );
}
