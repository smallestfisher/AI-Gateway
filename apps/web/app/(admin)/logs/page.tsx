"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RefreshCw, Search, ScrollText } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { api } from "@/lib/api";
import { qk } from "@/lib/query-keys";
import type { LogFilter, LogList, RequestLog } from "@/lib/types";

const PAGE_SIZE = 25;

type FilterState = {
  q: string;
  model: string;
  provider_id: string;
  protocol: string;
  status: string;
  stream: "all" | "true" | "false";
  from: string;
  to: string;
};

const initialFilters: FilterState = {
  q: "",
  model: "",
  provider_id: "",
  protocol: "all",
  status: "all",
  stream: "all",
  from: "",
  to: "",
};

export default function LogsPage() {
  const [filters, setFilters] = useState<FilterState>(initialFilters);
  const [page, setPage] = useState(0);
  const [selectedLog, setSelectedLog] = useState<RequestLog | null>(null);

  const queryFilter = useMemo<LogFilter>(
    () => ({
      q: clean(filters.q),
      model: clean(filters.model),
      provider_id: clean(filters.provider_id),
      protocol: filters.protocol === "all" ? undefined : filters.protocol,
      status: filters.status === "all" ? undefined : filters.status,
      stream: filters.stream === "all" ? undefined : filters.stream === "true",
      from: toRFC3339(filters.from),
      to: toRFC3339(filters.to),
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    }),
    [filters, page],
  );

  const params = useMemo(() => buildParams(queryFilter), [queryFilter]);

  const { data, isFetching, refetch } = useQuery({
    queryKey: qk.logs(queryFilter),
    queryFn: () => api.get<LogList>(`/logs?${params}`),
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
        title="请求日志"
        description="网关 request_logs 表中的请求历史"
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
              placeholder="搜索请求 ID、模型或错误"
              className="pl-8"
            />
          </div>
          <Input
            value={filters.model}
            onChange={(e) => updateFilter("model", e.target.value)}
            placeholder="模型"
          />
          <Input
            value={filters.provider_id}
            onChange={(e) => updateFilter("provider_id", e.target.value)}
            placeholder="供应商 ID"
          />
          <Select
            value={filters.protocol}
            onValueChange={(value) => updateFilter("protocol", value)}
          >
            <SelectTrigger className="w-full">
              <SelectValue placeholder="协议" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部协议</SelectItem>
              <SelectItem value="openai_chat">OpenAI Chat</SelectItem>
              <SelectItem value="openai_responses">OpenAI Responses</SelectItem>
              <SelectItem value="anthropic_messages">Anthropic Messages</SelectItem>
            </SelectContent>
          </Select>
          <Select
            value={filters.status}
            onValueChange={(value) => updateFilter("status", value)}
          >
            <SelectTrigger className="w-full">
              <SelectValue placeholder="状态" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部状态</SelectItem>
              <SelectItem value="success">成功</SelectItem>
              <SelectItem value="error">错误</SelectItem>
              <SelectItem value="no_channel">无通道</SelectItem>
              <SelectItem value="no_available_channel">无可用通道</SelectItem>
              <SelectItem value="client_disconnect">客户端断开</SelectItem>
            </SelectContent>
          </Select>
          <Select
            value={filters.stream}
            onValueChange={(value) =>
              updateFilter("stream", value as FilterState["stream"])
            }
          >
            <SelectTrigger className="w-full">
              <SelectValue placeholder="流式" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部模式</SelectItem>
              <SelectItem value="true">流式</SelectItem>
              <SelectItem value="false">非流式</SelectItem>
            </SelectContent>
          </Select>
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
          <div className="flex items-center justify-end md:col-span-2 xl:col-span-2">
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
                <TableHead>请求</TableHead>
                <TableHead>模型</TableHead>
                <TableHead>协议</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">延迟</TableHead>
                <TableHead className="text-right">Token</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isFetching && logs.length === 0 ? (
                Array.from({ length: 6 }).map((_, i) => (
                  <TableRow key={i}>
                    {Array.from({ length: 7 }).map((__, j) => (
                      <TableCell key={j}>
                        <Skeleton className="h-4 w-full max-w-[150px]" />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : logs.length > 0 ? (
                logs.map((log) => (
                  <LogRow
                    key={log.id}
                    log={log}
                    onSelect={setSelectedLog}
                  />
                ))
              ) : (
                <TableRow>
                  <TableCell colSpan={7} className="p-0">
                    <EmptyState
                      icon={<ScrollText className="size-5" />}
                      title="暂无请求日志"
                      description="网关处理请求后，匹配条件的记录会显示在这里。"
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

      <LogDetailSheet
        log={selectedLog}
        open={selectedLog !== null}
        onOpenChange={(open) => !open && setSelectedLog(null)}
      />
    </>
  );
}

function LogRow({
  log,
  onSelect,
}: {
  log: RequestLog;
  onSelect: (log: RequestLog) => void;
}) {
  const tokens =
    (log.input_tokens ?? 0) +
    (log.output_tokens ?? 0) +
    (log.reasoning_tokens ?? 0);

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
        <div className="space-y-1">
          <div className="font-mono text-xs">{log.request_id}</div>
          {(log.provider_id || log.upstream_model || log.error_msg) && (
            <div className="max-w-[360px] truncate text-xs text-muted-foreground">
              {log.error_msg || log.upstream_model || log.provider_id}
            </div>
          )}
        </div>
      </TableCell>
      <TableCell>
        <span className="font-medium">{log.model}</span>
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline" className="font-mono text-[11px]">
            {log.protocol}
          </Badge>
          {log.stream && <Badge variant="secondary">流式</Badge>}
        </div>
      </TableCell>
      <TableCell>
        <StatusBadge status={log.status} />
      </TableCell>
      <TableCell className="text-right tabular-nums">
        {formatMs(log.latency_ms)}
        {log.ttft_ms ? (
          <div className="text-xs text-muted-foreground">
            {log.ttft_ms}ms TTFT
          </div>
        ) : null}
      </TableCell>
      <TableCell className="text-right tabular-nums">
        {tokens > 0 ? tokens.toLocaleString() : "-"}
      </TableCell>
    </TableRow>
  );
}

function LogDetailSheet({
  log,
  open,
  onOpenChange,
}: {
  log: RequestLog | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>请求详情</SheetTitle>
          <SheetDescription>
            {log?.request_id ?? "选择日志后查看完整字段。"}
          </SheetDescription>
        </SheetHeader>

        {log && (
          <div className="mt-5 space-y-5">
            <DetailSection title="请求">
              <DetailItem label="请求 ID" value={log.request_id} mono />
              <DetailItem label="时间" value={formatTime(log.timestamp)} />
              <DetailItem label="用户 ID" value={log.user_id || "-"} mono />
              <DetailItem label="API Key ID" value={log.api_key_id || "-"} mono />
              <DetailItem label="协议" value={log.protocol} mono />
              <DetailItem label="模型" value={log.model} mono />
              <DetailItem label="模式" value={log.stream ? "流式" : "非流式"} />
            </DetailSection>

            <DetailSection title="路由">
              <DetailItem label="供应商" value={log.provider_id || "-"} mono />
              <DetailItem
                label="上游模型"
                value={log.upstream_model || "-"}
                mono
              />
              <DetailItem label="状态" value={log.status} mono />
              <DetailItem
                label="HTTP"
                value={log.http_status ? String(log.http_status) : "-"}
              />
              <DetailItem label="停止原因" value={log.stop_reason || "-"} mono />
            </DetailSection>

            <DetailSection title="耗时">
              <DetailItem label="总延迟" value={formatMs(log.latency_ms)} />
              <DetailItem label="TTFT" value={formatMs(log.ttft_ms)} />
            </DetailSection>

            <DetailSection title="Token">
              <DetailItem
                label="输入"
                value={String(log.input_tokens ?? 0)}
              />
              <DetailItem
                label="输出"
                value={String(log.output_tokens ?? 0)}
              />
              <DetailItem
                label="Cache Read"
                value={String(log.cache_read_tokens ?? 0)}
              />
              <DetailItem
                label="Cache Create"
                value={String(log.cache_creation_tokens ?? 0)}
              />
              <DetailItem
                label="Reasoning"
                value={String(log.reasoning_tokens ?? 0)}
              />
            </DetailSection>

            {(log.error_code || log.error_msg) && (
              <DetailSection title="错误">
                <DetailItem label="错误码" value={log.error_code || "-"} mono />
                <DetailItem label="错误信息" value={log.error_msg || "-"} />
              </DetailSection>
            )}
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

function DetailSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-md border p-3">
      <h3 className="mb-3 text-sm font-medium">{title}</h3>
      <dl className="grid grid-cols-2 gap-x-3 gap-y-2 text-xs">{children}</dl>
    </section>
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

function StatusBadge({ status }: { status: string }) {
  if (status === "success") {
    return <Badge>成功</Badge>;
  }
  if (status === "client_disconnect") {
    return <Badge variant="secondary">客户端断开</Badge>;
  }
  return <Badge variant="destructive">{status.replaceAll("_", " ")}</Badge>;
}

function buildParams(filter: LogFilter) {
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

function formatMs(value?: number) {
  if (!value) return "-";
  return `${value}ms`;
}
