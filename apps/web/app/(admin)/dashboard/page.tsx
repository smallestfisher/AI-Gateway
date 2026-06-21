"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  Activity,
  Users,
  Server,
  Boxes,
  TrendingUp,
  Clock,
  AlertCircle
} from "lucide-react";

interface DashboardStats {
  period: string;
  total_requests: number;
  active_users: number;
  provider_count: number;
  model_count: number;
  avg_latency_ms: number;
  success_rate: number;
  top_models: ModelStats[];
  recent_errors: ErrorStat[];
}

interface ModelStats {
  model: string;
  requests: number;
  percentage: number;
  avg_latency_ms: number;
  success_rate: number;
}

interface ErrorStat {
  timestamp: string;
  model: string;
  provider_id?: string;
  error_code: string;
  error_msg: string;
  status: string;
}

export default function DashboardPage() {
  const { data: stats, isLoading } = useQuery({
    queryKey: ["dashboard", "stats"],
    queryFn: () => api.get<DashboardStats>("/dashboard/stats"),
    refetchInterval: 30000,
  });
  const topModels = stats?.top_models ?? [];
  const recentErrors = stats?.recent_errors ?? [];

  if (isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader title="仪表盘" description="网关指标与运行概览" />
        <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-4">
          {[...Array(4)].map((_, i) => (
            <Card key={i} className="min-h-36 animate-pulse rounded-lg">
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <div className="h-4 w-24 rounded bg-muted" />
              </CardHeader>
              <CardContent>
                <div className="h-8 w-16 rounded bg-muted" />
              </CardContent>
            </Card>
          ))}
        </div>
      </div>
    );
  }

  if (!stats) {
    return (
      <div className="space-y-6">
        <PageHeader title="仪表盘" description="网关指标与运行概览" />
        <Card className="rounded-lg">
          <CardContent className="py-12 text-center text-muted-foreground">
            暂无数据。发送请求后这里会显示运行指标。
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="仪表盘"
        description={`最近 ${stats.period}，每 30 秒自动刷新`}
      />

      <div className="space-y-6">
        <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-4">
          <Card className="min-h-36 rounded-lg">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">总请求数</CardTitle>
              <Activity className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              <div className="text-2xl font-bold">
                {stats.total_requests.toLocaleString()}
              </div>
            </CardContent>
          </Card>

        <Card className="min-h-36 rounded-lg">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">活跃用户</CardTitle>
            <Users className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stats.active_users}</div>
          </CardContent>
        </Card>

        <Card className="min-h-36 rounded-lg">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">平均延迟</CardTitle>
            <Clock className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {Math.round(stats.avg_latency_ms)}
              <span className="text-sm font-normal text-muted-foreground ml-1">ms</span>
            </div>
          </CardContent>
        </Card>

        <Card className="min-h-36 rounded-lg">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">成功率</CardTitle>
            <TrendingUp className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {stats.success_rate.toFixed(1)}%
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        <Card className="min-h-32 rounded-lg">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">供应商</CardTitle>
            <Server className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stats.provider_count}</div>
            <p className="text-xs text-muted-foreground">已启用供应商</p>
          </CardContent>
        </Card>

        <Card className="min-h-32 rounded-lg">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">模型</CardTitle>
            <Boxes className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stats.model_count}</div>
            <p className="text-xs text-muted-foreground">可用模型别名</p>
          </CardContent>
        </Card>
      </div>

      <Card className="rounded-lg">
        <CardHeader>
          <CardTitle>热门模型</CardTitle>
        </CardHeader>
        <CardContent>
          {topModels.length === 0 ? (
            <p className="flex min-h-28 items-center justify-center text-center text-sm text-muted-foreground">
              暂无模型调用
            </p>
          ) : (
            <div className="space-y-3">
              {topModels.map((model) => (
                <div key={model.model} className="flex items-center justify-between">
                  <div className="flex-1">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{model.model}</span>
                      <Badge variant="secondary" className="tabular-nums">
                        {model.percentage.toFixed(1)}%
                      </Badge>
                    </div>
                    <div className="mt-1 flex items-center gap-4 text-xs text-muted-foreground">
                      <span>{model.requests.toLocaleString()} 次请求</span>
                      <span>•</span>
                      <span>平均 {Math.round(model.avg_latency_ms)}ms</span>
                      <span>•</span>
                      <span>成功率 {model.success_rate.toFixed(1)}%</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Card className="rounded-lg">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <AlertCircle className="h-4 w-4" />
            最近错误
          </CardTitle>
        </CardHeader>
        <CardContent>
          {recentErrors.length === 0 ? (
            <p className="flex min-h-28 items-center justify-center text-center text-sm text-muted-foreground">
              最近 {stats.period} 没有错误
            </p>
          ) : (
            <div className="space-y-3">
              {recentErrors.map((err, i) => (
                <div key={i} className="border-l-2 border-destructive pl-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex-1 space-y-1">
                      <div className="flex items-center gap-2">
                        <Badge variant="destructive">{err.error_code}</Badge>
                        <span className="text-sm font-medium">{err.model}</span>
                      </div>
                      <p className="text-xs text-muted-foreground">
                        {err.error_msg}
                      </p>
                    </div>
                    <span className="text-xs text-muted-foreground whitespace-nowrap">
                      {new Date(err.timestamp).toLocaleTimeString()}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
      </div>
    </div>
  );
}
