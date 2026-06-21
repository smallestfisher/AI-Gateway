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
    .min(1, "必填")
    .regex(/^[a-zA-Z0-9_.\-\/]+$/, "仅支持字母、数字、_、.、-、/"),
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
      title="新建模型"
      description="创建模型别名后，再为它绑定上游通道。"
      onSubmit={form.handleSubmit(handle)}
      submitting={submitting}
      submitLabel="创建模型"
    >
      <Field
        label="别名"
        required
        error={form.formState.errors.alias?.message}
        hint="客户端请求的模型名称，例如 gpt-4o 或 claude-3.5-sonnet。"
      >
        <Input
          {...form.register("alias")}
          placeholder="gpt-4o"
          autoComplete="off"
        />
      </Field>

      <Field
        label="显示名称"
        hint="可选。留空时默认使用别名。"
      >
        <Input
          {...form.register("display_name")}
          placeholder="GPT-4o"
        />
      </Field>

      <Field label="描述">
        <Textarea
          {...form.register("description")}
          rows={3}
          placeholder="这个别名的用途..."
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
    </FormSheet>
  );
}
