"use client";

import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { LogOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ThemeToggle } from "@/components/theme-toggle";
import { api } from "@/lib/api";
import { qk } from "@/lib/query-keys";

export function Topbar() {
  const router = useRouter();
  const { data } = useQuery({
    queryKey: qk.configVersion,
    queryFn: () => api.get<{ version: number }>("/config/version"),
    refetchInterval: 20_000,
  });

  async function logout() {
    await fetch("/api/auth/logout", { method: "POST" });
    router.replace("/login");
    router.refresh();
  }

  return (
    <header className="flex h-14 shrink-0 items-center justify-between gap-3 border-b bg-background/80 px-4 backdrop-blur">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span className="relative flex size-2">
          <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-500/60" />
          <span className="relative inline-flex size-2 rounded-full bg-emerald-500" />
        </span>
        网关在线
      </div>

      <div className="flex items-center gap-1.5">
        {typeof data?.version === "number" && (
          <span className="rounded-md border bg-muted px-2 py-1 font-mono text-xs text-muted-foreground">
            v{data.version}
          </span>
        )}
        <ThemeToggle />
        <Button
          variant="ghost"
          size="icon"
          aria-label="退出登录"
          onClick={logout}
        >
          <LogOut className="size-4" />
        </Button>
      </div>
    </header>
  );
}
