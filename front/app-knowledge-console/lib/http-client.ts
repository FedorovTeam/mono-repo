'use client';

import { signIn } from 'next-auth/react';

type FetchWithAuthOptions = RequestInit & {
  retryOnUnauthorized?: boolean;
};

const interactionErrorCodes = ['interaction_required', 'interactionrequiredautherror'];

export class SilentSignInError extends Error {
  interactionRequired: boolean;

  constructor(message: string, interactionRequired = false) {
    super(message);
    this.name = 'SilentSignInError';
    this.interactionRequired = interactionRequired;
  }
}

const isInteractionRequired = (error?: string | null) => {
  if (!error) return false;
  const normalized = error.toLowerCase();
  return interactionErrorCodes.some((code) => normalized.includes(code));
};

export const fetchWithAuth = async (
  input: RequestInfo | URL,
  init: FetchWithAuthOptions = {},
): Promise<Response> => {
  const { retryOnUnauthorized = true, ...fetchInit } = init;

  const response = await fetch(input, fetchInit);

  if (response.status !== 401 || !retryOnUnauthorized) {
    return response;
  }

  if (typeof window === 'undefined') {
    return response;
  }

  console.info('Получен 401, запускаем silent sign-in через Authentik');

  try {
    const signInResult = await signIn('authentik', {
      redirect: true,
      prompt: 'none',
      callbackUrl: window.location.href,
    });

    if (signInResult?.error) {
      if (isInteractionRequired(signInResult.error)) {
        console.warn('Authentik вернул interaction_required — требуется ручной вход.');
        throw new SilentSignInError('Для продолжения войдите вручную.', true);
      }

      console.error('Ошибка silent sign-in Authentik:', signInResult.error);
      throw new SilentSignInError('Не удалось автоматически восстановить сессию.');
    }

    console.info('Silent sign-in выполнен успешно, повторяем запрос.');
  } catch (error) {
    if (error instanceof SilentSignInError) {
      throw error;
    }

    console.error('Сбой при silent sign-in Authentik:', error);
    throw new SilentSignInError('Не удалось автоматически восстановить сессию.');
  }

  const retryResponse = await fetch(input, fetchInit);

  if (retryResponse.status === 401) {
    throw new SilentSignInError('Сессия недействительна. Войдите вручную.', true);
  }

  return retryResponse;
};
