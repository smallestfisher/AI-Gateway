"use client";

import { ColumnDef } from "@tanstack/react-table";
import { User } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { MoreHorizontal, Key, CreditCard, Trash2 } from "lucide-react";

interface ColumnsProps {
  onEdit: (user: User) => void;
  onManageKeys: (user: User) => void;
  onManageQuota: (user: User) => void;
  onToggleStatus: (user: User) => void;
  onDelete: (user: User) => void;
}

export function createColumns({
  onEdit,
  onManageKeys,
  onManageQuota,
  onToggleStatus,
  onDelete,
}: ColumnsProps): ColumnDef<User>[] {
  return [
    {
      accessorKey: "name",
      header: "名称",
    },
    {
      accessorKey: "email",
      header: "邮箱",
      cell: ({ row }) => row.original.email || "—",
    },
    {
      accessorKey: "status",
      header: "状态",
      cell: ({ row }) => (
        <Badge variant={row.original.status === "active" ? "default" : "secondary"}>
          {row.original.status === "active" ? "启用" : "停用"}
        </Badge>
      ),
    },
    {
      accessorKey: "balance",
      header: "余额",
      cell: ({ row }) => (
        <span className="tabular-nums">
          {row.original.balance.toLocaleString()} token
        </span>
      ),
    },
    {
      accessorKey: "rpm",
      header: "RPM",
      cell: ({ row }) => (
        <span className="tabular-nums">
          {row.original.rpm || "∞"}
        </span>
      ),
    },
    {
      accessorKey: "tpm",
      header: "TPM",
      cell: ({ row }) => (
        <span className="tabular-nums">
          {row.original.tpm || "∞"}
        </span>
      ),
    },
    {
      accessorKey: "whitelist",
      header: "白名单",
      cell: ({ row }) => {
        const count = row.original.whitelist?.length || 0;
        return count > 0 ? `${count} 个模型` : "全部";
      },
    },
    {
      id: "actions",
      cell: ({ row }) => {
        const user = row.original;
        return (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" className="h-8 w-8 p-0">
                <MoreHorizontal className="h-4 w-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => onEdit(user)}>
                编辑用户
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onManageKeys(user)}>
                <Key className="mr-2 h-4 w-4" />
                管理 API 密钥
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onManageQuota(user)}>
                <CreditCard className="mr-2 h-4 w-4" />
                管理配额
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onToggleStatus(user)}>
                {user.status === "active" ? "停用" : "启用"}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onClick={() => onDelete(user)}
              >
                <Trash2 className="mr-2 h-4 w-4" />
                删除用户
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        );
      },
    },
  ];
}
