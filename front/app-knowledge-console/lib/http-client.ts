// front/app-knowledge-console/lib/http-client.ts
'use client';

import {
  SilentSignInError,
  isSilentSignInNavigationInProgress,
  performSilentSignIn,
} from './auth-flow';
import { createLogger } from './logger';

export { SilentSignInError } from './auth-flow';

type FetchWithAuthOptions = RequestInit & {
  retryOnUnauthorized?: boolean;
};

const REAUTH_REQUIRED_STATUS = 440;

let restorationPromise: Promise<void> | null = null;

const waitForRestoration = async (context: string): Promise<void> => {
  if (!restorationPromise) {
    restorationPromise = performSilentSignIn(context).finally(() => {
      restorationPromise = null;
    });
  }

  return restorationPromise;
};

export const fetchWithAuth = async (
  input: RequestInfo | URL,
  init: FetchWithAuthOptions = {},
): Promise<Response> => {
  const { retryOnUnauthorized = true, ...fetchInit } = init;
  const logger = createLogger({
    operation: 'fetchWithAuth',
    target: typeof input === 'string' ? input : input instanceof URL ? input.toString() : undefined,
  });

  const response = await fetch(input, fetchInit);

  const shouldRetry =
    (response.status === 401 || response.status === REAUTH_REQUIRED_STATUS) &&
    retryOnUnauthorized;

  if (!shouldRetry) {
    return response;
  }

  if (typeof window === 'undefined') {
    return response;
  }

  try {
    await waitForRestoration('fetchWithAuth');
    logger.info('Silent sign-in выполнен успешно, повторяем запрос без ожидания.', {
      status: response.status,
    });
  } catch (error) {
    if (error instanceof SilentSignInError) {
      throw error;
    }

    logger.error('Сбой при silent sign-in Authentik', { error });
    throw new SilentSignInError('Не удалось автоматически восстановить сессию.', {
      interactionRequired: true,
    });
  }

  if (isSilentSignInNavigationInProgress()) {
    logger.info('Навигация после silent sign-in активна, пропускаем повторный запрос.', {
      status: response.status,
    });
    return response;
  }

  const retryResponse = await fetch(input, fetchInit);

  if (retryResponse.status === REAUTH_REQUIRED_STATUS) {
    logger.warn('Повторный статус 440 после восстановления сессии, требуется интерактивный вход.', {
      status: retryResponse.status,
    });
  }

  if (retryResponse.status === 401 || retryResponse.status === REAUTH_REQUIRED_STATUS) {
    throw new SilentSignInError(
      'Сессия недействительна или восстановление не удалось. Войдите вручную.',
      {
        interactionRequired: true,
      },
    );
  }

  return retryResponse;
};
