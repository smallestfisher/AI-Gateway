"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { Pencil, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { Provider } from "@/lib/types";

export function providerColumns({
  onEdit,
  onDelete,
}: {
  onEdit: (p: Provider) => void;
  onDelete: (p: Provider) => void;
}): ColumnDef<Provider>[] {
  return [
    {
      accessorKey: "name",
      header: "Name",
      cell: ({ row }) => (
        <span className="font-medium">{row.original.name}</span>
      ),
    },
    {
      accessorKey: "base_url",
      header: "Base URL",
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.original.base_url}
        </span>
      ),
    },
    {
      accessorKey: "protocol",
      header: "Protocol",
      cell: ({ row }) => (
        <Badge variant="outline" className="font-mono text-[11px]">
          {row.original.protocol}
        </Badge>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: ({ row }) => (
        <Badge variant={row.original.enabled ? "default" : "secondary"}>
          <span
            className={`mr-1 size-1.5 rounded-full ${
              row.original.enabled ? "bg-emerald-500" : "bg-muted-foreground"
            }`}
          />
          {row.original.enabled ? "enabled" : "disabled"}
        </Badge>
      ),
    },
    {
      accessorKey: "weight",
      header: "Weight",
      cell: ({ row }) => (
        <span className="tabular-nums text-muted-foreground">
          {row.original.weight}
        </span>
      ),
    },
    {
      accessorKey: "priority",
      header: "Priority",
      cell: ({ row }) => (
        <span className="tabular-nums text-muted-foreground">
          {row.original.priority}
        </span>
      ),
    },
    {
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Edit"
            onClick={() => onEdit(row.original)}
          >
            <Pencil className="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Delete"
            onClick={() => onDelete(row.original)}
          >
            <Trash2 className="size-3.5 text-destructive" />
          </Button>
        </div>
      ),
    },
  ];
}
