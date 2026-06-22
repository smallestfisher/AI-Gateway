"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Loader2, RefreshCw, Send } from "lucide-react";
import { toast } from "sonner";
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
import { qk } from "@/lib/query-keys";
import type {
  DiagnosticResult,
  GatewayTestInput,
  Model,
  ModelChannel,
  Provider,
  UpstreamModel,
  UpstreamTestInput,
} from "@/lib/types";

const PROTOCOLS = ["openai_chat", "anthropic_messages", "openai_responses"];

export function ProviderDiagnosticsSheet({
  provider,
  open,
  onOpenChange,
  models,
  channels,
}: {
  provider: Provider | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  models: Model[];
  channels: ModelChannel[];
}) {
  const providerId = provider?.id ?? "";
  const [upstreamModel, setUpstreamModel] = useState("");
  const [message, setMessage] = useState("ping");
  const [clientProtocol, setClientProtocol] = useState("openai_chat");
  const [channelKey, setChannelKey] = useState("");
  const [result, setResult] = useState<DiagnosticResult | null>(null);

  const upstreamModels = useQuery({
    queryKey: qk.upstreamModels(providerId),
    queryFn: () =>
      api.list<UpstreamModel>(`/providers/${providerId}/upstream-models`),
    enabled: open && Boolean(providerId),
    retry: false,
  });

  const modelAlias = useMemo(() => {
    const map = new Map<string, string>();
    for (const model of models) {
      if (model.id) map.set(model.id, model.alias);
    }
    return map;
  }, [models]);

  const providerChannels = useMemo(
    () =>
      channels.filter(
        (channel) => channel.provider_id === providerId && channel.enabled && channel.id,
      ),
    [channels, providerId],
  );

  const direct = useMutation({
    mutationFn: (body: UpstreamTestInput) =>
      api.post<DiagnosticResult>(`/providers/${providerId}/test-upstream`, body),
    onSuccess: setResult,
    onError: (e) => toast.error(e instanceof Error ? e.message : "测试失败"),
  });

  const gateway = useMutation({
    mutationFn: (body: GatewayTestInput) =>
      api.post<DiagnosticResult>("/test-gateway", body),
    onSuccess: setResult,
    onError: (e) => toast.error(e instanceof Error ? e.message : "测试失败"),
  });

  const selectedChannel = providerChannels.find(
    (channel) => channel.id === channelKey,
  );

  function runDirect() {
    if (!upstreamModel.trim()) {
      toast.error("请输入上游模型");
      return;
    }
    direct.mutate({
      upstream_model: upstreamModel.trim(),
      message,
      timeout_ms: 30000,
    });
  }

  function runGateway() {
    if (!selectedChannel) {
      toast.error("请选择已绑定通道");
      return;
    }
    const alias = modelAlias.get(selectedChannel.model_id);
    if (!alias) {
      toast.error("找不到模型别名");
      return;
    }
    gateway.mutate({
      client_protocol: clientProtocol,
      alias,
      provider_id: selectedChannel.provider_id,
      upstream_model: selectedChannel.upstream_model,
      message,
      timeout_ms: 30000,
    });
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>诊断供应商</SheetTitle>
          <SheetDescription>
            {provider?.name ?? "选择供应商后可测试上游与网关链路。"}
          </SheetDescription>
        </SheetHeader>

        <div className="mt-5 space-y-5">
          <section className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>支持的上游模型</Label>
              <Button
                type="button"
                variant="outline"
                size="icon-sm"
                onClick={() => upstreamModels.refetch()}
                disabled={!providerId || upstreamModels.isFetching}
                aria-label="刷新上游模型"
              >
                <RefreshCw
                  className={
                    upstreamModels.isFetching
                      ? "size-3.5 animate-spin"
                      : "size-3.5"
                  }
                />
              </Button>
            </div>
            <Select
              value={upstreamModel}
              onValueChange={setUpstreamModel}
              disabled={(upstreamModels.data ?? []).length === 0}
            >
              <SelectTrigger>
                <SelectValue
                  placeholder={
                    upstreamModels.isLoading
                      ? "正在拉取模型..."
                      : upstreamModels.isError
                        ? "拉取失败，可手动输入"
                        : "选择上游模型"
                  }
                />
              </SelectTrigger>
              <SelectContent>
                {(upstreamModels.data ?? []).map((model) => (
                  <SelectItem key={model.id} value={model.id}>
                    <span className="font-mono">{model.id}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input
              value={upstreamModel}
              onChange={(e) => setUpstreamModel(e.target.value)}
              placeholder="手动输入上游模型"
            />
          </section>

          <section className="space-y-2">
            <Label>测试消息</Label>
            <Input
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              placeholder="ping"
            />
          </section>

          <section className="space-y-3 rounded-md border p-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h3 className="text-sm font-medium">直连上游测试</h3>
                <p className="text-xs text-muted-foreground">
                  验证 base_url、API key、代理和上游模型。
                </p>
              </div>
              <Button
                type="button"
                size="sm"
                onClick={runDirect}
                disabled={direct.isPending || !providerId}
              >
                {direct.isPending ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <Send className="size-4" />
                )}
                测试
              </Button>
            </div>
          </section>

          <section className="space-y-3 rounded-md border p-3">
            <div className="grid gap-3">
              <div className="space-y-2">
                <Label>已绑定通道</Label>
                <Select value={channelKey} onValueChange={setChannelKey}>
                  <SelectTrigger>
                    <SelectValue placeholder="选择模型通道" />
                  </SelectTrigger>
                  <SelectContent>
                    {providerChannels.map((channel) => (
                      <SelectItem key={channel.id} value={channel.id ?? ""}>
                        {(modelAlias.get(channel.model_id) ?? channel.model_id) +
                          " -> " +
                          channel.upstream_model}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label>客户端协议</Label>
                <Select
                  value={clientProtocol}
                  onValueChange={setClientProtocol}
                >
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
            </div>
            <Button
              type="button"
              size="sm"
              onClick={runGateway}
              disabled={gateway.isPending || !providerId}
            >
              {gateway.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Send className="size-4" />
              )}
              经网关测试
            </Button>
          </section>

          <DiagnosticResultView result={result} />
        </div>
      </SheetContent>
    </Sheet>
  );
}
