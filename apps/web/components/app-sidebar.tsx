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
    heading: "Configuration",
    items: [
      { href: "/providers", label: "Providers", icon: <Network className="size-4" /> },
      { href: "/models", label: "Models", icon: <Boxes className="size-4" /> },
      { href: "/profiles", label: "Client Profiles", icon: <ShieldCheck className="size-4" /> },
      { href: "/router", label: "Router", icon: <Route className="size-4" /> },
    ],
  },
  {
    heading: "Access",
    items: [
      { href: "/users", label: "Users & Keys", icon: <KeyRound className="size-4" /> },
    ],
  },
  {
    heading: "Observability",
    items: [
      { href: "/logs", label: "Logs", icon: <ScrollText className="size-4" />, disabled: true },
      { href: "/health", label: "Health", icon: <Activity className="size-4" />, disabled: true },
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
                    title="Coming in a later phase"
                    className="flex cursor-not-allowed items-center justify-between rounded-md px-2 py-1.5 text-sm text-muted-foreground/50"
                  >
                    {content}
                    <span className="text-[10px] uppercase">soon</span>
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
