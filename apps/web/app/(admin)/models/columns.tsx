"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { Model, ModelChannel } from "@/lib/types";

export function modelColumns({
  onDelete,
  channelsByModel,
}: {
  onDelete: (m: Model) => void;
  channelsByModel: Map<string, ModelChannel[]>;
}): ColumnDef<Model>[] {
  return [
    {
      accessorKey: "alias",
      header: "Alias",
      cell: ({ row }) => (
        <span className="font-mono text-sm font-medium">{row.original.alias}</span>
      ),
    },
    {
      accessorKey: "display_name",
      header: "Display name",
      cell: ({ row }) => (
        <span className="text-muted-foreground">{row.original.display_name}</span>
      ),
    },
    {
      id: "channels",
      header: "Channels",
      cell: ({ row }) => {
        const n = channelsByModel.get(row.original.id ?? "")?.length ?? 0;
        return (
          <Badge variant={n > 0 ? "default" : "secondary"} className="tabular-nums">
            {n} {n === 1 ? "channel" : "channels"}
          </Badge>
        );
      },
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
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Delete model"
            onClick={() => onDelete(row.original)}
          >
            <Trash2 className="size-3.5 text-destructive" />
          </Button>
        </div>
      ),
    },
  ];
}
