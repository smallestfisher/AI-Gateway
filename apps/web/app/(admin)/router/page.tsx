"use client";

import { useMemo, useState } from "react";
import { Route, Plus, Pencil } from "lucide-react";
import { toast } from "sonner";
import type { Model, RouterPolicy } from "@/lib/types";
import { qk } from "@/lib/query-keys";
import { useResource } from "@/hooks/use-resource";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { PolicyForm } from "./policy-form";

function modeBadge(mode: string) {
  return (
    <Badge variant="outline" className="font-mono text-[11px]">
      {mode}
    </Badge>
  );
}

function statusBadge(enabled: boolean) {
  return (
    <Badge variant={enabled ? "default" : "secondary"}>
      <span
        className={`mr-1 size-1.5 rounded-full ${
          enabled ? "bg-emerald-500" : "bg-muted-foreground"
        }`}
      />
      {enabled ? "enabled" : "disabled"}
    </Badge>
  );
}

function paramCount(p: RouterPolicy | undefined | null) {
  if (!p?.params) return 0;
  return Object.keys(p.params).length;
}

export default function RouterPage() {
  const policies = useResource<RouterPolicy>({
    key: qk.policies,
    path: "/router-policies",
  });
  const models = useResource<Model>({ key: qk.models, path: "/models" });

  // POST /router-policies is an upsert: global replaces the single global row;
  // a model override replaces the row for that model_id. No delete/put, so the
  // `create` mutation is the only write, and "edit" = upsert again.
  const [formKey, setFormKey] = useState(0);
  const [formOpen, setFormOpen] = useState(false);
  const [formScope, setFormScope] = useState<"global" | "model">("global");
  const [formEditing, setFormEditing] = useState<RouterPolicy | null>(null);

  const all = policies.list.data ?? [];
  const globalPolicy = all.find((p) => p.scope === "global") ?? null;
  const modelOverrides = all.filter((p) => p.scope === "model");

  const modelLabel = useMemo(() => {
    const m = new Map<string, string>();
    for (const mo of models.list.data ?? [])
      if (mo.id) m.set(mo.id, mo.display_name || mo.alias);
    return m;
  }, [models.list.data]);

  // Models selectable as an override target: those without an existing override,
  // plus (when editing) the one currently being edited.
  const overrideModelIds = useMemo(
    () =>
      new Set(
        modelOverrides.map((o) => o.model_id).filter(Boolean) as string[],
      ),
    [modelOverrides],
  );
  const modelOptions = useMemo(
    () =>
      (models.list.data ?? [])
        .filter(
          (m) =>
            m.id && (!overrideModelIds.has(m.id) || m.id === formEditing?.model_id),
        )
        .map((m) => ({ id: m.id as string, label: m.display_name || m.alias })),
    [models.list.data, overrideModelIds, formEditing],
  );

  function openGlobal(editing: RouterPolicy | null) {
    setFormScope("global");
    setFormEditing(editing);
    setFormKey((k) => k + 1);
    setFormOpen(true);
  }
  function openModel(editing: RouterPolicy | null) {
    setFormScope("model");
    setFormEditing(editing);
    setFormKey((k) => k + 1);
    setFormOpen(true);
  }

  async function upsert(body: Partial<RouterPolicy>) {
    try {
      await policies.create.mutateAsync(body);
      toast.success("Policy saved");
      setFormOpen(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Save failed");
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Router"
        description="How candidate channels are ordered for each model: a global policy, optionally overridden per model."
      />

      {/* Global policy */}
      <Card>
        <CardHeader className="border-b">
          <CardTitle>Global policy</CardTitle>
          <CardDescription>
            Applied to every model without a per-model override.
          </CardDescription>
          <CardAction>
            {globalPolicy ? (
              <Button variant="outline" size="sm" onClick={() => openGlobal(globalPolicy)}>
                <Pencil className="size-3.5" />
                Edit
              </Button>
            ) : (
              <Button size="sm" onClick={() => openGlobal(null)}>
                Set global policy
              </Button>
            )}
          </CardAction>
        </CardHeader>
        <CardContent className="pt-4">
          {policies.list.isLoading ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : globalPolicy ? (
            <div className="flex flex-wrap items-center gap-3 text-sm">
              {modeBadge(globalPolicy.mode)}
              {statusBadge(globalPolicy.enabled)}
              {paramCount(globalPolicy) > 0 && (
                <span className="text-xs text-muted-foreground">
                  params: {paramCount(globalPolicy)} key
                  {paramCount(globalPolicy) === 1 ? "" : "s"}
                </span>
              )}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">
              Not set — models use the built-in default (failover) behavior.
            </p>
          )}
        </CardContent>
      </Card>

      {/* Per-model overrides */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold">Per-model overrides</h2>
            <p className="text-xs text-muted-foreground">
              Take precedence over the global policy for a single model.
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => openModel(null)}
            disabled={modelOptions.length === 0}
            title={
              modelOptions.length === 0
                ? "Every model already has an override."
                : undefined
            }
          >
            <Plus className="size-3.5" />
            Add override
          </Button>
        </div>

        {modelOverrides.length === 0 ? (
          <EmptyState
            icon={<Route className="size-5" />}
            title="No model overrides"
            description="Models without an override fall back to the global policy."
          />
        ) : (
          <div className="space-y-2">
            {modelOverrides.map((o) => (
              <Card key={o.id} size="sm">
                <CardContent className="flex items-center justify-between py-3">
                  <div className="flex flex-wrap items-center gap-3 text-sm">
                    <span className="font-medium">
                      {o.model_id ? (modelLabel.get(o.model_id) ?? o.model_id) : "—"}
                    </span>
                    {modeBadge(o.mode)}
                    {statusBadge(o.enabled)}
                    {paramCount(o) > 0 && (
                      <span className="text-xs text-muted-foreground">
                        params: {paramCount(o)}
                      </span>
                    )}
                  </div>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Edit override"
                    onClick={() => openModel(o)}
                  >
                    <Pencil className="size-3.5" />
                  </Button>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </div>

      <PolicyForm
        key={formKey}
        open={formOpen}
        onOpenChange={setFormOpen}
        onSubmit={upsert}
        submitting={policies.create.isPending}
        scope={formScope}
        editing={formEditing}
        modelOptions={modelOptions}
      />
    </div>
  );
}
