"use client";

import { useState } from "react";
import { CheckCircle2, Copy, XCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { DiagnosticResult } from "@/lib/types";

export function DiagnosticResultView({
  result,
}: {
  result: DiagnosticResult | null;
}) {
  const [copied, setCopied] = useState(false);

  if (!result) return null;

  async function copyRequestID() {
    if (!result?.request_id) return;
    await navigator.clipboard.writeText(result.request_id);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className="rounded-md border bg-background p-3 text-sm">
      <div className="mb-3 flex items-center justify-between gap-2">
        <Badge variant={result.ok ? "default" : "destructive"} className="gap-1">
          {result.ok ? (
            <CheckCircle2 className="size-3.5" />
          ) : (
            <XCircle className="size-3.5" />
          )}
          {result.ok ? "成功" : "失败"}
        </Badge>
        <span className="text-xs text-muted-foreground tabular-nums">
          {result.latency_ms}ms
          {result.http_status ? ` · HTTP ${result.http_status}` : ""}
        </span>
      </div>

      <dl className="grid grid-cols-2 gap-x-3 gap-y-2 text-xs">
        <dt className="text-muted-foreground">模式</dt>
        <dd className="font-mono">{result.mode}</dd>

        {result.request_id && (
          <>
            <dt className="text-muted-foreground">请求 ID</dt>
            <dd className="flex min-w-0 items-center gap-1">
              <span className="min-w-0 break-all font-mono">
                {result.request_id}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                onClick={copyRequestID}
                aria-label="复制请求 ID"
              >
                <Copy className={copied ? "size-3 text-emerald-600" : "size-3"} />
              </Button>
            </dd>
          </>
        )}

        <dt className="text-muted-foreground">供应商</dt>
        <dd>{result.provider_name || result.provider_id || "-"}</dd>

        <dt className="text-muted-foreground">上游模型</dt>
        <dd className="break-all font-mono">{result.upstream_model || "-"}</dd>

        {result.client_protocol && (
          <>
            <dt className="text-muted-foreground">客户端协议</dt>
            <dd className="font-mono">{result.client_protocol}</dd>
          </>
        )}

        {result.stop_reason && (
          <>
            <dt className="text-muted-foreground">停止原因</dt>
            <dd className="font-mono">{result.stop_reason}</dd>
          </>
        )}

        {result.usage && (
          <>
            <dt className="text-muted-foreground">Token</dt>
            <dd className="tabular-nums">
              in {result.usage.input_tokens} / out {result.usage.output_tokens}
            </dd>
          </>
        )}
      </dl>

      {result.response_preview && (
        <pre className="mt-3 max-h-32 overflow-auto rounded-md bg-muted p-2 text-xs whitespace-pre-wrap">
          {result.response_preview}
        </pre>
      )}

      {result.error && (
        <div className="mt-3 rounded-md border border-destructive/30 bg-destructive/5 p-2 text-xs text-destructive">
          <div className="font-mono">{result.error.code}</div>
          <div className="mt-1">{result.error.message}</div>
          {result.error.body_preview && (
            <pre className="mt-2 whitespace-pre-wrap">
              {result.error.body_preview}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}
