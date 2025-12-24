"use client";

import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { SessionProvider, useSession } from "next-auth/react";

import { performSilentSignIn } from "@/lib/auth-flow";

const AUTO_SIGN_IN_COOLDOWN_MS = 60_000;

function AutoSignInOnFocus() {
  const { status } = useSession();
  const lastAttemptRef = useRef<number | null>(null);

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (status === "authenticated") {
      sessionStorage.removeItem("authAutoLoginDisabled");
    }
  }, [status]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (status !== "unauthenticated") return;

    const loginRequiredInQuery =
      new URLSearchParams(window.location.search).get("error") === "login_required";
    const isAutoLoginDisabled =
      sessionStorage.getItem("authAutoLoginDisabled") === "true" || loginRequiredInQuery;

    if (loginRequiredInQuery) {
      sessionStorage.setItem("authAutoLoginDisabled", "true");
    }

    if (isAutoLoginDisabled) {
      return;
    }

    let isCancelled = false;

    const attemptAutoSignIn = () => {
      if (isCancelled || status !== "unauthenticated") return;

      const now = Date.now();
      if (lastAttemptRef.current && now - lastAttemptRef.current < AUTO_SIGN_IN_COOLDOWN_MS) {
        return;
      }

      lastAttemptRef.current = now;

      void performSilentSignIn("AutoSignInOnFocus").catch((error) => {
        console.warn("Автоматический silent sign-in отклонён.", error);
        sessionStorage.setItem("authAutoLoginDisabled", "true");
      });
    };

    attemptAutoSignIn();

    const onFocus = () => attemptAutoSignIn();
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        attemptAutoSignIn();
      }
    };

    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibilityChange);

    return () => {
      isCancelled = true;
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [status]);

  return null;
}

export default function AppSessionProvider({ children }: { children: ReactNode }) {
  return (
    <SessionProvider>
      <AutoSignInOnFocus />
      {children}
    </SessionProvider>
  );
}
