"use client";

import { ColumnDef } from "@tanstack/react-table";
import { User } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { MoreHorizontal, Key, CreditCard } from "lucide-react";

interface ColumnsProps {
  onEdit: (user: User) => void;
  onManageKeys: (user: User) => void;
  onManageQuota: (user: User) => void;
  onToggleStatus: (user: User) => void;
}

export function createColumns({
  onEdit,
  onManageKeys,
  onManageQuota,
  onToggleStatus,
}: ColumnsProps): ColumnDef<User>[] {
  return [
    {
      accessorKey: "name",
      header: "Name",
    },
    {
      accessorKey: "email",
      header: "Email",
      cell: ({ row }) => row.original.email || "—",
    },
    {
      accessorKey: "status",
      header: "Status",
      cell: ({ row }) => (
        <Badge variant={row.original.status === "active" ? "default" : "secondary"}>
          {row.original.status}
        </Badge>
      ),
    },
    {
      accessorKey: "balance",
      header: "Balance",
      cell: ({ row }) => (
        <span className="tabular-nums">
          {row.original.balance.toLocaleString()} tokens
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
      header: "Whitelist",
      cell: ({ row }) => {
        const count = row.original.whitelist?.length || 0;
        return count > 0 ? `${count} models` : "All";
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
                Edit user
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onManageKeys(user)}>
                <Key className="mr-2 h-4 w-4" />
                Manage API keys
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onManageQuota(user)}>
                <CreditCard className="mr-2 h-4 w-4" />
                Manage quota
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onToggleStatus(user)}>
                {user.status === "active" ? "Disable" : "Enable"}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        );
      },
    },
  ];
}
