"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Boxes,
  KeyRound,
  Network,
  Route,
  ShieldCheck,
  ScrollText,
  Activity,
  LayoutDashboard,
  ClipboardList,
} from "lucide-react";
import { cn } from "@/lib/utils";

type NavItem = {
  href: string;
  label: string;
  icon: React.ReactNode;
  disabled?: boolean;
};

const SECTIONS: { heading: string; items: NavItem[] }[] = [
  {
    heading: "概览",
    items: [
      { href: "/dashboard", label: "仪表盘", icon: <LayoutDashboard className="size-4" /> },
    ],
  },
  {
    heading: "配置",
    items: [
      { href: "/providers", label: "供应商", icon: <Network className="size-4" /> },
      { href: "/models", label: "模型", icon: <Boxes className="size-4" /> },
      { href: "/profiles", label: "客户端伪装", icon: <ShieldCheck className="size-4" /> },
      { href: "/router", label: "路由策略", icon: <Route className="size-4" /> },
    ],
  },
  {
    heading: "访问",
    items: [
      { href: "/users", label: "用户与密钥", icon: <KeyRound className="size-4" /> },
    ],
  },
  {
    heading: "可观测",
    items: [
      { href: "/logs", label: "请求日志", icon: <ScrollText className="size-4" /> },
      { href: "/health", label: "健康状态", icon: <Activity className="size-4" /> },
      { href: "/audit", label: "审计日志", icon: <ClipboardList className="size-4" /> },
    ],
  },
];

export function AppSidebar() {
  const pathname = usePathname();

  return (
    <aside className="hidden w-60 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground md:flex">
      <div className="flex h-14 items-center gap-2 border-b px-4">
        <div className="flex size-6 items-center justify-center rounded-md bg-primary text-primary-foreground text-xs font-bold">
          A
        </div>
        <span className="text-sm font-semibold tracking-tight">AI Gateway</span>
      </div>

      <nav className="flex-1 space-y-6 overflow-y-auto p-3">
        {SECTIONS.map((section) => (
          <div key={section.heading} className="space-y-1">
            <p className="px-2 pb-1 text-xs font-medium tracking-wide text-muted-foreground uppercase">
              {section.heading}
            </p>
            {section.items.map((item) => {
              const active =
                pathname === item.href || pathname.startsWith(`${item.href}/`);
              const content = (
                <span className="flex items-center gap-2.5">
                  {item.icon}
                  {item.label}
                </span>
              );
              if (item.disabled) {
                return (
                  <span
                    key={item.href}
                    title="后续阶段开放"
                    className="flex cursor-not-allowed items-center justify-between rounded-md px-2 py-1.5 text-sm text-muted-foreground/50"
                  >
                    {content}
                    <span className="text-[10px] uppercase">稍后</span>
                  </span>
                );
              }
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  className={cn(
                    "flex rounded-md px-2 py-1.5 text-sm transition-colors",
                    active
                      ? "bg-sidebar-accent font-medium text-sidebar-accent-foreground"
                      : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground",
                  )}
                >
                  {content}
                </Link>
              );
            })}
          </div>
        ))}
      </nav>
    </aside>
  );
}
