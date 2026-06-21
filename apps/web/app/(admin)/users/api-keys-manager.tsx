"use client";

import { useState } from "react";
import { APIKey, User } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Field } from "@/components/field";
import { Trash2, Copy, Check } from "lucide-react";
import { toast } from "sonner";

interface APIKeysManagerProps {
  user: User;
  keys: APIKey[];
  onIssue: (name: string) => Promise<{ key: string }>;
  onRevoke: (keyId: string) => Promise<void>;
  onClose: () => void;
}

export function APIKeysManager({
  user,
  keys,
  onIssue,
  onRevoke,
  onClose,
}: APIKeysManagerProps) {
  const [isIssuing, setIsIssuing] = useState(false);
  const [newKeyName, setNewKeyName] = useState("");
  const [newlyIssuedKey, setNewlyIssuedKey] = useState<string | null>(null);
  const [copiedKey, setCopiedKey] = useState(false);
  const [copiedExistingKey, setCopiedExistingKey] = useState<string | null>(
    null
  );

  const handleIssue = async () => {
    if (!newKeyName.trim()) {
      toast.error("请输入密钥名称");
      return;
    }
    setIsIssuing(true);
    try {
      const result = await onIssue(newKeyName);
      setNewlyIssuedKey(result.key);
      setNewKeyName("");
      toast.success("API 密钥已发放");
    } catch {
      toast.error("发放 API 密钥失败");
    } finally {
      setIsIssuing(false);
    }
  };

  const handleCopyIssuedKey = async () => {
    if (!newlyIssuedKey) return;
    try {
      await navigator.clipboard.writeText(newlyIssuedKey);
      setCopiedKey(true);
      toast.success("完整 API 密钥已复制");
      setTimeout(() => setCopiedKey(false), 2000);
    } catch {
      toast.error("复制失败，请手动复制");
    }
  };

  const handleCopyExistingKey = async (key: APIKey) => {
    if (!key.key) {
      toast.error("旧密钥无法恢复完整值，请重新发放");
      return;
    }
    try {
      await navigator.clipboard.writeText(key.key);
      setCopiedExistingKey(key.id ?? key.key_prefix);
      toast.success("完整 API 密钥已复制");
      setTimeout(() => setCopiedExistingKey(null), 2000);
    } catch {
      toast.error("复制失败，请手动复制");
    }
  };

  const handleRevoke = async (keyId: string) => {
    if (!confirm("确定要撤销这个 API 密钥吗？")) return;
    try {
      await onRevoke(keyId);
      toast.success("API 密钥已撤销");
    } catch {
      toast.error("撤销 API 密钥失败");
    }
  };

  return (
    <div className="min-w-0 space-y-4 px-4 pb-4">
      <div className="min-w-0">
        <h3 className="mb-1 break-words text-lg font-medium">
          {user.name} 的 API 密钥
        </h3>
        <p className="text-sm text-muted-foreground">
          管理该用户的 API 密钥
        </p>
      </div>

      {newlyIssuedKey && (
        <Card className="border-terracotta bg-terracotta/5">
          <CardHeader>
            <CardTitle className="text-base">新 API 密钥已发放</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm text-muted-foreground">
              请立即复制此密钥，关闭后将不再显示。
            </p>
            <div className="flex min-w-0 gap-2">
              <Input
                value={newlyIssuedKey}
                readOnly
                className="min-w-0 font-mono text-sm"
              />
              <Button
                variant="outline"
                size="icon"
                onClick={handleCopyIssuedKey}
                className="shrink-0"
                title="复制完整密钥"
                aria-label="复制完整密钥"
              >
                {copiedKey ? (
                  <Check className="h-4 w-4" />
                ) : (
                  <Copy className="h-4 w-4" />
                )}
              </Button>
            </div>
            <Button
              variant="outline"
              onClick={() => setNewlyIssuedKey(null)}
              className="w-full"
            >
              完成
            </Button>
          </CardContent>
        </Card>
      )}

      <div className="space-y-3">
        <h4 className="text-sm font-medium">发放新密钥</h4>
        <div className="flex min-w-0 flex-col gap-2 sm:flex-row">
          <Field label="" className="mb-0 min-w-0 flex-1">
            <Input
              placeholder="密钥名称（例如 Production）"
              value={newKeyName}
              onChange={(e) => setNewKeyName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleIssue()}
            />
          </Field>
          <Button
            onClick={handleIssue}
            disabled={isIssuing}
            className="w-full sm:w-auto"
          >
            {isIssuing ? "发放中..." : "发放密钥"}
          </Button>
        </div>
      </div>

      <div className="space-y-3">
        <h4 className="text-sm font-medium">现有密钥（{keys.length}）</h4>
        {keys.length === 0 ? (
          <p className="text-sm text-muted-foreground py-8 text-center">
            暂无 API 密钥
          </p>
        ) : (
          <div className="space-y-2">
            {keys.map((key) => (
              <Card key={key.id}>
                <CardContent className="flex min-w-0 flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0 space-y-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="min-w-0 break-words font-medium">
                        {key.name}
                      </span>
                      <Badge
                        variant={
                          key.status === "active" ? "default" : "secondary"
                        }
                      >
                        {key.status}
                      </Badge>
                    </div>
                    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
                      <span className="min-w-0 break-all font-mono">
                        {key.key || `${key.key_prefix}...`}
                      </span>
                      {!key.key && (
                        <span className="text-xs text-destructive">
                          完整密钥不可恢复
                        </span>
                      )}
                      <span>•</span>
                      <span>
                        创建于{" "}
                        {new Date(key.created_at!).toLocaleDateString()}
                      </span>
                      {key.last_used_at && (
                        <>
                          <span>•</span>
                          <span>
                            最近使用{" "}
                            {new Date(key.last_used_at).toLocaleDateString()}
                          </span>
                        </>
                      )}
                    </div>
                  </div>
                  <div className="flex shrink-0 justify-end gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => handleCopyExistingKey(key)}
                      title={key.key ? "复制完整密钥" : "完整密钥不可恢复"}
                      aria-label={key.key ? "复制完整密钥" : "完整密钥不可恢复"}
                      disabled={!key.key}
                    >
                      {copiedExistingKey === (key.id ?? key.key_prefix) ? (
                        <Check className="h-4 w-4" />
                      ) : (
                        <Copy className="h-4 w-4" />
                      )}
                    </Button>
                    {key.status === "active" && (
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => handleRevoke(key.id!)}
                        title="撤销密钥"
                        aria-label="撤销密钥"
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    )}
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </div>

      <div className="flex justify-end pt-4">
        <Button variant="outline" onClick={onClose}>
          关闭
        </Button>
      </div>
    </div>
  );
}
