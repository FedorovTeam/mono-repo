'use client';

import { signIn } from 'next-auth/react';
import { SilentSignInError } from './http-client';

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

  const redirectPromise = signIn('authentik', callbackUrl ? { callbackUrl } : undefined);

  if (!redirectPromise) {
    fallbackRedirect(callbackUrl);
    return true;
  }

  redirectPromise.catch((signInError) => {
    console.error('[handleAuthError] Ошибка при попытке входа через Authentik', signInError);
    fallbackRedirect(callbackUrl);
  });

  return true;
};
