"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { signIn } from "next-auth/react";
import { Suspense, useMemo, useState } from "react";

const BENEFITS = [
  "Доступ к загрузке и просмотру документов",
  "Сохранение контекста перехода после входа",
  "Единый вход через Authentik",
];

function SignInContent() {
  const searchParams = useSearchParams();
  const [isLoading, setIsLoading] = useState(false);

  const callbackUrl = useMemo(() => {
    const requested = searchParams?.get("callbackUrl");
    if (!requested) return "/";
    try {
      const decoded = decodeURIComponent(requested);
      return decoded.startsWith("/") ? decoded : "/";
    } catch {
      return "/";
    }
  }, [searchParams]);

  const nextUrl = callbackUrl === "/" ? "главную страницу" : `страницу ${callbackUrl}`;
  const errorMessage = searchParams?.get("error");

  const handleSignIn = () => {
    setIsLoading(true);
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
              <h1 className="text-2xl font-semibold text-slate-900">Вход через Authentik</h1>
              <p className="text-sm text-slate-600">
                Пожалуйста, войдите, чтобы продолжить работу с Knowledge Console. Мы вернём вас на{" "}
                <span className="font-medium text-slate-900">{nextUrl}</span> после успешной авторизации.
              </p>
            </div>
            <ul className="space-y-2 text-sm text-slate-700">
              {BENEFITS.map((item) => (
                <li key={item} className="flex items-start gap-2">
                  <span className="mt-1 h-2 w-2 rounded-full bg-indigo-500" aria-hidden />
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
              <p className="text-sm text-slate-600">Используйте корпоративную учётную запись Authentik.</p>
            </div>

            {errorMessage && (
              <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
                {errorMessage === "AccessDenied" ? "Доступ запрещён. Обратитесь к администратору." : errorMessage}
              </div>
            )}

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
              <Link href="/" className="font-semibold text-indigo-600 hover:text-indigo-500">
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
            <p className="text-sm font-medium text-slate-800">Загружаем форму входа…</p>
          </div>
        </main>
      }
    >
      <SignInContent />
    </Suspense>
  );
}
