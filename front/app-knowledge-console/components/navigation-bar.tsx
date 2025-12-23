"use client";

import * as NavigationMenu from "@radix-ui/react-navigation-menu";
import Link from "next/link";
import { usePathname } from "next/navigation";

const links = [
  { href: "/", label: "Загрузка документа" },
  { href: "/documents", label: "Просмотр документов" },
];

const getLinkClasses = (isActive: boolean) =>
  [
    "inline-flex items-center gap-2 rounded-full px-4 py-2 text-sm font-medium transition-colors",
    "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-500",
    isActive
      ? "bg-indigo-600 text-white shadow-md shadow-indigo-500/20"
      : "text-slate-200 hover:bg-slate-800 hover:text-white",
  ].join(" ");

export default function NavigationBar() {
  const pathname = usePathname();

  const isActive = (href: string) => {
    if (href === "/") {
      return pathname === "/";
    }
    return pathname?.startsWith(href);
  };

  return (
    <NavigationMenu.Root className="fixed inset-x-0 top-0 z-50 border-b border-slate-800 bg-slate-950/80 backdrop-blur">
      <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
        <span className="text-sm font-semibold tracking-tight text-slate-100">
          Knowledge Console
        </span>
        <NavigationMenu.List className="flex items-center gap-2">
          {links.map((link) => {
            const active = isActive(link.href);
            return (
              <NavigationMenu.Item key={link.href}>
                <NavigationMenu.Link asChild>
                  <Link
                    href={link.href}
                    aria-current={active ? "page" : undefined}
                    className={getLinkClasses(active)}
                  >
                    {link.label}
                  </Link>
                </NavigationMenu.Link>
              </NavigationMenu.Item>
            );
          })}
        </NavigationMenu.List>
      </div>
    </NavigationMenu.Root>
  );
}
