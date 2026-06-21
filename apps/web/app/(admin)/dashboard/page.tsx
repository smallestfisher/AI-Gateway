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
    queryFn: () => api.get<DashboardStats>("/api/admin/dashboard/stats"),
    refetchInterval: 30000, // Refresh every 30 seconds
  });

  if (isLoading) {
    return (
      <>
        <PageHeader title="Dashboard" description="Gateway metrics and insights" />
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
          {[...Array(4)].map((_, i) => (
            <Card key={i} className="animate-pulse">
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <div className="h-4 w-24 bg-muted rounded" />
              </CardHeader>
              <CardContent>
                <div className="h-8 w-16 bg-muted rounded" />
              </CardContent>
            </Card>
          ))}
        </div>
      </>
    );
  }

  if (!stats) {
    return (
      <>
        <PageHeader title="Dashboard" description="Gateway metrics and insights" />
        <Card>
          <CardContent className="py-12 text-center text-muted-foreground">
            No data available yet. Start sending requests to see metrics.
          </CardContent>
        </Card>
      </>
    );
  }

  return (
    <>
      <PageHeader
        title="Dashboard"
        description={`Last ${stats.period} • Updates every 30s`}
      />

      {/* Top-level metrics */}
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Requests</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {stats.total_requests.toLocaleString()}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Active Users</CardTitle>
            <Users className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stats.active_users}</div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Avg Latency</CardTitle>
            <Clock className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {Math.round(stats.avg_latency_ms)}
              <span className="text-sm font-normal text-muted-foreground ml-1">ms</span>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Success Rate</CardTitle>
            <TrendingUp className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {stats.success_rate.toFixed(1)}%
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Secondary metrics */}
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Providers</CardTitle>
            <Server className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stats.provider_count}</div>
            <p className="text-xs text-muted-foreground">Active providers</p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Models</CardTitle>
            <Boxes className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stats.model_count}</div>
            <p className="text-xs text-muted-foreground">Available models</p>
          </CardContent>
        </Card>
      </div>

      {/* Top models */}
      <Card>
        <CardHeader>
          <CardTitle>Top Models</CardTitle>
        </CardHeader>
        <CardContent>
          {stats.top_models.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-8">
              No model usage yet
            </p>
          ) : (
            <div className="space-y-3">
              {stats.top_models.map((model) => (
                <div key={model.model} className="flex items-center justify-between">
                  <div className="flex-1">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{model.model}</span>
                      <Badge variant="secondary" className="tabular-nums">
                        {model.percentage.toFixed(1)}%
                      </Badge>
                    </div>
                    <div className="mt-1 flex items-center gap-4 text-xs text-muted-foreground">
                      <span>{model.requests.toLocaleString()} requests</span>
                      <span>•</span>
                      <span>{Math.round(model.avg_latency_ms)}ms avg</span>
                      <span>•</span>
                      <span>{model.success_rate.toFixed(1)}% success</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Recent errors */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <AlertCircle className="h-4 w-4" />
            Recent Errors
          </CardTitle>
        </CardHeader>
        <CardContent>
          {stats.recent_errors.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-8">
              No errors in the last {stats.period} 🎉
            </p>
          ) : (
            <div className="space-y-3">
              {stats.recent_errors.map((err, i) => (
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
    </>
  );
}
