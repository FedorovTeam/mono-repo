// front/app-knowledge-console/app/auth/signin/page.tsx
"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { signIn, useSession } from "next-auth/react";
import { Suspense, useEffect, useMemo, useState } from "react";

import { mapAuthError } from "@/lib/auth-flow";
import { createLogger } from "@/lib/logger";

const BENEFITS = [
  "Доступ к загрузке и просмотру документов",
  "Сохранение контекста перехода после входа",
  "Единый вход через Authentik",
];

function SignInContent() {
  const searchParams = useSearchParams();
  const { status } = useSession();

  const [isManualLoading, setIsManualLoading] = useState(false);
  const [providerUnavailable, setProviderUnavailable] = useState(false);

  const logger = useMemo(
      () => createLogger({ operation: "auth sign-in", target: "/api/auth" }),
      [],
  );

  // Safe callbackUrl (only same-origin, or relative)
  const callbackUrl = useMemo(() => {
    const requested = searchParams?.get("callbackUrl");
    if (!requested) return "/";

    try {
      const decoded = decodeURIComponent(requested);

      // allow relative paths
      if (decoded.startsWith("/")) return decoded;

      // allow absolute same-origin
      try {
        const parsed = new URL(decoded);
        const currentOrigin =
            typeof window !== "undefined" ? window.location.origin : undefined;

        if (currentOrigin && parsed.origin === currentOrigin) {
          const pathWithQuery = `${parsed.pathname}${parsed.search}${parsed.hash}`;
          return pathWithQuery || "/";
        }
      } catch {
        // ignore
      }
    } catch {
      // ignore
    }

    return "/";
  }, [searchParams]);

  const nextUrl =
      callbackUrl === "/" ? "главную страницу" : `страницу ${callbackUrl}`;

  const errorMessage = searchParams?.get("error") ?? null;
  const mappedError = mapAuthError(errorMessage);

  // We DO NOT auto-start signIn from this page to avoid multiple parallel OAuth flows
  // (fetchWithAuth / AutoSignInOnFocus / manual click can otherwise race and cause OAuthCallback).
  const isLoading = isManualLoading || status === "loading";

  useEffect(() => {
    let isCancelled = false;

    const fetchProviderStatus = async () => {
      try {
        const response = await fetch("/api/auth/provider-status", {
          cache: "no-store",
        });
        if (!response.ok) return;

        const data = (await response.json()) as { reachable?: boolean } | null;
        if (!isCancelled && data?.reachable === false) {
          setProviderUnavailable(true);
        }
      } catch (error) {
        logger.warn("Не удалось проверить статус Authentik", { error });
      }
    };

    void fetchProviderStatus();

    return () => {
      isCancelled = true;
    };
  }, [logger]);

  const friendlyErrorMessage = useMemo(() => {
    if (!errorMessage) return null;

    if (mappedError.interactionRequired) {
      return "Чтобы продолжить, подтвердите вход вручную через Authentik.";
    }

    if (errorMessage === "AccessDenied") {
      return "Доступ запрещён. Обратитесь к администратору.";
    }

    // For non-retryable errors, show raw error code (developer-friendly)
    if (!mappedError.retryable) {
      return errorMessage;
    }

    // retryable errors are handled by user clicking sign in
    return null;
  }, [errorMessage, mappedError.interactionRequired, mappedError.retryable]);

  const statusHint = useMemo(() => {
    if (status === "loading") return "Проверяем сессию…";

    if (mappedError.interactionRequired) {
      return "Бесшумный вход недоступен. Пожалуйста, авторизуйтесь вручную.";
    }

    if (errorMessage && mappedError.retryable) {
      return "Сессия истекла или требует повторного входа. Нажмите кнопку «Войти» ниже.";
    }

    if (errorMessage) {
      return "Не удалось выполнить вход автоматически. Нажмите «Войти» и продолжите через Authentik.";
    }

    return "Нажмите «Войти», чтобы продолжить через Authentik.";
  }, [errorMessage, mappedError.interactionRequired, mappedError.retryable, status]);

  const handleSignIn = () => {
    setIsManualLoading(true);

    // allow future auto-login attempts elsewhere
    if (typeof window !== "undefined") {
      sessionStorage.removeItem("authAutoLoginDisabled");
    }

    // IMPORTANT: keep the original target page
    // Do not force prompt=consent here; let provider decide.
    void signIn("authentik", { callbackUrl });
  };

  return (
      <main className="flex min-h-screen items-center justify-center bg-slate-50 px-4 py-10">
        <div className="w-full max-w-3xl overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
          <div className="grid gap-6 md:grid-cols-2">
            <div className="flex flex-col gap-4 border-b border-slate-100 bg-gradient-to-br from-indigo-50 via-white to-white px-6 py-8 md:border-b-0 md:border-r">
              <div className="inline-flex w-fit items-center gap-2 rounded-full bg-indigo-100 px-3 py-1 text-xs font-semibold uppercase tracking-wide text-indigo-700">
                Авторизация
              </div>
              <div className="space-y-2">
                <h1 className="text-2xl font-semibold text-slate-900">
                  Вход через Authentik
                </h1>
                <p className="text-sm text-slate-600">
                  Пожалуйста, войдите, чтобы продолжить работу с Knowledge Console.
                  Мы вернём вас на{" "}
                  <span className="font-medium text-slate-900">{nextUrl}</span>{" "}
                  после успешной авторизации.
                </p>
              </div>
              <ul className="space-y-2 text-sm text-slate-700">
                {BENEFITS.map((item) => (
                    <li key={item} className="flex items-start gap-2">
                  <span
                      className="mt-1 h-2 w-2 rounded-full bg-indigo-500"
                      aria-hidden
                  />
                      <span>{item}</span>
                    </li>
                ))}
              </ul>
              <div className="pt-2 text-xs text-slate-500">
                Если у вас нет доступа, обратитесь к администратору Authentik.
              </div>
            </div>

            <div className="flex flex-col gap-4 px-6 py-8">
              <div className="space-y-2">
                <p className="text-sm font-medium text-slate-900">Войти</p>
                <p className="text-sm text-slate-600">
                  Используйте корпоративную учётную запись Authentik.
                </p>
              </div>

              <div className="space-y-3">
                <div className="rounded-lg border border-indigo-100 bg-indigo-50 px-3 py-2 text-sm text-indigo-900">
                  {statusHint}
                </div>

                {providerUnavailable && (
                    <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
                      Служба авторизации недоступна. Попробуйте позже или убедитесь,
                      что VPN включён.
                    </div>
                )}

                {friendlyErrorMessage && (
                    <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
                      {friendlyErrorMessage}
                    </div>
                )}
              </div>

              <button
                  type="button"
                  onClick={handleSignIn}
                  disabled={isLoading}
                  className="inline-flex items-center justify-center gap-2 rounded-xl bg-indigo-600 px-4 py-3 text-sm font-semibold text-white shadow-sm transition hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-400 disabled:cursor-not-allowed disabled:opacity-70"
              >
                {isLoading ? "Перенаправляем…" : "Войти через Authentik"}
              </button>

              <div className="text-sm text-slate-600">
                После входа можно перейти{" "}
                <Link
                    href="/"
                    className="font-semibold text-indigo-600 hover:text-indigo-500"
                >
                  на главную страницу
                </Link>{" "}
                или продолжить на запрошенной странице.
              </div>
            </div>
          </div>
        </div>
      </main>
  );
}

export default function SignInPage() {
  return (
      <Suspense
          fallback={
            <main className="flex min-h-screen items-center justify-center bg-slate-50 px-4 py-10">
              <div className="w-full max-w-3xl rounded-2xl border border-slate-200 bg-white px-6 py-10 text-center shadow-sm">
                <p className="text-sm font-medium text-slate-800">
                  Загружаем форму входа…
                </p>
              </div>
            </main>
          }
      >
        <SignInContent />
      </Suspense>
  );
}
