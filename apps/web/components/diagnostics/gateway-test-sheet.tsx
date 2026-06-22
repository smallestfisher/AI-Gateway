"use client";

import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2, Send } from "lucide-react";
import { toast } from "sonner";
import {
  clearDiagnosticHistory,
  DiagnosticHistoryList,
  readDiagnosticHistory,
  storeDiagnosticHistory,
} from "@/components/diagnostics/diagnostic-history";
import { DiagnosticResultView } from "@/components/diagnostics/diagnostic-result";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { api } from "@/lib/api";
import type {
  DiagnosticResult,
  GatewayTestInput,
  ModelChannel,
} from "@/lib/types";

const PROTOCOLS = ["openai_chat", "anthropic_messages", "openai_responses"];

export function GatewayTestSheet({
  open,
  onOpenChange,
  channel,
  alias,
  providerName,
  providerProtocol,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channel: ModelChannel | null;
  alias: string | null;
  providerName: string | null;
  providerProtocol?: string | null;
}) {
  const [clientProtocolOverride, setClientProtocolOverride] = useState<string | null>(null);
  const clientProtocol = clientProtocolOverride ?? providerProtocol ?? "openai_chat";
  const [message, setMessage] = useState("ping");
  const [result, setResult] = useState<DiagnosticResult | null>(null);
  const historyKey =
    alias && channel
      ? `diagnostic-history:channel:${alias}:${channel.provider_id}:${channel.upstream_model}`
      : "";
  const [history, setHistory] = useState<DiagnosticResult[]>(() =>
    readDiagnosticHistory(historyKey),
  );

  function remember(next: DiagnosticResult) {
    setResult(next);
    setHistory(storeDiagnosticHistory(historyKey, next));
  }

  const test = useMutation({
    mutationFn: (body: GatewayTestInput) =>
      api.post<DiagnosticResult>("/test-gateway", body),
    onSuccess: remember,
    onError: (e) => toast.error(e instanceof Error ? e.message : "测试失败"),
  });

  function run() {
    if (!channel || !alias) {
      toast.error("缺少模型通道");
      return;
    }
    test.mutate({
      client_protocol: clientProtocol,
      alias,
      provider_id: channel.provider_id,
      upstream_model: channel.upstream_model,
      message,
      timeout_ms: 30000,
    });
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>测试模型通道</SheetTitle>
          <SheetDescription>
            {alias && channel
              ? `${alias} -> ${providerName ?? channel.provider_id} / ${
                  channel.upstream_model
                }`
              : "选择通道后可测试。"}
          </SheetDescription>
        </SheetHeader>

        <div className="mt-5 space-y-4">
          <div className="space-y-2">
            <Label>客户端协议</Label>
            <Select value={clientProtocol} onValueChange={setClientProtocolOverride}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PROTOCOLS.map((protocol) => (
                  <SelectItem key={protocol} value={protocol}>
                    {protocol}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label>测试消息</Label>
            <Input
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              placeholder="ping"
            />
          </div>

          <Button
            type="button"
            size="sm"
            onClick={run}
            disabled={test.isPending || !channel || !alias}
          >
            {test.isPending ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <Send className="size-4" />
            )}
            经网关测试
          </Button>

          <DiagnosticHistoryList
            history={history}
            onSelect={setResult}
            onClear={() => setHistory(clearDiagnosticHistory(historyKey))}
          />

          <DiagnosticResultView result={result} />
        </div>
      </SheetContent>
    </Sheet>
  );
}
