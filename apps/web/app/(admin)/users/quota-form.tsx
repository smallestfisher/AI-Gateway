"use client";

import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { User } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Field } from "@/components/field";

const quotaSchema = z.object({
  balance: z.number().int().min(0),
  rpm: z.number().int().min(0),
  tpm: z.number().int().min(0),
  whitelist: z.string(),
});

type QuotaFormData = z.infer<typeof quotaSchema>;

interface QuotaFormProps {
  user: User;
  onSubmit: (data: { balance: number; rpm: number; tpm: number; whitelist: string[] }) => void;
  onCancel: () => void;
}

export function QuotaForm({ user, onSubmit, onCancel }: QuotaFormProps) {
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<QuotaFormData>({
    resolver: zodResolver(quotaSchema),
    defaultValues: {
      balance: user.balance || 0,
      rpm: user.rpm || 0,
      tpm: user.tpm || 0,
      whitelist: user.whitelist?.join("\n") || "",
    },
  });

  const handleFormSubmit = (data: QuotaFormData) => {
    const whitelist = data.whitelist
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    onSubmit({
      balance: data.balance,
      rpm: data.rpm,
      tpm: data.tpm,
      whitelist,
    });
  };

  return (
    <form onSubmit={handleSubmit(handleFormSubmit)} className="space-y-4">
      <Field
        label="Balance"
        error={errors.balance?.message}
        hint="Token balance (total available tokens)"
      >
        <Input
          {...register("balance", { valueAsNumber: true })}
          type="number"
          placeholder="0"
          min="0"
        />
      </Field>

      <Field
        label="RPM Limit"
        error={errors.rpm?.message}
        hint="Requests per minute (0 = unlimited)"
      >
        <Input
          {...register("rpm", { valueAsNumber: true })}
          type="number"
          placeholder="0"
          min="0"
        />
      </Field>

      <Field
        label="TPM Limit"
        error={errors.tpm?.message}
        hint="Tokens per minute (0 = unlimited)"
      >
        <Input
          {...register("tpm", { valueAsNumber: true })}
          type="number"
          placeholder="0"
          min="0"
        />
      </Field>

      <Field
        label="Model Whitelist"
        error={errors.whitelist?.message}
        hint="One model alias per line (empty = all models allowed)"
      >
        <Textarea
          {...register("whitelist")}
          placeholder="claude-sonnet&#10;gpt-4o&#10;deepseek-r1"
          rows={5}
        />
      </Field>

      <div className="flex justify-end gap-2 pt-4">
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? "Saving..." : "Update Quota"}
        </Button>
      </div>
    </form>
  );
}
