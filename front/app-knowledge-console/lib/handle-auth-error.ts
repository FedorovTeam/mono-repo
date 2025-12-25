// front/app-knowledge-console/lib/handle-auth-error.ts
'use client';

import { signIn } from 'next-auth/react';
import { SilentSignInError } from './http-client';
import { createLogger } from './logger';

type HandleAuthErrorOptions = {
  onInteractionStart?: () => void;
  callbackUrl?: string;
};

const fallbackRedirect = (callbackUrl?: string) => {
  if (typeof window === 'undefined' || !callbackUrl) {
    return;
  }

  window.location.href = `/auth/signin?callbackUrl=${encodeURIComponent(callbackUrl)}`;
};

export const handleAuthError = (error: unknown, options: HandleAuthErrorOptions = {}): boolean => {
  if (!(error instanceof SilentSignInError) || !error.interactionRequired) {
    return false;
  }

  options.onInteractionStart?.();

  const callbackUrl =
      options.callbackUrl ?? (typeof window !== 'undefined' ? window.location.href : undefined);

  if (typeof window !== 'undefined' && callbackUrl) {
    window.location.href = `/auth/signin?callbackUrl=${encodeURIComponent(callbackUrl)}`;
    return true;
  }

  return true;
};
