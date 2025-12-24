'use client';

import { signIn } from 'next-auth/react';

const interactionErrorCodes = ['interaction_required', 'interactionrequiredautherror'];
const SILENT_SIGN_IN_TIMEOUT_MS = 15_000;
const MAX_SILENT_SIGN_IN_FAILURES = 3;

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

let activeAttempt: Promise<void> | null = null;
let failedAttempts = 0;

const createSilentSignInAttempt = (context: string): Promise<void> => {
  return new Promise((resolve, reject) => {
    const timeoutId = setTimeout(() => {
      console.error('[auth-flow] Таймаут silent sign-in — отмена попытки.', { context });
      reject(new SilentSignInError('Таймаут восстановления сессии.', true));
    }, SILENT_SIGN_IN_TIMEOUT_MS);

    const clearTimer = () => clearTimeout(timeoutId);

    signIn('authentik', {
      redirect: true,
      prompt: 'none',
      callbackUrl: window.location.href,
    })
      .then((signInResult) => {
        clearTimer();

        if (signInResult?.error) {
          if (isInteractionRequired(signInResult.error)) {
            console.warn('[auth-flow] Требуется ручной вход после silent sign-in.', {
              context,
              error: signInResult.error,
            });
            reject(new SilentSignInError('Для продолжения войдите вручную.', true));
            return;
          }

          console.error('[auth-flow] Ошибка silent sign-in Authentik.', {
            context,
            error: signInResult.error,
          });
          reject(new SilentSignInError('Не удалось автоматически восстановить сессию.'));
          return;
        }

        console.info('[auth-flow] Silent sign-in выполнен успешно.', { context });
        resolve();
      })
      .catch((error) => {
        clearTimer();
        console.error('[auth-flow] Сбой silent sign-in Authentik.', { context, error });
        reject(new SilentSignInError('Не удалось автоматически восстановить сессию.'));
      });
  });
};

export const performSilentSignIn = async (context: string): Promise<void> => {
  if (typeof window === 'undefined') {
    throw new SilentSignInError('Silent sign-in доступен только в браузере.');
  }

  if (failedAttempts >= MAX_SILENT_SIGN_IN_FAILURES) {
    console.error('[auth-flow] Лимит попыток silent sign-in превышен.', {
      context,
      failedAttempts,
    });
    throw new SilentSignInError('Превышено число попыток восстановления сессии.', true);
  }

  if (activeAttempt) {
    console.info('[auth-flow] Обнаружена активная попытка silent sign-in, ожидаем.', { context });
    return activeAttempt;
  }

  console.info('[auth-flow] Запускаем silent sign-in.', { context });

  const attempt = createSilentSignInAttempt(context)
    .then(() => {
      failedAttempts = 0;
    })
    .catch((error) => {
      failedAttempts += 1;
      throw error;
    })
    .finally(() => {
      activeAttempt = null;
    });

  activeAttempt = attempt;

  return attempt;
};

export const hasActiveSilentSignIn = () => activeAttempt !== null;
