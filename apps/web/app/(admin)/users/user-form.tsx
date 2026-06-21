"use client";

import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { User } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/field";

const userSchema = z.object({
  name: z.string().min(1, "Name is required"),
  email: z.string().email("Invalid email").or(z.literal("")),
  balance: z.number().int().min(0),
});

type UserFormData = z.infer<typeof userSchema>;

interface UserFormProps {
  user?: User;
  onSubmit: (data: UserFormData) => void;
  onCancel: () => void;
}

export function UserForm({ user, onSubmit, onCancel }: UserFormProps) {
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<UserFormData>({
    resolver: zodResolver(userSchema),
    defaultValues: user
      ? {
          name: user.name,
          email: user.email || "",
          balance: user.balance || 0,
        }
      : {
          name: "",
          email: "",
          balance: 0,
        },
  });

  return (
    <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
      <Field label="Name" error={errors.name?.message} required>
        <Input {...register("name")} placeholder="User name" />
      </Field>

      <Field label="Email" error={errors.email?.message}>
        <Input {...register("email")} type="email" placeholder="user@example.com" />
      </Field>

      {!user && (
        <Field
          label="Initial Balance"
          error={errors.balance?.message}
          hint="Token balance (can be adjusted later)"
        >
          <Input
            {...register("balance", { valueAsNumber: true })}
            type="number"
            placeholder="0"
            min="0"
          />
        </Field>
      )}

      <div className="flex justify-end gap-2 pt-4">
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? "Saving..." : user ? "Update" : "Create"}
        </Button>
      </div>
    </form>
  );
}
