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

  const logger = createLogger({ operation: 'handleAuthError', target: '/api/auth/signin' });

  options.onInteractionStart?.();

  const callbackUrl =
    options.callbackUrl ?? (typeof window !== 'undefined' ? window.location.href : undefined);

  const redirectPromise = signIn('authentik', callbackUrl ? { callbackUrl } : undefined);

  if (!redirectPromise) {
    fallbackRedirect(callbackUrl);
    return true;
  }

  redirectPromise.catch((signInError) => {
    logger.error('Ошибка при попытке входа через Authentik', { error: signInError });
    fallbackRedirect(callbackUrl);
  });

  return true;
};
