"use client";

import type { ReactNode } from "react";
import { SessionProvider } from "next-auth/react";

export default function AppSessionProvider({ children }: { children: ReactNode }) {
  return <SessionProvider>{children}</SessionProvider>;
}
