// front/app-knowledge-console/lib/session-restoration-store.ts
'use client';

import { useSyncExternalStore } from 'react';

type Listener = (isRestoring: boolean) => void;

let restoringSession = false;
const listeners = new Set<Listener>();

const emit = () => {
  listeners.forEach((listener) => listener(restoringSession));
};

const subscribe = (listener: Listener) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

const getSnapshot = () => restoringSession;

export const setRestoringSession = (isRestoring: boolean) => {
  if (restoringSession === isRestoring) {
    return;
  }

  restoringSession = isRestoring;
  emit();
};

export const useSessionRestorationState = () =>
  useSyncExternalStore(subscribe, getSnapshot, () => restoringSession);

export const getSessionRestorationState = () => restoringSession;
