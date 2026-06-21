"use client";

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { User, APIKey } from "@/lib/types";
import { api } from "@/lib/api";
import { DataTable } from "@/components/data-table";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { ConfirmDialog } from "@/components/confirm-dialog";
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
  | { type: "keys"; user: User }
  | { type: "delete"; user: User };

export default function UsersPage() {
  const [dialog, setDialog] = useState<DialogState>({ type: "none" });
  const queryClient = useQueryClient();
  const sheetClassName =
    "data-[side=right]:w-full overflow-y-auto sm:data-[side=right]:max-w-xl";

  const { data: users = [], isLoading } = useQuery({
    queryKey: ["users"],
    queryFn: () => api.list<User>("/users"),
  });

  const { data: apiKeys = [] } = useQuery({
    queryKey: ["apiKeys", dialog.type === "keys" ? dialog.user.id : null],
    queryFn: () =>
      dialog.type === "keys"
        ? api.list<APIKey>(`/users/${dialog.user.id}/api-keys`)
        : Promise.resolve([]),
    enabled: dialog.type === "keys",
  });

  const createMutation = useMutation({
    mutationFn: (data: { name: string; email: string; balance: number }) =>
      api.post("/users", data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      setDialog({ type: "none" });
      toast.success("用户已创建");
    },
    onError: () => toast.error("创建用户失败"),
  });

  const updateMutation = useMutation({
    mutationFn: ({
      id,
      data,
    }: {
      id: string;
      data: { name: string; email: string; status: string };
    }) => api.put(`/users/${id}`, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      setDialog({ type: "none" });
      toast.success("用户已更新");
    },
    onError: () => toast.error("更新用户失败"),
  });

  const quotaMutation = useMutation({
    mutationFn: ({
      id,
      data,
    }: {
      id: string;
      data: { balance: number; rpm: number; tpm: number; whitelist: string[] };
    }) => api.put(`/users/${id}/quota`, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      setDialog({ type: "none" });
      toast.success("配额已更新");
    },
    onError: () => toast.error("更新配额失败"),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/users/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      setDialog({ type: "none" });
      toast.success("用户已删除");
    },
    onError: () => toast.error("删除用户失败"),
  });

  const issueKeyMutation = useMutation({
    mutationFn: ({ userId, name }: { userId: string; name: string }) =>
      api.post<{ id: string; key: string }>(
        `/users/${userId}/api-keys`,
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
      api.delete(`/users/${userId}/api-keys/${keyId}`),
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
    onDelete: (user) => setDialog({ type: "delete", user }),
  });

  return (
    <>
      <PageHeader
        title="用户与 API 密钥"
        description="管理用户、API 密钥和调用配额"
        actions={
          <Button onClick={() => setDialog({ type: "create" })}>
            新建用户
          </Button>
        }
      />

      <DataTable columns={columns} data={users} loading={isLoading} />

      <Sheet
        open={dialog.type === "create"}
        onOpenChange={(open) => !open && setDialog({ type: "none" })}
      >
        <SheetContent className={sheetClassName}>
          <SheetHeader>
            <SheetTitle>新建用户</SheetTitle>
          </SheetHeader>
          <div className="mt-2 px-4 pb-4">
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
        <SheetContent className={sheetClassName}>
          <SheetHeader>
            <SheetTitle>编辑用户</SheetTitle>
          </SheetHeader>
          <div className="mt-2 px-4 pb-4">
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
        <SheetContent className={sheetClassName}>
          <SheetHeader>
            <SheetTitle>管理配额</SheetTitle>
          </SheetHeader>
          <div className="mt-2 px-4 pb-4">
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
        <SheetContent className={sheetClassName}>
          <SheetHeader>
            <SheetTitle>API 密钥</SheetTitle>
          </SheetHeader>
          <div className="mt-2">
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

      <ConfirmDialog
        open={dialog.type === "delete"}
        onOpenChange={(open) => !open && setDialog({ type: "none" })}
        title={
          dialog.type === "delete" ? `删除“${dialog.user.name}”？` : "删除用户？"
        }
        description="该用户的 API 密钥和配额会一并删除，历史请求日志会保留。"
        confirmLabel="删除用户"
        loading={deleteMutation.isPending}
        onConfirm={() => {
          if (dialog.type === "delete") {
            deleteMutation.mutate(dialog.user.id!);
          }
        }}
      />
    </>
  );
}
