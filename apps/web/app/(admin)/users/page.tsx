"use client";

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { User, APIKey } from "@/lib/types";
import { api } from "@/lib/api";
import { DataTable } from "@/components/data-table";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { createColumns } from "./columns";
import { UserForm } from "./user-form";
import { QuotaForm } from "./quota-form";
import { APIKeysManager } from "./api-keys-manager";
import { toast } from "sonner";

type DialogState =
  | { type: "none" }
  | { type: "create" }
  | { type: "edit"; user: User }
  | { type: "quota"; user: User }
  | { type: "keys"; user: User };

export default function UsersPage() {
  const [dialog, setDialog] = useState<DialogState>({ type: "none" });
  const queryClient = useQueryClient();

  const { data: users = [], isLoading } = useQuery({
    queryKey: ["users"],
    queryFn: () => api.get<User[]>("/api/admin/users"),
  });

  const { data: apiKeys = [] } = useQuery({
    queryKey: ["apiKeys", dialog.type === "keys" ? dialog.user.id : null],
    queryFn: () =>
      dialog.type === "keys"
        ? api.get<APIKey[]>(`/api/admin/users/${dialog.user.id}/api-keys`)
        : Promise.resolve([]),
    enabled: dialog.type === "keys",
  });

  const createMutation = useMutation({
    mutationFn: (data: { name: string; email: string; balance: number }) =>
      api.post("/api/admin/users", data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      setDialog({ type: "none" });
      toast.success("User created");
    },
    onError: () => toast.error("Failed to create user"),
  });

  const updateMutation = useMutation({
    mutationFn: ({
      id,
      data,
    }: {
      id: string;
      data: { name: string; email: string; status: string };
    }) => api.put(`/api/admin/users/${id}`, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      setDialog({ type: "none" });
      toast.success("User updated");
    },
    onError: () => toast.error("Failed to update user"),
  });

  const quotaMutation = useMutation({
    mutationFn: ({
      id,
      data,
    }: {
      id: string;
      data: { balance: number; rpm: number; tpm: number; whitelist: string[] };
    }) => api.put(`/api/admin/users/${id}/quota`, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      setDialog({ type: "none" });
      toast.success("Quota updated");
    },
    onError: () => toast.error("Failed to update quota"),
  });

  const issueKeyMutation = useMutation({
    mutationFn: ({ userId, name }: { userId: string; name: string }) =>
      api.post<{ id: string; key: string }>(
        `/api/admin/users/${userId}/api-keys`,
        { name }
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["apiKeys", dialog.type === "keys" ? dialog.user.id : null],
      });
    },
  });

  const revokeKeyMutation = useMutation({
    mutationFn: ({ userId, keyId }: { userId: string; keyId: string }) =>
      api.delete(`/api/admin/users/${userId}/api-keys/${keyId}`),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["apiKeys", dialog.type === "keys" ? dialog.user.id : null],
      });
    },
  });

  const handleToggleStatus = (user: User) => {
    const newStatus = user.status === "active" ? "disabled" : "active";
    updateMutation.mutate({
      id: user.id!,
      data: { name: user.name, email: user.email || "", status: newStatus },
    });
  };

  const columns = createColumns({
    onEdit: (user) => setDialog({ type: "edit", user }),
    onManageKeys: (user) => setDialog({ type: "keys", user }),
    onManageQuota: (user) => setDialog({ type: "quota", user }),
    onToggleStatus: handleToggleStatus,
  });

  return (
    <>
      <PageHeader
        title="Users & API Keys"
        description="Manage users and their API keys"
        actions={
          <Button onClick={() => setDialog({ type: "create" })}>
            Create User
          </Button>
        }
      />

      <DataTable columns={columns} data={users} loading={isLoading} />

      <Sheet
        open={dialog.type === "create"}
        onOpenChange={(open) => !open && setDialog({ type: "none" })}
      >
        <SheetContent>
          <SheetHeader>
            <SheetTitle>Create User</SheetTitle>
          </SheetHeader>
          <div className="mt-6">
            <UserForm
              onSubmit={(data) => createMutation.mutate(data)}
              onCancel={() => setDialog({ type: "none" })}
            />
          </div>
        </SheetContent>
      </Sheet>

      <Sheet
        open={dialog.type === "edit"}
        onOpenChange={(open) => !open && setDialog({ type: "none" })}
      >
        <SheetContent>
          <SheetHeader>
            <SheetTitle>Edit User</SheetTitle>
          </SheetHeader>
          <div className="mt-6">
            {dialog.type === "edit" && (
              <UserForm
                user={dialog.user}
                onSubmit={(data) =>
                  updateMutation.mutate({
                    id: dialog.user.id!,
                    data: { ...data, status: dialog.user.status },
                  })
                }
                onCancel={() => setDialog({ type: "none" })}
              />
            )}
          </div>
        </SheetContent>
      </Sheet>

      <Sheet
        open={dialog.type === "quota"}
        onOpenChange={(open) => !open && setDialog({ type: "none" })}
      >
        <SheetContent>
          <SheetHeader>
            <SheetTitle>Manage Quota</SheetTitle>
          </SheetHeader>
          <div className="mt-6">
            {dialog.type === "quota" && (
              <QuotaForm
                user={dialog.user}
                onSubmit={(data) =>
                  quotaMutation.mutate({ id: dialog.user.id!, data })
                }
                onCancel={() => setDialog({ type: "none" })}
              />
            )}
          </div>
        </SheetContent>
      </Sheet>

      <Sheet
        open={dialog.type === "keys"}
        onOpenChange={(open) => !open && setDialog({ type: "none" })}
      >
        <SheetContent className="sm:max-w-xl overflow-y-auto">
          <SheetHeader>
            <SheetTitle>API Keys</SheetTitle>
          </SheetHeader>
          <div className="mt-6">
            {dialog.type === "keys" && (
              <APIKeysManager
                user={dialog.user}
                keys={apiKeys}
                onIssue={(name) =>
                  issueKeyMutation.mutateAsync({
                    userId: dialog.user.id!,
                    name,
                  })
                }
                onRevoke={(keyId) =>
                  revokeKeyMutation.mutateAsync({
                    userId: dialog.user.id!,
                    keyId,
                  })
                }
                onClose={() => setDialog({ type: "none" })}
              />
            )}
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}
