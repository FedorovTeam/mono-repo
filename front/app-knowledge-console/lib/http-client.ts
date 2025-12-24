'use client';

import type { AppRouterInstance } from 'next/dist/shared/lib/app-router-context.shared-runtime';
import { getSession } from 'next-auth/react';

import { SilentSignInError, performSilentSignIn } from './auth-flow';

export { SilentSignInError } from './auth-flow';

type FetchWithAuthOptions = RequestInit & {
  retryOnUnauthorized?: boolean;
  router?: AppRouterInstance;
};

export const fetchWithAuth = async (
  input: RequestInfo | URL,
  init: FetchWithAuthOptions = {},
): Promise<Response> => {
  const { retryOnUnauthorized = true, router, ...fetchInit } = init;

  const response = await fetch(input, fetchInit);

  if (response.status !== 401 || !retryOnUnauthorized) {
    return response;
  }

  if (typeof window === 'undefined') {
    return response;
  }

  try {
    await performSilentSignIn('fetchWithAuth', { router });
    console.info('Silent sign-in выполнен успешно, повторяем запрос.');
    const session = await getSession();

    if (!session?.accessToken) {
      throw new SilentSignInError('Сессия не была восстановлена.', {
        interactionRequired: true,
      });
    }
  } catch (error) {
    if (error instanceof SilentSignInError) {
      throw error;
    }

    console.error('Сбой при silent sign-in Authentik:', error);
    throw new SilentSignInError('Не удалось автоматически восстановить сессию.');
  }

  const retryResponse = await fetch(input, fetchInit);

  if (retryResponse.status === 401) {
    throw new SilentSignInError('Сессия недействительна. Войдите вручную.', {
      interactionRequired: true,
    });
  }

  return retryResponse;
};
