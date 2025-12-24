'use client';

import type { AppRouterInstance } from 'next/dist/shared/lib/app-router-context.shared-runtime';
import { getSession } from 'next-auth/react';

import { SilentSignInError, performSilentSignIn } from './auth-flow';

export { SilentSignInError } from './auth-flow';

type FetchWithAuthOptions = RequestInit & {
  retryOnUnauthorized?: boolean;
  router?: AppRouterInstance;
};

const RESTORE_WAIT_TIMEOUT_MS = 15_000;

let restorationPromise: Promise<void> | null = null;

const waitForRestoration = async (
  context: string,
  router?: AppRouterInstance,
): Promise<void> => {
  if (!restorationPromise) {
    restorationPromise = performSilentSignIn(context, { router }).finally(() => {
      restorationPromise = null;
    });
  }

  return new Promise((resolve, reject) => {
    const timeoutId = setTimeout(() => {
      console.error('[http-client] Таймаут ожидания восстановления сессии.', { context });
      reject(
        new SilentSignInError('Не удалось дождаться восстановления сессии.', {
          interactionRequired: true,
        }),
      );
    }, RESTORE_WAIT_TIMEOUT_MS);

    restorationPromise
      ?.then(() => {
        clearTimeout(timeoutId);
        resolve();
      })
      .catch((error) => {
        clearTimeout(timeoutId);
        reject(error);
      });
  });
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
    await waitForRestoration('fetchWithAuth', router);
    console.info('Silent sign-in выполнен успешно, повторяем запрос.');

    const refreshedSessionResponse = await fetch('/api/auth/session');
    const session = (refreshedSessionResponse.ok
      ? await refreshedSessionResponse.json()
      : await getSession()) as Awaited<ReturnType<typeof getSession>>;

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
    throw new SilentSignInError(
      'Сессия недействительна или восстановление не удалось. Войдите вручную.',
      {
        interactionRequired: true,
      },
    );
  }

  return retryResponse;
};
