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
        label="余额"
        error={errors.balance?.message}
        hint="Token 余额（可用总 token）"
      >
        <Input
          {...register("balance", { valueAsNumber: true })}
          type="number"
          placeholder="0"
          min="0"
        />
      </Field>

      <Field
        label="RPM 限制"
        error={errors.rpm?.message}
        hint="每分钟请求数（0 表示不限制）"
      >
        <Input
          {...register("rpm", { valueAsNumber: true })}
          type="number"
          placeholder="0"
          min="0"
        />
      </Field>

      <Field
        label="TPM 限制"
        error={errors.tpm?.message}
        hint="每分钟 token 数（0 表示不限制）"
      >
        <Input
          {...register("tpm", { valueAsNumber: true })}
          type="number"
          placeholder="0"
          min="0"
        />
      </Field>

      <Field
        label="模型白名单"
        error={errors.whitelist?.message}
        hint="每行一个模型别名；留空表示允许全部模型"
      >
        <Textarea
          {...register("whitelist")}
          placeholder="claude-sonnet&#10;gpt-4o&#10;deepseek-r1"
          rows={5}
        />
      </Field>

      <div className="flex justify-end gap-2 pt-4">
        <Button type="button" variant="outline" onClick={onCancel}>
          取消
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? "保存中..." : "更新配额"}
        </Button>
      </div>
    </form>
  );
}
