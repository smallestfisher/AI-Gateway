"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ClipboardList, RefreshCw, Search } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api } from "@/lib/api";
import { qk } from "@/lib/query-keys";
import type { AuditFilter, AuditLog, AuditLogList } from "@/lib/types";

const PAGE_SIZE = 25;

type FilterState = {
  q: string;
  action: string;
  target_type: string;
  target_id: string;
  from: string;
  to: string;
};

const initialFilters: FilterState = {
  q: "",
  action: "",
  target_type: "",
  target_id: "",
  from: "",
  to: "",
};

export default function AuditPage() {
  const [filters, setFilters] = useState<FilterState>(initialFilters);
  const [page, setPage] = useState(0);
  const [selectedLog, setSelectedLog] = useState<AuditLog | null>(null);

  const queryFilter = useMemo<AuditFilter>(
    () => ({
      q: clean(filters.q),
      action: clean(filters.action),
      target_type: clean(filters.target_type),
      target_id: clean(filters.target_id),
      from: toRFC3339(filters.from),
      to: toRFC3339(filters.to),
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    }),
    [filters, page],
  );
  const params = useMemo(() => buildParams(queryFilter), [queryFilter]);

  const { data, isFetching, refetch } = useQuery({
    queryKey: qk.auditLogs(queryFilter),
    queryFn: () => api.get<AuditLogList>(`/audit-logs?${params}`),
    refetchInterval: 30000,
  });

  const logs = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const updateFilter = <K extends keyof FilterState>(
    key: K,
    value: FilterState[K],
  ) => {
    setPage(0);
    setFilters((prev) => ({ ...prev, [key]: value }));
  };

  const resetFilters = () => {
    setPage(0);
    setFilters(initialFilters);
  };

  return (
    <>
      <PageHeader
        title="审计日志"
        description="Admin 写操作和配置 reload 的审计记录"
        actions={
          <Button variant="outline" onClick={() => refetch()} disabled={isFetching}>
            <RefreshCw className={isFetching ? "size-4 animate-spin" : "size-4"} />
            刷新
          </Button>
        }
      />

      <div className="space-y-4">
        <div className="grid gap-3 rounded-lg border bg-card p-3 md:grid-cols-2 xl:grid-cols-6">
          <div className="relative md:col-span-2">
            <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={filters.q}
              onChange={(e) => updateFilter("q", e.target.value)}
              placeholder="搜索动作、目标、请求 ID 或 diff"
              className="pl-8"
            />
          </div>
          <Input
            value={filters.action}
            onChange={(e) => updateFilter("action", e.target.value)}
            placeholder="动作"
          />
          <Input
            value={filters.target_type}
            onChange={(e) => updateFilter("target_type", e.target.value)}
            placeholder="目标类型"
          />
          <Input
            value={filters.target_id}
            onChange={(e) => updateFilter("target_id", e.target.value)}
            placeholder="目标 ID"
            className="font-mono"
          />
          <Input
            type="datetime-local"
            value={filters.from}
            onChange={(e) => updateFilter("from", e.target.value)}
            aria-label="开始时间"
          />
          <Input
            type="datetime-local"
            value={filters.to}
            onChange={(e) => updateFilter("to", e.target.value)}
            aria-label="结束时间"
          />
          <div className="flex items-center justify-end md:col-span-2 xl:col-span-1">
            <Button variant="ghost" onClick={resetFilters}>
              重置
            </Button>
          </div>
        </div>

        <div className="overflow-hidden rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/40 hover:bg-muted/40">
                <TableHead className="w-[170px]">时间</TableHead>
                <TableHead>动作</TableHead>
                <TableHead>目标</TableHead>
                <TableHead>请求 ID</TableHead>
                <TableHead className="text-right">变更</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isFetching && logs.length === 0 ? (
                Array.from({ length: 6 }).map((_, i) => (
                  <TableRow key={i}>
                    {Array.from({ length: 5 }).map((__, j) => (
                      <TableCell key={j}>
                        <Skeleton className="h-4 w-full max-w-[150px]" />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : logs.length > 0 ? (
                logs.map((log) => (
                  <AuditRow key={log.id} log={log} onSelect={setSelectedLog} />
                ))
              ) : (
                <TableRow>
                  <TableCell colSpan={5} className="p-0">
                    <EmptyState
                      icon={<ClipboardList className="size-5" />}
                      title="暂无审计日志"
                      description="Admin 写操作完成后，匹配条件的记录会显示在这里。"
                      className="rounded-none border-0"
                    />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>

        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            共 {total.toLocaleString()} 条，第 {page + 1} / {pageCount} 页
          </span>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.max(0, p - 1))}
              disabled={page === 0 || isFetching}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
              disabled={page >= pageCount - 1 || isFetching}
            >
              下一页
            </Button>
          </div>
        </div>
      </div>

      <AuditDetailSheet
        log={selectedLog}
        open={selectedLog !== null}
        onOpenChange={(open) => !open && setSelectedLog(null)}
      />
    </>
  );
}

function AuditRow({
  log,
  onSelect,
}: {
  log: AuditLog;
  onSelect: (log: AuditLog) => void;
}) {
  return (
    <TableRow
      className="cursor-pointer"
      onClick={() => onSelect(log)}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") onSelect(log);
      }}
    >
      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
        {formatTime(log.timestamp)}
      </TableCell>
      <TableCell>
        <Badge variant="outline" className="font-mono text-[11px]">
          {log.action}
        </Badge>
      </TableCell>
      <TableCell>
        <div className="space-y-1">
          <div className="font-medium">{log.target_type}</div>
          <div className="max-w-[360px] truncate font-mono text-xs text-muted-foreground">
            {log.target_id || "-"}
          </div>
        </div>
      </TableCell>
      <TableCell>
        <span className="break-all font-mono text-xs">
          {log.request_id || "-"}
        </span>
      </TableCell>
      <TableCell className="text-right text-xs text-muted-foreground">
        {Object.keys(log.diff ?? {}).length.toLocaleString()} 字段
      </TableCell>
    </TableRow>
  );
}

function AuditDetailSheet({
  log,
  open,
  onOpenChange,
}: {
  log: AuditLog | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const diffText = useMemo(() => formatDiff(log?.diff), [log?.diff]);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle>审计详情</SheetTitle>
          <SheetDescription>
            {log ? `${log.action} · ${formatTime(log.timestamp)}` : "选择记录后查看变更摘要。"}
          </SheetDescription>
        </SheetHeader>

        {log && (
          <div className="mt-5 space-y-5">
            <section className="rounded-md border p-3">
              <h3 className="mb-3 text-sm font-medium">基础信息</h3>
              <dl className="grid grid-cols-2 gap-x-3 gap-y-2 text-xs">
                <DetailItem label="时间" value={formatTime(log.timestamp)} />
                <DetailItem label="动作" value={log.action} mono />
                <DetailItem label="目标类型" value={log.target_type} mono />
                <DetailItem label="目标 ID" value={log.target_id || "-"} mono />
                <DetailItem label="请求 ID" value={log.request_id || "-"} mono />
                <DetailItem label="操作者" value={log.actor_id || "静态 Admin Token"} />
              </dl>
            </section>

            <section className="rounded-md border p-3">
              <h3 className="mb-3 text-sm font-medium">Diff</h3>
              <pre className="max-h-[520px] overflow-auto rounded-md bg-muted p-3 font-mono text-xs leading-5">
                {diffText}
              </pre>
            </section>
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

function DetailItem({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={mono ? "break-all font-mono" : "break-words"}>
        {value}
      </dd>
    </>
  );
}

function buildParams(filter: AuditFilter) {
  const params = new URLSearchParams();
  Object.entries(filter).forEach(([key, value]) => {
    if (value === undefined || value === "" || value === null) return;
    params.set(key, String(value));
  });
  return params.toString();
}

function clean(value: string) {
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : undefined;
}

function toRFC3339(value: string) {
  if (!value) return undefined;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return undefined;
  return d.toISOString();
}

function formatTime(value: string) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString();
}

function formatDiff(diff?: Record<string, unknown>) {
  const value = diff && Object.keys(diff).length > 0 ? diff : {};
  return JSON.stringify(value, null, 2);
}
