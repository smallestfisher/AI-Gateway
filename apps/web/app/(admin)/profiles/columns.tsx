"use client";

import type { ColumnDef } from "@tanstack/react-table";
import { Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { ClientProfile } from "@/lib/types";

export function profileColumns({
  onDelete,
  targetLabel,
}: {
  onDelete: (p: ClientProfile) => void;
  // Resolves a (scope, target_id) to a human label, e.g. the provider/model name.
  targetLabel: (scope: string, targetId: string) => string;
}): ColumnDef<ClientProfile>[] {
  return [
    {
      accessorKey: "name",
      header: "名称",
      cell: ({ row }) => (
        <span className="font-medium">{row.original.name}</span>
      ),
    },
    {
      accessorKey: "scope",
      header: "作用域",
      cell: ({ row }) => (
        <Badge variant="outline" className="font-mono text-[11px]">
          {row.original.scope}
        </Badge>
      ),
    },
    {
      id: "target",
      header: "目标",
      cell: ({ row }) => {
        const p = row.original;
        if (p.scope === "default" || !p.target_id) {
          return <span className="text-xs text-muted-foreground">—</span>;
        }
        return (
          <span className="font-mono text-xs text-muted-foreground">
            {targetLabel(p.scope, p.target_id)}
          </span>
        );
      },
    },
    {
      id: "impersonation",
      header: "伪装内容",
      cell: ({ row }) => {
        const p = row.original;
        const bits: string[] = [];
        if (p.user_agent) bits.push("UA");
        if (p.origin) bits.push("Origin");
        if (p.referer) bits.push("Referer");
        if (p.headers && Object.keys(p.headers).length) bits.push("Headers");
        if (bits.length === 0)
          return (
            <span className="text-xs text-muted-foreground">无</span>
          );
        return (
          <div className="flex flex-wrap gap-1">
            {bits.map((b) => (
              <Badge key={b} variant="secondary" className="text-[10px]">
                {b}
              </Badge>
            ))}
          </div>
        );
      },
    },
    {
      id: "strip",
      header: "剥离客户端头",
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.strip_client_headers ? "是" : "否"}
        </span>
      ),
    },
    {
      id: "status",
      header: "状态",
      cell: ({ row }) => (
        <Badge variant={row.original.enabled ? "default" : "secondary"}>
          <span
            className={`mr-1 size-1.5 rounded-full ${
              row.original.enabled ? "bg-emerald-500" : "bg-muted-foreground"
            }`}
          />
          {row.original.enabled ? "已启用" : "已停用"}
        </Badge>
      ),
    },
    {
      id: "actions",
      header: () => <span className="sr-only">操作</span>,
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="删除"
            onClick={() => onDelete(row.original)}
          >
            <Trash2 className="size-3.5 text-destructive" />
          </Button>
        </div>
      ),
    },
  ];
}
