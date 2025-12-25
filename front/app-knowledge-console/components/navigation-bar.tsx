// front/app-knowledge-console/components/navigation-bar.tsx
"use client";

import * as NavigationMenu from "@radix-ui/react-navigation-menu";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { signIn, signOut, useSession } from "next-auth/react";
import { useCallback, useMemo } from "react";

const links = [
  { href: "/", label: "Загрузка документа" },
  { href: "/documents", label: "Просмотр документов" },
];

const getLinkClasses = (isActive: boolean) =>
  [
    "inline-flex items-center gap-2 rounded-full px-4 py-2 text-sm font-medium transition-colors",
    "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-500",
    isActive
      ? "bg-indigo-100 text-indigo-700"
      : "text-slate-700 hover:bg-slate-100",
  ].join(" ");

export default function NavigationBar() {
  const pathname = usePathname();
  const { data: session, status } = useSession();

  const isActive = (href: string) => {
    if (href === "/") {
      return pathname === "/";
    }
    return pathname?.startsWith(href);
  };

  const postLogoutRedirect =
    process.env.NEXT_PUBLIC_AUTHENTIK_POST_LOGOUT_REDIRECT ??
    (typeof window !== "undefined" ? window.location.origin : undefined);

  const logoutUrl = useMemo(() => {
    if (!process.env.NEXT_PUBLIC_AUTHENTIK_LOGOUT_URL) return null;
    const redirectUrl = postLogoutRedirect ?? "/";
    const separator = process.env.NEXT_PUBLIC_AUTHENTIK_LOGOUT_URL.includes("?") ? "&" : "?";
    const redirectParam = `post_logout_redirect_uri=${encodeURIComponent(redirectUrl)}`;
    return `${process.env.NEXT_PUBLIC_AUTHENTIK_LOGOUT_URL}${separator}${redirectParam}`;
  }, [postLogoutRedirect]);

  const handleSignOut = useCallback(() => {
    void signOut({ callbackUrl: logoutUrl ?? "/" });
  }, [logoutUrl]);

  const handleSignIn = useCallback(() => {
    void signIn("authentik");
  }, []);

  const userName = session?.user?.name || session?.user?.email;

  return (
    <NavigationMenu.Root className="fixed inset-x-0 top-0 z-50 border-b border-slate-200 bg-white/80 backdrop-blur">
      <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
        <span className="text-sm font-semibold tracking-tight text-slate-900">
          Knowledge Console
        </span>
        <div className="flex items-center gap-3">
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

          <div className="flex items-center gap-3 rounded-full border border-slate-200 bg-white/70 px-3 py-1 shadow-sm">
            <div className="flex flex-col leading-tight text-right">
              <span className="text-[11px] uppercase tracking-wide text-slate-500">Пользователь</span>
              <span className="text-sm font-medium text-slate-900">
                {status === "loading" ? "Загрузка…" : userName || "—"}
              </span>
              {session?.user?.email && userName !== session.user.email && (
                <span className="text-xs text-slate-600">{session.user.email}</span>
              )}
            </div>

            {status === "unauthenticated" ? (
              <button
                type="button"
                onClick={handleSignIn}
                className="inline-flex items-center rounded-full bg-indigo-600 px-3 py-1 text-xs font-semibold text-white transition hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-500"
              >
                Войти
              </button>
            ) : (
              <button
                type="button"
                onClick={handleSignOut}
                className="inline-flex items-center rounded-full bg-slate-900 px-3 py-1 text-xs font-semibold text-white transition hover:bg-slate-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-500 disabled:opacity-70"
                disabled={status === "loading"}
              >
                Выйти
              </button>
            )}
          </div>
        </div>
      </div>
    </NavigationMenu.Root>
  );
}
