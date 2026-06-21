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

  const handleIssue = async () => {
    if (!newKeyName.trim()) {
      toast.error("Key name is required");
      return;
    }
    setIsIssuing(true);
    try {
      const result = await onIssue(newKeyName);
      setNewlyIssuedKey(result.key);
      setNewKeyName("");
      toast.success("API key issued successfully");
    } catch (error) {
      toast.error("Failed to issue API key");
    } finally {
      setIsIssuing(false);
    }
  };

  const handleCopy = async () => {
    if (newlyIssuedKey) {
      await navigator.clipboard.writeText(newlyIssuedKey);
      setCopiedKey(true);
      toast.success("Copied to clipboard");
      setTimeout(() => setCopiedKey(false), 2000);
    }
  };

  const handleRevoke = async (keyId: string) => {
    if (!confirm("Are you sure you want to revoke this API key?")) return;
    try {
      await onRevoke(keyId);
      toast.success("API key revoked");
    } catch (error) {
      toast.error("Failed to revoke API key");
    }
  };

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-lg font-medium mb-1">
          API Keys for {user.name}
        </h3>
        <p className="text-sm text-muted-foreground">
          Manage API keys for this user
        </p>
      </div>

      {newlyIssuedKey && (
        <Card className="border-terracotta bg-terracotta/5">
          <CardHeader>
            <CardTitle className="text-base">New API Key Issued</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Copy this key now — it won't be shown again.
            </p>
            <div className="flex gap-2">
              <Input
                value={newlyIssuedKey}
                readOnly
                className="font-mono text-sm"
              />
              <Button
                variant="outline"
                size="icon"
                onClick={handleCopy}
                className="shrink-0"
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
              Done
            </Button>
          </CardContent>
        </Card>
      )}

      <div className="space-y-3">
        <h4 className="text-sm font-medium">Issue New Key</h4>
        <div className="flex gap-2">
          <Field label="" className="flex-1 mb-0">
            <Input
              placeholder="Key name (e.g., Production)"
              value={newKeyName}
              onChange={(e) => setNewKeyName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleIssue()}
            />
          </Field>
          <Button onClick={handleIssue} disabled={isIssuing}>
            {isIssuing ? "Issuing..." : "Issue Key"}
          </Button>
        </div>
      </div>

      <div className="space-y-3">
        <h4 className="text-sm font-medium">Existing Keys ({keys.length})</h4>
        {keys.length === 0 ? (
          <p className="text-sm text-muted-foreground py-8 text-center">
            No API keys yet
          </p>
        ) : (
          <div className="space-y-2">
            {keys.map((key) => (
              <Card key={key.id}>
                <CardContent className="flex items-center justify-between p-4">
                  <div className="space-y-1">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{key.name}</span>
                      <Badge
                        variant={
                          key.status === "active" ? "default" : "secondary"
                        }
                      >
                        {key.status}
                      </Badge>
                    </div>
                    <div className="flex items-center gap-3 text-sm text-muted-foreground">
                      <span className="font-mono">{key.key_prefix}...</span>
                      <span>•</span>
                      <span>
                        Created{" "}
                        {new Date(key.created_at!).toLocaleDateString()}
                      </span>
                      {key.last_used_at && (
                        <>
                          <span>•</span>
                          <span>
                            Last used{" "}
                            {new Date(key.last_used_at).toLocaleDateString()}
                          </span>
                        </>
                      )}
                    </div>
                  </div>
                  {key.status === "active" && (
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => handleRevoke(key.id!)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  )}
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </div>

      <div className="flex justify-end pt-4">
        <Button variant="outline" onClick={onClose}>
          Close
        </Button>
      </div>
    </div>
  );
}
