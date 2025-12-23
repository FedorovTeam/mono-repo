'use client';

import Link from 'next/link';
import { useParams } from 'next/navigation';
import React, { useCallback, useEffect, useMemo, useState } from 'react';

type KnowledgeDocumentStatus =
  | 'UPLOADED'
  | 'STORED_ORIGINAL'
  | 'CONVERTED_TO_MARKDOWN'
  | 'PUBLISHED_TO_GITHUB'
  | 'FAILED';

type KnowledgeDocument = {
  id: string;
  status: KnowledgeDocumentStatus;
  path: string;
  repository: string;
  repositoryOwner?: string | null;
  branch: string;
  jobId?: string | null;
  error?: string | null;
  fileName: string;
  createdAt: string;
  originalDownloadUrl?: string | null;
};

type KnowledgeDocumentResponse = {
  knowledgeDocument: KnowledgeDocument | null;
};

const STATUS_LABELS: Record<KnowledgeDocumentStatus, string> = {
  UPLOADED: 'Файл принят',
  STORED_ORIGINAL: 'Оригинал сохранён',
  CONVERTED_TO_MARKDOWN: 'Преобразован в Markdown',
  PUBLISHED_TO_GITHUB: 'Опубликован в GitHub',
  FAILED: 'Ошибка обработки',
};

const STATUS_FLOW: KnowledgeDocumentStatus[] = [
  'UPLOADED',
  'STORED_ORIGINAL',
  'CONVERTED_TO_MARKDOWN',
  'PUBLISHED_TO_GITHUB',
];

const generateCorrelationId = () =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `corr-${Date.now()}`;

const formatUploadDate = (value: string) => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '—';
  }

  return new Intl.DateTimeFormat('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
    .format(date)
    .replace(',', '');
};

const simplifyError = (value?: string | null) => {
  if (!value) return null;
  const normalized = value.replace(/[\r\n]+/g, ' ').trim();
  if (normalized.length === 0) return null;
  if (normalized.length > 180) {
    return `${normalized.slice(0, 177)}...`;
  }
  return normalized;
};

const getRepositoryLink = (document: KnowledgeDocument) => {
  if (!document.repository || !document.path) {
    return null;
  }

  const normalizedPath = document.path.replace(/^\//, '');
  const repository = document.repositoryOwner ? `${document.repositoryOwner}/${document.repository}` : document.repository;
  return `https://github.com/${repository}/tree/${document.branch}/${normalizedPath}`;
};

export default function DocumentDetailsPage() {
  const params = useParams<{ id: string }>();
  const documentId = useMemo(() => {
    const value = params?.id;
    if (typeof value === 'string') return value;
    if (Array.isArray(value) && value.length > 0) return value[0];
    return '';
  }, [params]);

  const [document, setDocument] = useState<KnowledgeDocument | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const gatewayUrl = process.env.NEXT_PUBLIC_GATEWAY_URL;
  const apiKey = process.env.NEXT_PUBLIC_API_KEY;

  const fetchDocument = useCallback(async () => {
    if (!documentId) {
      setError('Идентификатор документа не указан.');
      setIsLoading(false);
      return;
    }

    if (!gatewayUrl || !apiKey) {
      setError('NEXT_PUBLIC_GATEWAY_URL или NEXT_PUBLIC_API_KEY не заданы.');
      setIsLoading(false);
      return;
    }

    try {
      setIsLoading(true);
      setError(null);

      const query = `
        query KnowledgeDocument($id: ID!) {
          knowledgeDocument(id: $id) {
            id
            status
            path
            repository
            repositoryOwner
            branch
            jobId
            error
            fileName
            createdAt
            originalDownloadUrl
          }
        }
      `;

      const res = await fetch(gatewayUrl, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-API-Key': apiKey,
          'X-Correlation-ID': generateCorrelationId(),
        },
        body: JSON.stringify({ query, variables: { id: documentId } }),
      });

      if (!res.ok) {
        throw new Error(`Ошибка загрузки документа: ${res.status} ${res.statusText}`);
      }

      const json = (await res.json()) as { data?: KnowledgeDocumentResponse; errors?: { message: string }[] };
      if (json.errors?.length) {
        throw new Error(json.errors.map((e) => e.message).join('; '));
      }

      const doc = json.data?.knowledgeDocument;
      if (!doc) {
        setDocument(null);
        setError('Документ не найден.');
        return;
      }

      setDocument(doc);
    } catch (err) {
      console.error(err);
      setError('Не удалось загрузить данные документа. Попробуйте обновить страницу.');
    } finally {
      setIsLoading(false);
    }
  }, [apiKey, documentId, gatewayUrl]);

  useEffect(() => {
    fetchDocument();
  }, [fetchDocument]);

  const simplifiedError = simplifyError(document?.error);
  const repositoryLink = document ? getRepositoryLink(document) : null;
  const downloadAvailable = !!document?.originalDownloadUrl;

  return (
    <main className="px-4 pb-12">
      <div className="mx-auto max-w-5xl space-y-6">
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-3">
            <Link
              href="/documents"
              className="inline-flex items-center gap-2 text-sm text-slate-700 underline-offset-4 hover:text-slate-900 hover:underline"
            >
              ← К списку документов
            </Link>
            <p className="text-xs text-slate-600">ID: {documentId}</p>
          </div>
          <h1 className="text-2xl font-semibold text-slate-900">Детали документа</h1>
          <p className="text-sm text-slate-600">
            Здесь отображаются статусы прохождения документа по цепочке и ссылка для скачивания оригинального файла.
          </p>
        </div>

        {error && (
          <div className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">{error}</div>
        )}

        <div className="grid gap-4 lg:grid-cols-3">
          <div className="space-y-4 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm lg:col-span-2">
            {isLoading && <p className="text-sm text-slate-600">Загружаем данные документа…</p>}

            {!isLoading && document && (
              <div className="space-y-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                  <div className="space-y-1">
                    <p className="text-lg font-semibold text-slate-900">{document.fileName || 'Без названия'}</p>
                    <p className="text-sm text-slate-700">
                      Репозиторий:{' '}
                      <span className="font-mono text-slate-800">
                        {document.repositoryOwner ? `${document.repositoryOwner}/` : ''}
                        {document.repository}
                      </span>
                    </p>
                    <p className="text-sm text-slate-700">
                      Ветка: <span className="font-mono text-slate-800">{document.branch}</span>
                    </p>
                    <p className="text-sm text-slate-700">Путь в репозитории: {document.path}</p>
                    <p className="text-sm text-slate-700">Загружено: {formatUploadDate(document.createdAt)}</p>
                    {document.jobId && (
                      <p className="text-xs text-slate-600">
                        jobId: <span className="font-mono">{document.jobId}</span>
                      </p>
                    )}
                  </div>

                  <div className="flex flex-col gap-2 sm:items-end">
                    <StatusBadge status={document.status} />
                    {repositoryLink && (
                      <a
                        href={repositoryLink}
                        target="_blank"
                        rel="noreferrer"
                        className="text-sm text-indigo-700 underline-offset-4 hover:text-indigo-800 hover:underline"
                      >
                        Открыть в GitHub
                      </a>
                    )}
                  </div>
                </div>

                <StatusStepper status={document.status} />

                {simplifiedError && (
                  <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
                    Ошибка: {simplifiedError}
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
            <h2 className="text-lg font-semibold text-slate-900">Действия</h2>
            <p className="mt-1 text-sm text-slate-600">Скачайте загруженный пользователем файл при необходимости.</p>

            <div className="mt-4 flex flex-col gap-3">
              <a
                href={downloadAvailable ? document?.originalDownloadUrl ?? '#' : '#'}
                download={document?.fileName ?? 'document'}
                target={downloadAvailable ? '_blank' : undefined}
                rel="noreferrer"
                className={cn(
                  'inline-flex items-center justify-center rounded-lg px-4 py-2 text-sm font-medium transition shadow-sm',
                  downloadAvailable
                    ? 'bg-emerald-600 text-white hover:bg-emerald-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-emerald-400'
                    : 'cursor-not-allowed bg-slate-100 text-slate-500 opacity-70',
                )}
                aria-disabled={!downloadAvailable}
              >
                Скачать оригинал
              </a>

              <button
                type="button"
                onClick={fetchDocument}
                disabled={isLoading}
                className="inline-flex items-center justify-center rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-400 disabled:cursor-not-allowed disabled:opacity-60"
              >
                Обновить
              </button>
            </div>
          </div>
        </div>
      </div>
    </main>
  );
}

function StatusStepper({ status }: { status: KnowledgeDocumentStatus }) {
  const isFailed = status === 'FAILED';
  const activeIndex = isFailed ? STATUS_FLOW.length - 1 : Math.max(STATUS_FLOW.indexOf(status), 0);

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
      <p className="text-sm font-medium text-slate-900">Ход обработки</p>
      <div className="mt-4 flex flex-col gap-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:gap-3">
          {STATUS_FLOW.map((step, index) => {
            const state = getStepState(index, activeIndex, isFailed);
            const lineColor = state === 'done' ? 'bg-emerald-500/50' : state === 'failed' ? 'bg-red-500/50' : 'bg-slate-200';

            return (
              <div key={step} className="flex flex-1 items-center gap-3">
                <div className="flex flex-col gap-1">
                  <div className="flex items-center gap-2">
                    <span className={cn('flex h-5 w-5 items-center justify-center rounded-full', stepRingClass(state))}>
                      <span className={cn('h-2.5 w-2.5 rounded-full', stepDotClass(state))} aria-hidden />
                    </span>
                    <span className="text-sm text-slate-900">{STATUS_LABELS[step]}</span>
                  </div>
                  <span className="text-[11px] uppercase tracking-wide text-slate-600">{step}</span>
                </div>

                {index < STATUS_FLOW.length - 1 && <div className={cn('hidden h-px flex-1 sm:block', lineColor)} aria-hidden />}
              </div>
            );
          })}
        </div>

        {isFailed && <p className="text-sm text-red-700">Обработка остановилась с ошибкой.</p>}
      </div>
    </div>
  );
}

function getStepState(index: number, activeIndex: number, isFailed: boolean) {
  if (isFailed && index === activeIndex) return 'failed';
  if (index < activeIndex) return 'done';
  if (index === activeIndex && !isFailed) return 'active';
  return 'pending';
}

function stepRingClass(state: ReturnType<typeof getStepState>) {
  return cn(
    'border px-0.5 py-0.5 shadow-sm',
    state === 'done' && 'border-emerald-300 bg-emerald-50 text-emerald-700',
    state === 'active' && 'border-indigo-300 bg-indigo-50 text-indigo-700',
    state === 'failed' && 'border-red-300 bg-red-50 text-red-700',
    state === 'pending' && 'border-slate-200 bg-white text-slate-500',
  );
}

function stepDotClass(state: ReturnType<typeof getStepState>) {
  return cn(
    state === 'done' && 'bg-emerald-500',
    state === 'active' && 'bg-indigo-500 animate-pulse',
    state === 'failed' && 'bg-red-500 animate-pulse',
    state === 'pending' && 'bg-slate-300',
  );
}

function StatusBadge({ status }: { status: KnowledgeDocumentStatus }) {
  return (
    <span className={cn('inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs font-medium', STATUS_STYLES[status])}>
      <span className="h-2 w-2 rounded-full bg-current opacity-80" aria-hidden />
      {STATUS_LABELS[status] ?? status}
    </span>
  );
}

const STATUS_STYLES: Record<KnowledgeDocumentStatus, string> = {
  UPLOADED: 'bg-slate-50 text-slate-800 border-slate-300',
  STORED_ORIGINAL: 'bg-blue-50 text-blue-800 border-blue-300',
  CONVERTED_TO_MARKDOWN: 'bg-indigo-50 text-indigo-800 border-indigo-300',
  PUBLISHED_TO_GITHUB: 'bg-emerald-50 text-emerald-800 border-emerald-300',
  FAILED: 'bg-red-50 text-red-800 border-red-300',
};

function cn(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(' ');
}
