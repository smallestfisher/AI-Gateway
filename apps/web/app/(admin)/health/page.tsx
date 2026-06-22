"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, AlertTriangle, RefreshCw, ShieldCheck } from "lucide-react";
import { api } from "@/lib/api";
import { qk } from "@/lib/query-keys";
import type { HealthStatus } from "@/lib/types";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

export default function HealthPage() {
  const { data, isFetching, refetch } = useQuery({
    queryKey: qk.health,
    queryFn: () => api.get<HealthStatus>("/health"),
    refetchInterval: 20000,
  });

  const rows = useMemo(() => data?.data ?? [], [data?.data]);
  const summary = useMemo(() => {
    const open = rows.filter((row) => row.open).length;
    const samples = rows.reduce((sum, row) => sum + row.total, 0);
    const failures = rows.reduce((sum, row) => sum + row.failures, 0);
    return { open, samples, failures };
  }, [rows]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="健康状态"
        description="Provider × 上游模型的熔断窗口统计，每 20 秒自动刷新。"
        actions={
          <Button
            type="button"
            variant="outline"
            onClick={() => refetch()}
            disabled={isFetching}
          >
            <RefreshCw
              className={isFetching ? "size-4 animate-spin" : "size-4"}
            />
            刷新
          </Button>
        }
      />

      {data?.warning && (
        <div className="flex items-center gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm text-amber-700 dark:text-amber-300">
          <AlertTriangle className="size-4" />
          {data.warning}
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-3">
        <MetricCard
          title="窗口样本"
          value={summary.samples.toLocaleString()}
          icon={<Activity className="size-4 text-muted-foreground" />}
        />
        <MetricCard
          title="失败样本"
          value={summary.failures.toLocaleString()}
          icon={<AlertTriangle className="size-4 text-muted-foreground" />}
        />
        <MetricCard
          title="熔断中"
          value={summary.open.toLocaleString()}
          icon={<ShieldCheck className="size-4 text-muted-foreground" />}
        />
      </div>

      <div className="overflow-hidden rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow className="bg-muted/40 hover:bg-muted/40">
              <TableHead>供应商</TableHead>
              <TableHead>上游模型</TableHead>
              <TableHead>状态</TableHead>
              <TableHead className="text-right">样本</TableHead>
              <TableHead className="text-right">错误率</TableHead>
              <TableHead className="text-right">慢请求率</TableHead>
              <TableHead className="text-right">阈值</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="h-32 text-center text-sm text-muted-foreground"
                >
                  暂无启用通道或健康样本。
                </TableCell>
              </TableRow>
            ) : (
              rows.map((row) => (
                <TableRow key={`${row.provider_id}:${row.upstream_model}`}>
                  <TableCell className="font-medium">
                    {row.provider_name}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {row.upstream_model}
                  </TableCell>
                  <TableCell>
                    <Badge variant={row.open ? "destructive" : "default"}>
                      {row.open ? "熔断" : "正常"}
                    </Badge>
                    {row.open && (
                      <div className="mt-1 text-xs text-muted-foreground">
                        {Math.round(row.opened_ago_s)}s ago
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {row.total}
                    <div className="text-xs text-muted-foreground">
                      fail {row.failures} / slow {row.slow}
                    </div>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {formatRate(row.error_rate)}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {formatRate(row.slow_rate)}
                  </TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground">
                    <div>err {formatRate(row.thresholds.error_rate)}</div>
                    <div>ttft {row.thresholds.p95_ttft_ms}ms</div>
                    <div>
                      {row.thresholds.window_sec}s /{" "}
                      {row.thresholds.cooldown_sec}s
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

function MetricCard({
  title,
  value,
  icon,
}: {
  title: string;
  value: string;
  icon: React.ReactNode;
}) {
  return (
    <Card className="rounded-lg">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        {icon}
      </CardHeader>
      <CardContent>
        <div className="text-2xl font-bold tabular-nums">{value}</div>
      </CardContent>
    </Card>
  );
}

function formatRate(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}
