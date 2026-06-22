"use client";

import { CheckCircle2, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import type { DiagnosticResult } from "@/lib/types";

const HISTORY_LIMIT = 20;

export function readDiagnosticHistory(key: string): DiagnosticResult[] {
  if (!key || typeof window === "undefined") return [];
  try {
    const value = window.localStorage.getItem(key);
    const parsed = value ? JSON.parse(value) : [];
    return Array.isArray(parsed) ? parsed.slice(0, HISTORY_LIMIT) : [];
  } catch {
    return [];
  }
}

export function storeDiagnosticHistory(
  key: string,
  result: DiagnosticResult,
) {
  if (!key || typeof window === "undefined") return [];
  const current = readDiagnosticHistory(key);
  const next = [
    result,
    ...current.filter((item) => item.request_id !== result.request_id),
  ].slice(0, HISTORY_LIMIT);
  window.localStorage.setItem(key, JSON.stringify(next));
  return next;
}

export function clearDiagnosticHistory(key: string) {
  if (key && typeof window !== "undefined") {
    window.localStorage.removeItem(key);
  }
  return [];
}

export function DiagnosticHistoryList({
  history,
  onSelect,
  onClear,
}: {
  history: DiagnosticResult[];
  onSelect: (result: DiagnosticResult) => void;
  onClear: () => void;
}) {
  if (history.length === 0) return null;

  return (
    <section className="space-y-2 rounded-md border p-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium">最近测试</h3>
        <Button type="button" variant="ghost" size="sm" onClick={onClear}>
          清空
        </Button>
      </div>
      <div className="space-y-1">
        {history.map((item, index) => (
          <button
            key={item.request_id ?? `${item.mode}-${index}`}
            type="button"
            onClick={() => onSelect(item)}
            className="flex w-full min-w-0 items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted"
          >
            <span className="flex min-w-0 items-center gap-2">
              {item.ok ? (
                <CheckCircle2 className="size-3.5 shrink-0 text-emerald-600" />
              ) : (
                <XCircle className="size-3.5 shrink-0 text-destructive" />
              )}
              <span className="min-w-0 truncate font-mono">
                {item.request_id || item.upstream_model || item.alias || item.mode}
              </span>
            </span>
            <span className="flex shrink-0 items-center gap-2">
              <Badge variant={item.ok ? "default" : "destructive"}>
                {item.ok ? "成功" : "失败"}
              </Badge>
              <span className="text-muted-foreground tabular-nums">
                {item.latency_ms}ms
              </span>
            </span>
          </button>
        ))}
      </div>
    </section>
  );
}
