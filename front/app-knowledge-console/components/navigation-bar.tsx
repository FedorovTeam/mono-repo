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
      "inline-flex items-center rounded-full px-4 py-2 text-sm font-medium transition-colors",
      "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-500",
      isActive ? "bg-indigo-100 text-indigo-700" : "text-slate-700 hover:bg-slate-100",
    ].join(" ");

export default function NavigationBar() {
  const pathname = usePathname();
  const { data: session, status } = useSession();

  const isActive = (href: string) => {
    if (href === "/") return pathname === "/";
    return pathname?.startsWith(href);
  };

  const postLogoutRedirect =
      process.env.NEXT_PUBLIC_AUTHENTIK_POST_LOGOUT_REDIRECT ??
      (typeof window !== "undefined" ? window.location.origin : undefined);

  const logoutUrl = useMemo(() => {
    if (!process.env.NEXT_PUBLIC_AUTHENTIK_LOGOUT_URL) return null;
    const redirectUrl = postLogoutRedirect ?? "/";
    const separator = process.env.NEXT_PUBLIC_AUTHENTIK_LOGOUT_URL.includes("?") ? "&" : "?";
    return `${process.env.NEXT_PUBLIC_AUTHENTIK_LOGOUT_URL}${separator}post_logout_redirect_uri=${encodeURIComponent(
        redirectUrl,
    )}`;
  }, [postLogoutRedirect]);

  const handleSignOut = useCallback(() => {
    void signOut({ callbackUrl: logoutUrl ?? "/" });
  }, [logoutUrl]);

  const handleSignIn = useCallback(() => {
    void signIn("authentik");
  }, []);

  const userName = session?.user?.name || session?.user?.email;
  const userEmail = session?.user?.email;

  return (
      <NavigationMenu.Root className="fixed inset-x-0 top-0 z-50 border-b border-slate-200 bg-white/80 backdrop-blur">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
          {/* LEFT: Brand + Navigation */}
          <div className="flex items-center gap-4">
            <Link href="/" className="text-sm font-semibold tracking-tight text-slate-900 hover:opacity-80">
              Knowledge Console
            </Link>

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

          {/* RIGHT: User chip + Auth button */}
          <div className="flex items-center gap-2">
            {status !== "unauthenticated" && (
                <div className="flex items-center gap-3 rounded-full border border-slate-200 bg-white/70 px-3 py-1 shadow-sm">
                  <div className="flex flex-col leading-tight">
                    <span className="text-[11px] uppercase tracking-wide text-slate-500">Пользователь</span>
                    <span className="text-sm font-medium text-slate-900">
                  {status === "loading" ? "Загрузка…" : userEmail || "—"}
                </span>
                  </div>
                </div>
            )}

            {status === "unauthenticated" ? (
                <button
                    type="button"
                    onClick={handleSignIn}
                    className="
                               inline-flex items-center rounded-full px-4 py-2 text-sm font-medium
                               text-indigo-700 bg-indigo-50 hover:bg-indigo-100
                               transition-colors
                               focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2
                               focus-visible:outline-indigo-500">
                  Войти
                </button>

            ) : (
                <button
                    type="button"
                    onClick={handleSignOut}
                    disabled={status === "loading"}
                    className="
                                inline-flex items-center rounded-full px-4 py-2 text-sm font-medium
                                text-red-700 bg-red-50 hover:bg-red-100
                                transition-colors
                                focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-red-500
                                disabled:opacity-60">
                  Выйти
                </button>

            )}
          </div>
        </div>
      </NavigationMenu.Root>
  );
}
